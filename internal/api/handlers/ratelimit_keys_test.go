package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
)

// TestRateLimitKeyByOrg keys on org id (H-16 per-org rate limit).
func TestRateLimitKeyByOrg(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/alerts", nil)
	ctx := middleware.SetClaims(req.Context(), &auth.Claims{OrgID: "org-abc"})
	req = req.WithContext(ctx)
	key, err := RateLimitKeyByOrg(req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if key != "org:org-abc" {
		t.Fatalf("key = %q, want org:org-abc", key)
	}

	// No claims: anonymous fallback (auth middleware would 401 before
	// the handler; the key just needs to not panic).
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/alerts", nil)
	key2, err := RateLimitKeyByOrg(req2)
	if err != nil {
		t.Fatalf("anon err: %v", err)
	}
	if key2 != "anonymous" {
		t.Fatalf("anonymous key = %q, want anonymous", key2)
	}
}
