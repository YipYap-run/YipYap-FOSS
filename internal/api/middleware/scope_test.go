package middleware_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
}

func runWithClaims(t *testing.T, mw func(http.Handler) http.Handler, method string, c *auth.Claims) int {
	t.Helper()
	req := httptest.NewRequest(method, "/x", nil)
	if c != nil {
		req = req.WithContext(middleware.SetClaims(req.Context(), c))
	}
	rec := httptest.NewRecorder()
	mw(okHandler()).ServeHTTP(rec, req)
	return rec.Code
}

func TestRequireScope_ReadWriteVocabulary(t *testing.T) {
	key := func(scopes ...string) *auth.Claims {
		return &auth.Claims{OrgID: "o", UserID: "u", Role: "api_key", Scopes: scopes}
	}
	cases := []struct {
		name   string
		method string
		claims *auth.Claims
		want   int
	}{
		{"read scope allows GET", http.MethodGet, key("monitors:read"), http.StatusOK},
		{"read scope blocks POST", http.MethodPost, key("monitors:read"), http.StatusForbidden},
		{"write scope allows GET", http.MethodGet, key("monitors:write"), http.StatusOK},
		{"write scope allows DELETE", http.MethodDelete, key("monitors:write"), http.StatusOK},
		{"legacy bare scope allows POST", http.MethodPost, key("monitors"), http.StatusOK},
		{"other resource blocked", http.MethodGet, key("alerts:write"), http.StatusForbidden},
		{"empty scopes unrestricted", http.MethodPost, key(), http.StatusOK},
		{"human session passes", http.MethodPost, &auth.Claims{Role: "member"}, http.StatusOK},
		{"no claims 401", http.MethodGet, nil, http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := runWithClaims(t, middleware.RequireScope("monitors"), c.method, c.claims); got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
		})
	}
}

func TestRequireSession_RejectsAPIKeys(t *testing.T) {
	if got := runWithClaims(t, middleware.RequireSession(), http.MethodPost, &auth.Claims{Role: "api_key"}); got != http.StatusForbidden {
		t.Fatalf("api key: got %d, want 403", got)
	}
	if got := runWithClaims(t, middleware.RequireSession(), http.MethodPost, &auth.Claims{Role: "owner"}); got != http.StatusOK {
		t.Fatalf("session: got %d, want 200", got)
	}
}

func TestNormalizeScopes(t *testing.T) {
	got, unknown := middleware.NormalizeScopes([]string{" Monitors:Read ", "alerts", "monitors:read", "bogus", "monitors:admin", ""})
	if want := []string{"monitors:read", "alerts:write"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("canonical = %v, want %v", got, want)
	}
	if want := []string{"bogus", "monitors:admin"}; !reflect.DeepEqual(unknown, want) {
		t.Fatalf("unknown = %v, want %v", unknown, want)
	}
	allowed := map[string]bool{}
	for _, s := range middleware.AllowedScopes() {
		allowed[s] = true
	}
	for _, s := range []string{"monitors:read", "monitors:write", "users:read", "org:read"} {
		if !allowed[s] {
			t.Fatalf("AllowedScopes missing %q", s)
		}
	}
}

func TestAuth_RejectsExpiredAPIKey(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.Orgs().Create(ctx, &domain.Org{ID: "o1", Name: "T", Slug: "t", Plan: domain.PlanFree, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	hasher := auth.NewAPIKeyHasher([]byte("test-secret"))
	jwt := auth.NewJWTIssuer([]byte("jwt-secret-for-tests"), time.Hour)

	mk := func(id string, exp *time.Time) string {
		plain, hash, prefix, err := hasher.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if err := s.APIKeys().Create(ctx, &domain.APIKey{
			ID: id, OrgID: "o1", Name: id, KeyHash: hash, Prefix: prefix,
			Scopes: []string{}, CreatedBy: "u1", ExpiresAt: exp, CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		return plain
	}
	past := now.Add(-time.Minute)
	future := now.Add(time.Hour)
	expired := mk("k-expired", &past)
	live := mk("k-live", &future)
	forever := mk("k-forever", nil)

	mw := middleware.Auth(jwt, s.APIKeys(), hasher, nil)
	call := func(token string) (int, map[string]string) {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		mw(okHandler()).ServeHTTP(rec, req)
		var body map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}

	code, body := call(expired)
	if code != http.StatusUnauthorized || body["error"] != "api key expired" || body["code"] != middleware.APIKeyExpiredCode {
		t.Fatalf("expired key: got %d %v", code, body)
	}
	if code, _ := call(live); code != http.StatusOK {
		t.Fatalf("future expiry: got %d, want 200", code)
	}
	if code, _ := call(forever); code != http.StatusOK {
		t.Fatalf("no expiry: got %d, want 200", code)
	}
}
