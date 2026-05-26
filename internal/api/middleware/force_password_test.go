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
}
