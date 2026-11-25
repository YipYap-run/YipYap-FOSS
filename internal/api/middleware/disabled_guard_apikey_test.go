package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// A key made by a disabled user stops working as soon as the account is
// disabled. A key whose creator row is gone is orphaned and left to the
// endpoint's own authorization.
func TestDisabledAccountGuard_APIKeyIgnoresCreatorState(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	org := &domain.Org{ID: "o-dg", Name: "DG", Slug: "dg", CreatedAt: now, UpdatedAt: now}
	if err := s.Orgs().Create(ctx, org); err != nil {
		t.Fatal(err)
	}
	u := &domain.User{ID: "u-dg", OrgID: org.ID, Email: "dg@example.com", PasswordHash: "x", Role: domain.RoleAdmin, CreatedAt: now, UpdatedAt: now}
	if err := s.Users().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := s.Users().Disable(ctx, u.ID, now); err != nil {
		t.Fatal(err)
	}

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := middleware.DisabledAccountGuard(s)(ok)
	do := func(userID, role string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil)
		req = req.WithContext(middleware.SetClaims(req.Context(), &auth.Claims{UserID: userID, OrgID: org.ID, Role: role}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := do(u.ID, "api_key"); got != http.StatusForbidden {
		t.Errorf("key from disabled creator: got %d, want 403", got)
	}
	if got := do("gone", "api_key"); got != http.StatusOK {
		t.Errorf("key from removed creator: got %d, want 200", got)
	}
	if got := do(u.ID, "admin"); got != http.StatusForbidden {
		t.Errorf("disabled session: got %d, want 403", got)
	}
	if got := do("gone", "admin"); got != http.StatusUnauthorized {
		t.Errorf("removed session: got %d, want 401", got)
	}
}
