package middleware

import (
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// AccountDisabledCode marks the 403 DisabledAccountGuard returns, so the SPA
// can sign the user out instead of showing a broken page.
const AccountDisabledCode = "account_disabled"

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
				// An API key whose creator row is gone is orphaned; the
				// endpoint's own authorization decides whether it works.
				if claims.Role == "api_key" {
					next.ServeHTTP(w, r)
					return
				}
				writeError(w, http.StatusUnauthorized, "user not found")
				return
			}
			if user.DisabledAt != nil {
				// A key whose creator was removed (not merely disabled) is
				// org-owned and keeps working until it is revoked.
				if claims.Role == "api_key" && user.DeletedAt != nil {
					next.ServeHTTP(w, r)
					return
				}
				writeErrorCode(w, http.StatusForbidden, "account is disabled", AccountDisabledCode)
				return
			}
			// The role in a session token is a snapshot from login. Use the
			// stored role so a demotion takes effect on the next request
			// instead of when the token expires. An API key keeps its api_key
			// role: its reach comes from its scopes, not its creator's role.
			if claims.Role != "api_key" && string(user.Role) != "" && string(user.Role) != claims.Role {
				fresh := *claims
				fresh.Role = string(user.Role)
				r = r.WithContext(SetClaims(r.Context(), &fresh))
			}
			next.ServeHTTP(w, r)
		})
	}
}
