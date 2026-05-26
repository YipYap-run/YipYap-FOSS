package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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
