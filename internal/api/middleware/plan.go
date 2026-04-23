package middleware

import (
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// RequirePaidPlan rejects requests from orgs on the free plan.
func RequirePaidPlan(s store.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetClaims(r.Context())
			if claims == nil {
				writeError(w, http.StatusUnauthorized, "authentication required")
				return
			}
			org, err := s.Orgs().GetByID(r.Context(), claims.OrgID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to verify organization")
				return
			}
			if org.Plan == domain.PlanFree {
				writeError(w, http.StatusForbidden, "this feature requires a paid plan")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireEnterprisePlan rejects requests from orgs not on the enterprise plan.
func RequireEnterprisePlan(s store.Store) func(http.Handler) http.Handler {
	return requireEnterprisePlan(s)
}
