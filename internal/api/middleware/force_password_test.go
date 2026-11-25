package middleware_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func TestForcePasswordChange(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	now := time.Now()

	org := &domain.Org{
		ID:        "o1",
		Name:      "Test",
		Slug:      "test",
		Plan:      domain.PlanFree,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.Orgs().Create(ctx, org); err != nil {
		t.Fatal(err)
	}

	normalUser := &domain.User{
		ID:                  "u1",
		OrgID:               "o1",
		Email:               "normal@example.com",
		PasswordHash:        "x",
		Role:                domain.RoleMember,
		ForcePasswordChange: false,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := s.Users().Create(ctx, normalUser); err != nil {
		t.Fatal(err)
	}

	forcedUser := &domain.User{
		ID:                  "u2",
		OrgID:               "o1",
		Email:               "forced@example.com",
		PasswordHash:        "x",
		Role:                domain.RoleMember,
		ForcePasswordChange: true,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := s.Users().Create(ctx, forcedUser); err != nil {
		t.Fatal(err)
	}

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := middleware.ForcePasswordChange(s)

	makeReq := func(userID, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		claims := &auth.Claims{UserID: userID, OrgID: "o1", Role: "member"}
		req = req.WithContext(middleware.SetClaims(req.Context(), claims))
		rr := httptest.NewRecorder()
		mw(okHandler).ServeHTTP(rr, req)
		return rr
	}

	t.Run("allows request when force_password_change is false", func(t *testing.T) {
		rr := makeReq("u1", "/api/v1/monitors")
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("blocks request when force_password_change is true", func(t *testing.T) {
		rr := makeReq("u2", "/api/v1/monitors")
		if rr.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", rr.Code)
		}
	})

	t.Run("allows password change endpoint when forced", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/auth/password", nil)
		claims := &auth.Claims{UserID: "u2", OrgID: "o1", Role: "member"}
		req = req.WithContext(middleware.SetClaims(req.Context(), claims))
		rr := httptest.NewRecorder()
		mw(okHandler).ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("blocked response carries the password_change_required code", func(t *testing.T) {
		rr := makeReq("u2", "/api/v1/teams")
		if rr.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", rr.Code)
		}
		var body map[string]string
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["code"] != middleware.PasswordChangeRequiredCode {
			t.Fatalf("code = %q, want %q", body["code"], middleware.PasswordChangeRequiredCode)
		}
	})

	t.Run("allows the session read when forced", func(t *testing.T) {
		rr := makeReq("u2", "/api/v1/auth/session")
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("allows a WebAuthn step-up challenge when forced", func(t *testing.T) {
		rr := makeReq("u2", "/api/v1/auth/mfa/step-up/begin")
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
	})
	t.Run("API keys are not gated by the creator's password state", func(t *testing.T) {
		for _, creator := range []string{"u2", "deleted-user"} {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil)
			claims := &auth.Claims{UserID: creator, OrgID: "o1", Role: "api_key"}
			req = req.WithContext(middleware.SetClaims(req.Context(), claims))
			rr := httptest.NewRecorder()
			mw(okHandler).ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("api key created by %s: expected 200, got %d", creator, rr.Code)
			}
		}
	})
}

func TestDisabledAccountGuardCode(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	now := time.Now()
	if err := s.Orgs().Create(ctx, &domain.Org{ID: "o1", Name: "Test", Slug: "test", Plan: domain.PlanFree, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	disabled := now.Add(-time.Hour)
	u := &domain.User{ID: "u1", OrgID: "o1", Email: "gone@example.com", PasswordHash: "x", Role: domain.RoleMember, CreatedAt: now, UpdatedAt: now}
	if err := s.Users().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := s.Users().Disable(ctx, u.ID, disabled); err != nil {
		t.Fatal(err)
	}

	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/api/v1/org/api-keys", nil)
	req = req.WithContext(middleware.SetClaims(req.Context(), &auth.Claims{UserID: "u1", OrgID: "o1", Role: "member"}))
	rr := httptest.NewRecorder()
	middleware.DisabledAccountGuard(s)(ok).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["code"] != middleware.AccountDisabledCode {
		t.Fatalf("code = %q, want %q", body["code"], middleware.AccountDisabledCode)
	}
}

// An API key authenticates as its creator, so a key made by a user whose
// account is pending deletion stops working as soon as the account is
// disabled, not only once the cleanup job deletes it.
func TestDisabledAccountGuardBlocksCreatorsAPIKeys(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	now := time.Now()
	if err := s.Orgs().Create(ctx, &domain.Org{ID: "o1", Name: "Test", Slug: "test", Plan: domain.PlanFree, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	u := &domain.User{ID: "u1", OrgID: "o1", Email: "leaving@example.com", PasswordHash: "x", Role: domain.RoleMember, CreatedAt: now, UpdatedAt: now}
	if err := s.Users().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := s.Users().Disable(ctx, u.ID, now); err != nil {
		t.Fatal(err)
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil)
	req = req.WithContext(middleware.SetClaims(req.Context(), &auth.Claims{UserID: "u1", OrgID: "o1", Role: "api_key"}))
	rr := httptest.NewRecorder()
	middleware.DisabledAccountGuard(s)(ok).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("api key of a disabled creator: got %d, want 403", rr.Code)
	}
}
