package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// FOSS builds have no billing concept, so RequirePaidPlan must let any
// authenticated request through (including PlanFree). Self-hosted operators
// would otherwise be locked out of paid-only features they administer themselves.
func TestRequirePaidPlan_FOSSPassThrough(t *testing.T) {
	s := setupTestStore(t)
	jwt := auth.NewJWTIssuer([]byte("test-secret"), 1*time.Hour)

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	planMW := middleware.RequirePaidPlan(s)

	t.Run("free plan passes under FOSS", func(t *testing.T) {
		org := createOrg(t, s, "org-foss-free", domain.PlanFree)
		rr := makeAuthenticatedRequest(t, jwt, org.ID, planMW(okHandler))
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 under FOSS, got %d", rr.Code)
		}
	})

	t.Run("middleware no-ops without claims under FOSS", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rr := httptest.NewRecorder()
		planMW(okHandler).ServeHTTP(rr, req)
		// FOSS variant doesn't auth-check; it leaves that to the auth
		// middleware upstream.
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 (FOSS no-op), got %d", rr.Code)
		}
	})
}
