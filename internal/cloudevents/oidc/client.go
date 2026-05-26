// Package oidc provides an outbound OIDC token client used by the
// cloudevent_http provider to obtain bearer tokens for authenticated
// CloudEvents delivery (Knative-style).
//
// The package intentionally avoids golang.org/x/oauth2: we need a small,
// pluggable cache (future-proofed for a JetStream KV adapter) and the
// client_credentials flow is trivial.
package oidc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// refreshAheadWindow is how close to expiry a cached token must be before
// we treat it as expired and refresh.
const refreshAheadWindow = 30 * time.Second

// defaultHTTPTimeout applies to the token-endpoint HTTP request when the
// caller does not supply a custom *http.Client via WithHTTPClient.
const defaultHTTPTimeout = 30 * time.Second

// TokenCache is a pluggable cache for OIDC bearer tokens keyed by
// (issuer, audience, clientID). A nil TokenCache means no caching, every
// Token() call triggers a fetch.
//
// Implementations must be safe for concurrent use.
type TokenCache interface {
	Get(ctx context.Context, key string) (value string, expiresAt time.Time, ok bool)
	Put(ctx context.Context, key, value string, expiresAt time.Time) error
}

// DefaultMemoryTokenCacheMax is the default LRU cap for MemoryTokenCache.
// Beyond this many live entries the cache evicts the least-recently-used
// entry on each new insert. Chosen to comfortably cover the working set
// for thousands of (issuer, client, audience) tuples while keeping
// total memory bounded. See H-14.
const DefaultMemoryTokenCacheMax = 1024

// MemoryTokenCache is a process-local TokenCache with a bounded LRU.
// Safe for concurrent use.
//
// Prior to the bound, a channel-write-capable attacker could churn OIDC
// audience strings and pin unbounded entries (each carrying a 2-4 KiB
// JWT). The cache now caps live entries at maxEntries with LRU eviction
// on insert. Entries can be explicitly dropped by calling Delete, the
// Client does this on refresh so expired tokens do not linger.
//
// Get does NOT auto-expire based on wall clock: the Client already has
// its own refresh-ahead check and an injectable clock for tests, and
// auto-expiry here would collide with the Client's fake clock.
type MemoryTokenCache struct {
	mu         sync.Mutex
	items      map[string]*memItem
	order      []string // FIFO-of-recency; head is LRU, tail is MRU
	maxEntries int
}

type memItem struct {
	value     string
	expiresAt time.Time
}

// NewMemoryTokenCache returns an empty in-memory token cache with the
// default LRU cap (DefaultMemoryTokenCacheMax).
func NewMemoryTokenCache() *MemoryTokenCache {
	return NewMemoryTokenCacheWithMax(DefaultMemoryTokenCacheMax)
}

// NewMemoryTokenCacheWithMax returns a cache bound to maxEntries. A
// non-positive value falls back to DefaultMemoryTokenCacheMax.
func NewMemoryTokenCacheWithMax(maxEntries int) *MemoryTokenCache {
	if maxEntries <= 0 {
		maxEntries = DefaultMemoryTokenCacheMax
	}
	return &MemoryTokenCache{
		items:      make(map[string]*memItem),
		order:      make([]string, 0, maxEntries),
		maxEntries: maxEntries,
	}
}

// Get returns a cached token and its stored expiry. ok is false on miss.
// The Client's refresh-ahead window is checked by the caller against the
// returned expiry.
func (c *MemoryTokenCache) Get(_ context.Context, key string) (string, time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	it, ok := c.items[key]
	if !ok {
		return "", time.Time{}, false
	}
	// Touch as MRU.
	c.touchLocked(key)
	return it.value, it.expiresAt, true
}

// Delete removes an entry so an expired token no longer occupies cap
// headroom. Called by the Client when it observes a stale cache hit and
// is about to re-fetch.
func (c *MemoryTokenCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleteLocked(key)
}

// DeleteExpired walks the map and drops every entry past expiresAt per
// the supplied now function. Returns the number of entries evicted.
// Intended for a periodic janitor or for batch cleanup at shutdown.
func (c *MemoryTokenCache) DeleteExpired(now time.Time) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	evicted := 0
	for k, it := range c.items {
		if !it.expiresAt.IsZero() && now.After(it.expiresAt) {
			c.deleteLocked(k)
			evicted++
		}
	}
	return evicted
}

// Put stores value under key with the given expiry. Overwrites any prior
// entry. If the live set would exceed maxEntries, the least-recently-used
// entry is evicted first.
func (c *MemoryTokenCache) Put(_ context.Context, key, value string, expiresAt time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[key]; ok {
		c.items[key] = &memItem{value: value, expiresAt: expiresAt}
		c.touchLocked(key)
		return nil
	}
	if len(c.items) >= c.maxEntries {
		// Evict head of the recency list (LRU).
		if len(c.order) > 0 {
			lru := c.order[0]
			c.deleteLocked(lru)
		}
	}
	c.items[key] = &memItem{value: value, expiresAt: expiresAt}
	c.order = append(c.order, key)
	return nil
}

// deleteLocked removes key from items and order. Caller must hold c.mu.
func (c *MemoryTokenCache) deleteLocked(key string) {
	delete(c.items, key)
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}

// touchLocked moves key to the MRU end of the recency list. Caller must
// hold c.mu. No-op when the key is not present (shouldn't happen, keeps
// the invariant defensive).
func (c *MemoryTokenCache) touchLocked(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(append(c.order[:i], c.order[i+1:]...), key)
			return
		}
	}
}

// size returns the current entry count. Test helper.
func (c *MemoryTokenCache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// ClientConfig is the user-facing token-fetching configuration.
type ClientConfig struct {
	// Issuer is the IdP issuer URL, informational, used in the cache key
	// and in error messages. The actual HTTP request goes to TokenURL.
	Issuer string
	// TokenURL is the OAuth2 token endpoint (e.g. https://issuer/oauth2/token).
	TokenURL string
	// ClientID / ClientSecret are the client_credentials grant credentials.
	ClientID     string
	ClientSecret string
	// Audience, if non-empty, is sent as the `audience` form parameter,
	// required by Auth0 and other audience-aware IdPs (e.g. Knative sinks).
	Audience string
	// Scopes, if non-empty, are joined with spaces and sent as `scope`.
	Scopes []string
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient overrides the default 30s-timeout http.Client used for
// the token-endpoint request.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.httpClient = h
		}
	}
}

// WithClock overrides the clock used for expiry math. Test-only.
func WithClock(now func() time.Time) Option {
	return func(c *Client) {
		if now != nil {
			c.now = now
		}
	}
}

// Client fetches and caches OIDC access tokens for outbound delivery.
type Client struct {
	cfg        ClientConfig
	cache      TokenCache
	httpClient *http.Client
	now        func() time.Time
	// sf deduplicates concurrent Token() calls that all miss the cache
	// for the same key. Without it, N goroutines firing simultaneously
	// on a cold cache hit produce N token-endpoint round-trips. A4-N-5.
	sf singleflightGroup
}

// NewClient returns a Client configured against cfg, storing tokens in
// cache. A nil cache disables caching.
func NewClient(cfg ClientConfig, cache TokenCache, opts ...Option) *Client {
	c := &Client{
		cfg:        cfg,
		cache:      cache,
		httpClient: &http.Client{Timeout: defaultHTTPTimeout},
		now:        time.Now,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// cacheKey uniquely identifies a (issuer, token URL, client, audience)
// tuple. Including both Issuer and TokenURL is cheap insurance against
// misconfiguration where two tenants share an issuer but diverge on the
// actual token endpoint.
//
// A prefix-truncated SHA-256 of ClientSecret is also folded in so that two
// tenants sharing a canonical ClientID (e.g. "yipyap-alerts") with
// distinct secrets do NOT collide on the same cache entry. Before this
// fix, tenant A's token could be reused by tenant B. The secret itself
// never leaves the process, only the 16-hex prefix of its digest is
// stored. See security review 01 (H-1).
func (c *Client) cacheKey() string {
	sum := sha256.Sum256([]byte(c.cfg.ClientSecret))
	return fmt.Sprintf("%s|%s|%s|%s|sk-%s",
		c.cfg.Issuer,
		c.cfg.TokenURL,
		c.cfg.ClientID,
		c.cfg.Audience,
		hex.EncodeToString(sum[:])[:16],
	)
}

// Token returns a valid access token, fetching from the IdP if the cache
// is empty or the stored token is within refreshAheadWindow of expiry.
// Failures are never cached.
func (c *Client) Token(ctx context.Context) (string, error) {
	key := c.cacheKey()
	now := c.now()

	if c.cache != nil {
		if v, exp, ok := c.cache.Get(ctx, key); ok {
			if now.Add(refreshAheadWindow).Before(exp) {
				return v, nil
			}
			// Past-expiry cache hit: proactively drop the entry so it
			// doesn't occupy cap headroom. Uses the optional Deleter
			// interface so non-memory caches (e.g. a future JetStream KV)
			// can opt in without breaking the TokenCache contract.
			if d, ok := c.cache.(interface{ Delete(key string) }); ok {
				d.Delete(key)
			}
		}
	}

	// A4-N-5: when many goroutines miss the cache for the same key at
	// once, fan them through a single fetch. The first goroutine drives
	// the HTTP round-trip; the others piggy-back on its result.
	type tokenResult struct {
		tok       string
		expiresAt time.Time
	}
	v, err, _ := c.sf.do(key, func() (any, error) {
		// Re-check the cache inside the singleflight critical section in
		// case another goroutine that ran fetch() finished between our
		// initial Get and entering do().
		if c.cache != nil {
			if cached, exp, ok := c.cache.Get(ctx, key); ok {
				if c.now().Add(refreshAheadWindow).Before(exp) {
					return tokenResult{cached, exp}, nil
				}
			}
		}
		tok, expiresAt, fetchErr := c.fetch(ctx, c.now())
		if fetchErr != nil {
			return tokenResult{}, fetchErr
		}
		if c.cache != nil {
			// Caching errors are non-fatal, the caller still got a valid token.
			_ = c.cache.Put(ctx, key, tok, expiresAt)
		}
		return tokenResult{tok, expiresAt}, nil
	})
	if err != nil {
		return "", fmt.Errorf("oidc: fetch token from %s: %w", c.issuerForErr(), err)
	}
	return v.(tokenResult).tok, nil
}

// issuerForErr returns a human-readable identifier for error messages,
// preferring Issuer but falling back to TokenURL.
func (c *Client) issuerForErr() string {
	if c.cfg.Issuer != "" {
		return c.cfg.Issuer
	}
	return c.cfg.TokenURL
}

// tokenEndpointResponse is the JSON shape we parse from the token endpoint.
type tokenEndpointResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// fetch performs the client_credentials POST and returns the token plus
// its absolute expiry.
func (c *Client) fetch(ctx context.Context, now time.Time) (string, time.Time, error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", c.cfg.ClientID)
	form.Set("client_secret", c.cfg.ClientSecret)
	if c.cfg.Audience != "" {
		form.Set("audience", c.cfg.Audience)
	}
	if len(c.cfg.Scopes) > 0 {
		form.Set("scope", strings.Join(c.cfg.Scopes, " "))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Keep the response snippet short to avoid logging large HTML error pages.
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 256 {
			snippet = snippet[:256] + "..."
		}
		return "", time.Time{}, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, snippet)
	}

	var tr tokenEndpointResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", time.Time{}, fmt.Errorf("decode response: %w", err)
	}

	if tr.AccessToken == "" {
		return "", time.Time{}, errors.New("token endpoint returned empty access_token")
	}
	// token_type is case-insensitive per RFC 6749 §5.1 but we only support bearer.
	if !strings.EqualFold(tr.TokenType, "bearer") {
		return "", time.Time{}, fmt.Errorf("unsupported token_type %q (want bearer)", tr.TokenType)
	}
	if tr.ExpiresIn <= 0 {
		return "", time.Time{}, fmt.Errorf("invalid expires_in %d", tr.ExpiresIn)
	}

	expiresAt := now.Add(time.Duration(tr.ExpiresIn) * time.Second)
	return tr.AccessToken, expiresAt, nil
}
