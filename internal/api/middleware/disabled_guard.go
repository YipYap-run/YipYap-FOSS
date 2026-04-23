package middleware

import (
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// DisabledAccountGuard checks on every request whether the user's account
// has been disabled. This adds a DB round-trip per authenticated request
// but ensures real-time enforcement with no stale-JWT window.
func DisabledAccountGuard(s store.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetClaims(r.Context())
			if claims == nil || isStaffReadonly(claims) {
				next.ServeHTTP(w, r)
				return
			}
			user, err := s.Users().GetByID(r.Context(), claims.UserID)
			if err != nil {
				// API key claims reference the key creator; if that user no
				// longer exists the key is effectively orphaned but we let the
				// request through -- the endpoint's own authz will decide.
				if claims.Role == "api_key" {
					next.ServeHTTP(w, r)
					return
				}
				writeError(w, http.StatusUnauthorized, "user not found")
				return
			}
			if user.DisabledAt != nil {
				writeError(w, http.StatusForbidden, "account is disabled")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
