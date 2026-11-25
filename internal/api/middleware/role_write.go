package middleware

import (
	"net/http"
)

// writerRoles are the principals allowed to change org resources. API keys
// are included because only owners and admins can mint them, and their reach
// is narrowed by RequireScope and RequireWriteAccess.
var writerRoles = map[string]bool{
	"owner":   true,
	"admin":   true,
	"member":  true,
	"api_key": true,
}

// IsWriterRole reports whether role may change org resources such as
// monitors, teams, schedules and channels. Viewers and unknown roles may not.
func IsWriterRole(role string) bool {
	return writerRoles[role]
}

// RequireWriterRole lets reads (GET, HEAD, OPTIONS) through and limits every
// other method to owners, admins, members and API keys. Viewers are read-only;
// unknown roles fail closed.
func RequireWriterRole() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			claims := GetClaims(r.Context())
			if claims == nil {
				writeError(w, http.StatusUnauthorized, "authentication required")
				return
			}
			if !IsWriterRole(claims.Role) {
				writeError(w, http.StatusForbidden, "your role is read-only")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
