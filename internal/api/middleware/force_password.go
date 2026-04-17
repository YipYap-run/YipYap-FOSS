package middleware

import (
	"net/http"
	"strings"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// ForcePasswordChange blocks all requests except password change, refresh, and meta
// when the authenticated user's force_password_change flag is set.
func ForcePasswordChange(s store.Store) func(http.Handler) http.Handler {
	exempt := map[string]bool{
		"/api/v1/auth/password": true,
		"/api/v1/auth/refresh":  true,
		"/api/v1/meta":          true,
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetClaims(r.Context())
			if claims == nil {
				next.ServeHTTP(w, r)
				return
			}
			// Staff-readonly tokens have no real user record.
			if claims.Role == "staff-readonly" {
				next.ServeHTTP(w, r)
				return
			}
			path := strings.TrimSuffix(r.URL.Path, "/")
			if exempt[path] {
				next.ServeHTTP(w, r)
				return
			}
			user, err := s.Users().GetByID(r.Context(), claims.UserID)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "user not found")
				return
			}
			if user.ForcePasswordChange {
				writeError(w, http.StatusForbidden, "password change required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
