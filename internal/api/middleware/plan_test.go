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
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

func setupTestStore(t *testing.T) store.Store {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func createOrg(t *testing.T, s store.Store, id string, plan domain.OrgPlan) *domain.Org {
	t.Helper()
	org := &domain.Org{
		ID:   id,
		Name: "Test Org " + id,
		Slug: "test-" + id,
		Plan: plan,
	}
	if err := s.Orgs().Create(context.Background(), org); err != nil {
		t.Fatal(err)
	}
	return org
}

func makeAuthenticatedRequest(t *testing.T, jwt *auth.JWTIssuer, orgID string, handler http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	token, err := jwt.Issue("user-1", orgID, "owner")
	if err != nil {
		t.Fatal(err)
	}

	authed := middleware.Auth(jwt, nil, nil)(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	authed.ServeHTTP(rr, req)
	return rr
}

func TestRequirePaidPlan(t *testing.T) {
	s := setupTestStore(t)
	jwt := auth.NewJWTIssuer([]byte("test-secret"), 1*time.Hour)

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	planMW := middleware.RequirePaidPlan(s)

	t.Run("free plan is rejected", func(t *testing.T) {
		org := createOrg(t, s, "org-free-paid", domain.PlanFree)
		rr := makeAuthenticatedRequest(t, jwt, org.ID, planMW(okHandler))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", rr.Code)
		}
		var resp map[string]string
		_ = json.NewDecoder(rr.Body).Decode(&resp)
		if resp["error"] != "this feature requires a paid plan" {
			t.Fatalf("unexpected error: %s", resp["error"])
		}
	})

	t.Run("unauthenticated is rejected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rr := httptest.NewRecorder()
		planMW(okHandler).ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rr.Code)
		}
	})
}

