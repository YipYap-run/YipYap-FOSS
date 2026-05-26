package middleware

import (
	"net/http"
)

// RequireScope returns middleware that enforces API key scope restrictions.
//
// The check only applies when the request was authenticated via an API key
// (i.e. claims.Role == "api_key" and the Scopes slice is non-empty).  Regular
// JWT users (human sessions) always pass through so that the existing role
// system remains the sole gate for them.
//
// If an API key has an empty scopes list it is treated as having no
// restrictions (legacy / unrestricted key behaviour).  Once any scope is
// declared the key is limited to only those scopes.
func RequireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetClaims(r.Context())
			if claims == nil {
				writeError(w, http.StatusUnauthorized, "authentication required")
				return
			}

			// Only enforce scopes for API key tokens that have declared scopes.
			// Human JWT sessions pass through unconditionally.
			if claims.Role == "api_key" && len(claims.Scopes) > 0 {
				if !hasScope(claims.Scopes, scope) {
					writeError(w, http.StatusForbidden, "api key missing required scope: "+scope)
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireWriteAccess blocks API keys that only carry read-type scopes from
// performing any mutating HTTP method (POST, PUT, PATCH, DELETE).
//
// It does this by checking whether ALL declared scopes end in ":read" or equal
// "read".  If so, the key is considered read-only and write methods are
// rejected.  A key with no scopes, or with at least one write scope, passes
// through.
//
// Regular JWT sessions are never affected.
func RequireWriteAccess() func(http.Handler) http.Handler {
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

			if claims.Role == "api_key" && len(claims.Scopes) > 0 && isReadOnly(claims.Scopes) {
				writeError(w, http.StatusForbidden, "api key is read-only")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// hasScope reports whether the given scope appears in the list.
func hasScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}

// isReadOnly reports whether every scope in the list is a read-type scope
// (i.e. ends in ":read" or equals "read").
func isReadOnly(scopes []string) bool {
	for _, s := range scopes {
		if s != "read" && !hasReadSuffix(s) {
			return false
		}
	}
	return true
}

func hasReadSuffix(s string) bool {
	const suffix = ":read"
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
