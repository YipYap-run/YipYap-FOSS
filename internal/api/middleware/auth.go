package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

type contextKey int

const claimsKey contextKey = 1

// GetClaims extracts the auth claims from the request context.
func GetClaims(ctx context.Context) *auth.Claims {
	c, _ := ctx.Value(claimsKey).(*auth.Claims)
	return c
}

// SetClaims stores claims in the context. Exported for testing.
func SetClaims(ctx context.Context, c *auth.Claims) context.Context {
	return context.WithValue(ctx, claimsKey, c)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// Auth returns middleware that authenticates requests via JWT or API key.
// Token resolution order: HttpOnly cookie → Authorization header.
// hasher is used to hash API key tokens before database lookup; if nil,
// plain SHA-256 is used (backward-compatible but less secure).
func Auth(jwt *auth.JWTIssuer, keys store.APIKeyStore, hasher *auth.APIKeyHasher) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Prefer the HttpOnly session cookie  - it is not accessible to JS
			// and is not visible in proxy logs, unlike the Authorization header
			// or WebSocket query parameters.
			var token string
			if cookie, err := r.Cookie("yipyap_session"); err == nil && cookie.Value != "" {
				token = cookie.Value
			}

			// Fall back to the Authorization header for API keys and existing
			// integrations that do not use cookies.
			if token == "" {
				header := r.Header.Get("Authorization")
				if header == "" {
					writeError(w, http.StatusUnauthorized, "missing authorization header")
					return
				}
				token = strings.TrimPrefix(header, "Bearer ")
				if token == header {
					writeError(w, http.StatusUnauthorized, "invalid authorization header")
					return
				}
			}

			// Try JWT first.
			claims, err := jwt.Validate(token)
			if err == nil {
				ctx := context.WithValue(r.Context(), claimsKey, claims)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// If token looks like an API key, try that.
			if strings.HasPrefix(token, "yy_") && keys != nil {
				if hasher == nil {
					writeError(w, http.StatusUnauthorized, "api key authentication not configured")
					return
				}
				hash := hasher.Hash(token)
				key, err := keys.GetByHash(r.Context(), hash)
				if err == nil {
					// Update last used time asynchronously.
					go func() { _ = keys.UpdateLastUsed(context.Background(), key.ID, time.Now()) }()

					claims := &auth.Claims{
						OrgID:  key.OrgID,
						UserID: key.CreatedBy,
						Role:   "api_key",
						Scopes: key.Scopes,
					}
					ctx := context.WithValue(r.Context(), claimsKey, claims)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}

			writeError(w, http.StatusUnauthorized, "invalid or expired token")
		})
	}
}

