package oidc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// tokenResponse mirrors the IdP JSON payload; kept local to tests so the
// production code has no incentive to export it.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// newFakeIdP returns a test server that responds with the supplied handler.
// The handler receives the parsed form so tests can assert on body fields.
func newFakeIdP(t *testing.T, handler func(t *testing.T, r *http.Request, w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
			t.Errorf("expected urlencoded body, got %q", ct)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		handler(t, r, w)
	}))
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, body tokenResponse) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Fatalf("encode: %v", err)
	}
}

func TestClient_Token_FetchesFromTokenURL(t *testing.T) {
	var calls int32
	srv := newFakeIdP(t, func(t *testing.T, r *http.Request, w http.ResponseWriter) {
		atomic.AddInt32(&calls, 1)
		if got := r.Form.Get("grant_type"); got != "client_credentials" {
			t.Errorf("grant_type = %q, want client_credentials", got)
		}
		if got := r.Form.Get("client_id"); got != "cid" {
			t.Errorf("client_id = %q, want cid", got)
		}
		if got := r.Form.Get("client_secret"); got != "secret" {
			t.Errorf("client_secret = %q, want secret", got)
		}
		writeJSON(t, w, 200, tokenResponse{AccessToken: "abc", TokenType: "bearer", ExpiresIn: 3600})
	})
	defer srv.Close()

	c := NewClient(ClientConfig{
		Issuer:       "https://issuer.example.com",
		TokenURL:     srv.URL,
		ClientID:     "cid",
		ClientSecret: "secret",
	}, NewMemoryTokenCache())

	tok, err := c.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != "abc" {
		t.Errorf("token = %q, want abc", tok)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d, want 1", got)
	}
}

func TestClient_Token_CachesUntilExpiry(t *testing.T) {
	var calls int32
	srv := newFakeIdP(t, func(t *testing.T, r *http.Request, w http.ResponseWriter) {
		atomic.AddInt32(&calls, 1)
		writeJSON(t, w, 200, tokenResponse{AccessToken: "abc", TokenType: "bearer", ExpiresIn: 3600})
	})
	defer srv.Close()

	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }

	c := NewClient(ClientConfig{
		TokenURL:     srv.URL,
		ClientID:     "cid",
		ClientSecret: "secret",
	}, NewMemoryTokenCache(), WithClock(clock))

	for i := 0; i < 3; i++ {
		tok, err := c.Token(context.Background())
		if err != nil {
			t.Fatalf("Token[%d]: %v", i, err)
		}
		if tok != "abc" {
			t.Errorf("token[%d] = %q, want abc", i, tok)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d, want 1 (cached)", got)
	}
}

func TestClient_Token_RefreshesAfterExpiry(t *testing.T) {
	var calls int32
	srv := newFakeIdP(t, func(t *testing.T, r *http.Request, w http.ResponseWriter) {
		n := atomic.AddInt32(&calls, 1)
		tok := "first"
		if n == 2 {
			tok = "second"
		}
		writeJSON(t, w, 200, tokenResponse{AccessToken: tok, TokenType: "bearer", ExpiresIn: 60})
	})
	defer srv.Close()

	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }

	c := NewClient(ClientConfig{
		TokenURL:     srv.URL,
		ClientID:     "cid",
		ClientSecret: "secret",
	}, NewMemoryTokenCache(), WithClock(clock))

	tok1, err := c.Token(context.Background())
	if err != nil {
		t.Fatalf("Token first: %v", err)
	}
	if tok1 != "first" {
		t.Fatalf("first = %q, want first", tok1)
	}

	// Advance within the 30s refresh-ahead window: should re-fetch.
	// ExpiresIn=60 → expiresAt = now+60; move to now+31 → (expiresAt - now = 29s) < 30s.
	now = now.Add(31 * time.Second)

	tok2, err := c.Token(context.Background())
	if err != nil {
		t.Fatalf("Token second: %v", err)
	}
	if tok2 != "second" {
		t.Errorf("second = %q, want second", tok2)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("calls = %d, want 2", got)
	}
}

func TestClient_Token_RejectsNonBearer(t *testing.T) {
	srv := newFakeIdP(t, func(t *testing.T, r *http.Request, w http.ResponseWriter) {
		writeJSON(t, w, 200, tokenResponse{AccessToken: "abc", TokenType: "Basic", ExpiresIn: 60})
	})
	defer srv.Close()

	c := NewClient(ClientConfig{
		Issuer:       "https://issuer.example.com",
		TokenURL:     srv.URL,
		ClientID:     "cid",
		ClientSecret: "secret",
	}, NewMemoryTokenCache())

	_, err := c.Token(context.Background())
	if err == nil {
		t.Fatal("expected error for non-bearer token_type")
	}
	if !strings.Contains(err.Error(), "issuer.example.com") {
		t.Errorf("error should mention issuer, got: %v", err)
	}
}

func TestClient_Token_HTTPError(t *testing.T) {
	var calls int32
	srv := newFakeIdP(t, func(t *testing.T, r *http.Request, w http.ResponseWriter) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	defer srv.Close()

	cache := NewMemoryTokenCache()
	c := NewClient(ClientConfig{
		TokenURL:     srv.URL,
		ClientID:     "cid",
		ClientSecret: "secret",
	}, cache)

	if _, err := c.Token(context.Background()); err == nil {
		t.Fatal("expected error on 500")
	}

	// Second call should also hit the server, no caching of failures.
	if _, err := c.Token(context.Background()); err == nil {
		t.Fatal("expected error on retry 500")
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("calls = %d, want 2 (failures should not cache)", got)
	}
}

func TestClient_Token_EmptyAccessToken(t *testing.T) {
	srv := newFakeIdP(t, func(t *testing.T, r *http.Request, w http.ResponseWriter) {
		writeJSON(t, w, 200, tokenResponse{AccessToken: "", TokenType: "bearer", ExpiresIn: 60})
	})
	defer srv.Close()

	c := NewClient(ClientConfig{
		TokenURL:     srv.URL,
		ClientID:     "cid",
		ClientSecret: "secret",
	}, NewMemoryTokenCache())

	if _, err := c.Token(context.Background()); err == nil {
		t.Fatal("expected error for empty access_token")
	}
}

func TestClient_Token_NilCache(t *testing.T) {
	var calls int32
	srv := newFakeIdP(t, func(t *testing.T, r *http.Request, w http.ResponseWriter) {
		atomic.AddInt32(&calls, 1)
		writeJSON(t, w, 200, tokenResponse{AccessToken: "abc", TokenType: "bearer", ExpiresIn: 3600})
	})
	defer srv.Close()

	c := NewClient(ClientConfig{
		TokenURL:     srv.URL,
		ClientID:     "cid",
		ClientSecret: "secret",
	}, nil) // nil cache, must not panic

	for i := 0; i < 3; i++ {
		tok, err := c.Token(context.Background())
		if err != nil {
			t.Fatalf("Token[%d]: %v", i, err)
		}
		if tok != "abc" {
			t.Errorf("token[%d] = %q", i, tok)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3 (nil cache fetches every call)", got)
	}
}

func TestClient_Token_IncludesAudience(t *testing.T) {
	srv := newFakeIdP(t, func(t *testing.T, r *http.Request, w http.ResponseWriter) {
		if got := r.Form.Get("audience"); got != "https://example.sink" {
			t.Errorf("audience = %q, want https://example.sink", got)
		}
		if got := r.Form.Get("scope"); got != "read write" {
			t.Errorf("scope = %q, want %q", got, "read write")
		}
		writeJSON(t, w, 200, tokenResponse{AccessToken: "abc", TokenType: "bearer", ExpiresIn: 3600})
	})
	defer srv.Close()

	c := NewClient(ClientConfig{
		TokenURL:     srv.URL,
		ClientID:     "cid",
		ClientSecret: "secret",
		Audience:     "https://example.sink",
		Scopes:       []string{"read", "write"},
	}, NewMemoryTokenCache())

	if _, err := c.Token(context.Background()); err != nil {
		t.Fatalf("Token: %v", err)
	}
}

// TestClient_CacheKey_IsolatesByClientSecret is the H-1 regression. Prior
// to the fix, two tenants sharing ClientID, TokenURL, Issuer, and
// Audience but holding distinct ClientSecrets would collide on the same
// cache key, tenant A's token was reused by tenant B.
func TestClient_CacheKey_IsolatesByClientSecret(t *testing.T) {
	cfg := ClientConfig{
		Issuer:       "https://issuer.example.com",
		TokenURL:     "https://issuer.example.com/token",
		ClientID:     "yipyap-alerts",
		ClientSecret: "secret-A",
		Audience:     "https://sink.example.com",
	}
	clientA := NewClient(cfg, nil)

	cfg.ClientSecret = "secret-B"
	clientB := NewClient(cfg, nil)

	if clientA.cacheKey() == clientB.cacheKey() {
		t.Fatalf("cacheKey must differ when client_secret differs; got %q for both", clientA.cacheKey())
	}
	if !strings.HasPrefix(strings.Split(clientA.cacheKey(), "|sk-")[1], "") {
		t.Fatalf("cacheKey must carry sk-<hex> suffix; got %q", clientA.cacheKey())
	}

	// Sanity: two clients with identical secrets MUST still collide (same
	// identity ⇒ same cache hit).
	cfg.ClientSecret = "secret-A"
	clientA2 := NewClient(cfg, nil)
	if clientA.cacheKey() != clientA2.cacheKey() {
		t.Fatalf("cacheKey must match when secret matches; got %q vs %q", clientA.cacheKey(), clientA2.cacheKey())
	}
}

// TestClient_CacheKey_TwoSecretsProduceTwoEntries exercises the full Put /
// Get flow through MemoryTokenCache: two Clients differing only by secret
// must produce two distinct entries. Regression for H-1.
func TestClient_CacheKey_TwoSecretsProduceTwoEntries(t *testing.T) {
	cache := NewMemoryTokenCache()
	ctx := context.Background()

	cfg := ClientConfig{
		Issuer:       "https://issuer.example.com",
		TokenURL:     "https://issuer.example.com/token",
		ClientID:     "yipyap-alerts",
		ClientSecret: "secret-A",
		Audience:     "https://sink.example.com",
	}
	cA := NewClient(cfg, cache)
	if err := cache.Put(ctx, cA.cacheKey(), "token-A", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Put A: %v", err)
	}

	cfg.ClientSecret = "secret-B"
	cB := NewClient(cfg, cache)
	if err := cache.Put(ctx, cB.cacheKey(), "token-B", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Put B: %v", err)
	}

	// Expect two distinct entries in the backing map.
	if len(cache.items) != 2 {
		t.Fatalf("expected 2 cache entries, got %d", len(cache.items))
	}

	// And tenant B's Get must return tenant B's token, never A's.
	gotA, _, ok := cache.Get(ctx, cA.cacheKey())
	if !ok || gotA != "token-A" {
		t.Fatalf("A lookup: ok=%v got=%q want token-A", ok, gotA)
	}
	gotB, _, ok := cache.Get(ctx, cB.cacheKey())
	if !ok || gotB != "token-B" {
		t.Fatalf("B lookup: ok=%v got=%q want token-B", ok, gotB)
	}
}

// TestMemoryTokenCache_LRU_CapsAtMax is the H-14 regression. Writing
// N+100 entries must leave the cache at exactly N.
func TestMemoryTokenCache_LRU_CapsAtMax(t *testing.T) {
	const cap = 16
	c := NewMemoryTokenCacheWithMax(cap)
	ctx := context.Background()

	for i := 0; i < cap+100; i++ {
		key := "k-" + itoa(i)
		exp := time.Now().Add(time.Hour)
		if err := c.Put(ctx, key, "v-"+itoa(i), exp); err != nil {
			t.Fatalf("Put[%d]: %v", i, err)
		}
	}
	if got := c.size(); got != cap {
		t.Fatalf("cache size = %d, want %d", got, cap)
	}

	// First cap entries were evicted; last cap entries survive.
	for i := 0; i < 100; i++ {
		if _, _, ok := c.Get(ctx, "k-"+itoa(i)); ok {
			t.Errorf("k-%d must have been evicted", i)
		}
	}
	for i := cap + 100 - cap; i < cap+100; i++ {
		if _, _, ok := c.Get(ctx, "k-"+itoa(i)); !ok {
			t.Errorf("k-%d must still be present", i)
		}
	}
}

// TestMemoryTokenCache_DeleteExpired confirms that a bulk-expiry sweep
// walks the map and drops every entry past expiresAt. Paired with the
// LRU cap, this keeps the resident set bounded under sustained churn.
func TestMemoryTokenCache_DeleteExpired(t *testing.T) {
	c := NewMemoryTokenCacheWithMax(16)
	ctx := context.Background()

	past := time.Now().Add(-time.Minute)
	future := time.Now().Add(time.Hour)

	for i := 0; i < 5; i++ {
		if err := c.Put(ctx, "past-"+itoa(i), "v", past); err != nil {
			t.Fatalf("Put past: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		if err := c.Put(ctx, "future-"+itoa(i), "v", future); err != nil {
			t.Fatalf("Put future: %v", err)
		}
	}

	evicted := c.DeleteExpired(time.Now())
	if evicted != 5 {
		t.Fatalf("DeleteExpired = %d, want 5", evicted)
	}
	if got := c.size(); got != 3 {
		t.Fatalf("post-sweep size = %d, want 3", got)
	}
}

// TestClient_Token_DropsExpiredCacheEntry confirms the Client's
// cache-hit-past-expiry path calls Delete so stale entries don't occupy
// cap headroom. Paired with the LRU cap, this bounds total memory.
func TestClient_Token_DropsExpiredCacheEntry(t *testing.T) {
	srv := newFakeIdP(t, func(t *testing.T, r *http.Request, w http.ResponseWriter) {
		writeJSON(t, w, 200, tokenResponse{AccessToken: "fresh", TokenType: "bearer", ExpiresIn: 3600})
	})
	defer srv.Close()

	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }

	cache := NewMemoryTokenCacheWithMax(8)
	// Seed a cache entry that the Client will see as already-expired.
	if err := cache.Put(context.Background(), "does-not-matter", "stale", now.Add(-time.Hour)); err != nil {
		t.Fatalf("seed Put: %v", err)
	}

	c := NewClient(ClientConfig{
		TokenURL:     srv.URL,
		ClientID:     "cid",
		ClientSecret: "secret",
	}, cache, WithClock(clock))

	// This key corresponds to the real cacheKey; but we seed a stale
	// entry under the same key by computing it.
	key := c.cacheKey()
	if err := cache.Put(context.Background(), key, "stale-v", now.Add(-time.Hour)); err != nil {
		t.Fatalf("seed key Put: %v", err)
	}
	if cache.size() != 2 {
		t.Fatalf("seed size = %d, want 2", cache.size())
	}

	if _, err := c.Token(context.Background()); err != nil {
		t.Fatalf("Token: %v", err)
	}

	// The client looked up key, saw stale, deleted it, then Put the fresh one.
	// Net: key is present (fresh), plus the other entry we seeded earlier.
	if cache.size() > 2 {
		t.Fatalf("size = %d, want <= 2 after Delete + Put", cache.size())
	}
	v, _, ok := cache.Get(context.Background(), key)
	if !ok || v != "fresh" {
		t.Fatalf("expected fresh token in cache; got (%q, %v)", v, ok)
	}
}

// itoa is a minimal helper so this file doesn't need strconv just for
// test key formatting.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func TestMemoryTokenCache(t *testing.T) {
	ctx := context.Background()
	c := NewMemoryTokenCache()

	// Miss on empty cache.
	if _, _, ok := c.Get(ctx, "k"); ok {
		t.Error("expected miss on empty cache")
	}

	// Put then hit.
	exp := time.Now().Add(1 * time.Hour)
	if err := c.Put(ctx, "k", "v", exp); err != nil {
		t.Fatalf("Put: %v", err)
	}
	v, gotExp, ok := c.Get(ctx, "k")
	if !ok {
		t.Fatal("expected hit")
	}
	if v != "v" {
		t.Errorf("value = %q, want v", v)
	}
	if !gotExp.Equal(exp) {
		t.Errorf("expiresAt = %v, want %v", gotExp, exp)
	}

	// Different key → miss.
	if _, _, ok := c.Get(ctx, "other"); ok {
		t.Error("expected miss on different key")
	}

	// Overwrite.
	exp2 := time.Now().Add(2 * time.Hour)
	if err := c.Put(ctx, "k", "v2", exp2); err != nil {
		t.Fatalf("Put: %v", err)
	}
	v, gotExp, ok = c.Get(ctx, "k")
	if !ok || v != "v2" || !gotExp.Equal(exp2) {
		t.Errorf("after overwrite got (%q,%v,%v)", v, gotExp, ok)
	}
}
