package middleware

import (
	"net/http"
	"strings"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// PasswordChangeRequiredCode marks the 403 ForcePasswordChange returns, so
// the SPA can switch to its change-password screen.
const PasswordChangeRequiredCode = "password_change_required"

// ForcePasswordChange blocks all requests except password change, refresh,
// the session read, and meta when the authenticated user's
// force_password_change flag is set.
func ForcePasswordChange(s store.Store) func(http.Handler) http.Handler {
	exempt := map[string]bool{
		"/api/v1/auth/password":          true,
		"/api/v1/auth/refresh":           true,
		"/api/v1/auth/session":           true,
		"/api/v1/meta":                   true,
		"/api/v1/auth/mfa/step-up/begin": true,
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetClaims(r.Context())
			if claims == nil {
				next.ServeHTTP(w, r)
				return
			}
			// A forced password change gates the user's sessions. API keys
			// are org credentials and do not depend on the creator's
			// password (or on the creator still existing).
			if isStaffReadonly(claims) || claims.Role == "api_key" {
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
				writeErrorCode(w, http.StatusForbidden, "password change required", PasswordChangeRequiredCode)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
