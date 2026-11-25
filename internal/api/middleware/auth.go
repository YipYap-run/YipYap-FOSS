package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/session"
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

// writeErrorCode is writeError plus a machine-readable code the SPA can act on.
func writeErrorCode(w http.ResponseWriter, status int, message, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message, "code": code})
}

// APIKeyExpiredCode is the machine-readable code sent with a 401 for an API
// key whose expires_at has passed.
const APIKeyExpiredCode = "api_key_expired"

// SessionRevokedCode is the machine-readable code sent with a 401 for a
// session JWT whose server-side session was revoked, expired or is missing.
const SessionRevokedCode = "session_revoked"

// SessionCheck reports whether a session JWT's server-side session is still
// active. See internal/session.Checker.
// It returns session.ErrRevoked when the session can no longer authenticate.
type SessionCheck func(ctx context.Context, claims *auth.Claims) error

// Auth returns middleware that authenticates requests via JWT or API key.
// Token resolution order: HttpOnly cookie → Authorization header.
// hasher is used to hash API key tokens before database lookup; if nil,
// plain SHA-256 is used (backward-compatible but less secure).
// sessions checks every session JWT against its server-side session; the
// server always passes one. A nil check accepts any validly signed JWT and
// exists only for unit tests of later middleware.
func Auth(jwt *auth.JWTIssuer, keys store.APIKeyStore, hasher *auth.APIKeyHasher, sessions SessionCheck) func(http.Handler) http.Handler {
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
				if sessions != nil {
					if err := sessions(r.Context(), claims); err != nil {
						if errors.Is(err, session.ErrRevoked) {
							writeErrorCode(w, http.StatusUnauthorized, "session expired or signed out", SessionRevokedCode)
							return
						}
						// Fail closed, but do not tell the client it was signed
						// out because of a database error.
						writeError(w, http.StatusServiceUnavailable, "session check unavailable")
						return
					}
				}
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
					if key.ExpiresAt != nil && !time.Now().Before(*key.ExpiresAt) {
						writeErrorCode(w, http.StatusUnauthorized, "api key expired", APIKeyExpiredCode)
						return
					}
					// Update last used time asynchronously.
					go func() { _ = keys.UpdateLastUsed(context.Background(), key.ID, time.Now()) }()

					claims := &auth.Claims{
						OrgID:    key.OrgID,
						UserID:   key.CreatedBy,
						APIKeyID: key.ID,
						Role:     "api_key",
						Scopes:   key.Scopes,
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

// OptionalAuth authenticates the request like Auth when it carries a
// credential, but never rejects it: a missing or invalid credential, or a
// disabled or deleted user, continues as an anonymous request with no
// claims. Use it only on endpoints that serve public data to everyone and
// extra data to signed-in callers. The role is refreshed from the store, as
// DisabledAccountGuard does on the authenticated chain.
func OptionalAuth(jwt *auth.JWTIssuer, keys store.APIKeyStore, hasher *auth.APIKeyHasher, users store.UserStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, _ := authenticate(r, jwt, keys, hasher)
			// API keys belong to the org, not to their creator, so the
			// creator's account state does not apply to them.
			if claims != nil && !isStaffReadonly(claims) && claims.Role != "api_key" {
				user, err := users.GetByID(r.Context(), claims.UserID)
				switch {
				case err != nil:
					claims = nil
				case user.DisabledAt != nil:
					claims = nil
				case string(user.Role) != "" && string(user.Role) != claims.Role:
					fresh := *claims
					fresh.Role = string(user.Role)
					claims = &fresh
				}
			}
			if claims != nil {
				r = r.WithContext(context.WithValue(r.Context(), claimsKey, claims))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// authenticate resolves the request's session cookie, bearer JWT or API key
// into claims. On failure it returns nil and the error message for a 401.
func authenticate(r *http.Request, jwt *auth.JWTIssuer, keys store.APIKeyStore, hasher *auth.APIKeyHasher) (*auth.Claims, string) {
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
			return nil, "missing authorization header"
		}
		token = strings.TrimPrefix(header, "Bearer ")
		if token == header {
			return nil, "invalid authorization header"
		}
	}

	// Try JWT first.
	if claims, err := jwt.Validate(token); err == nil {
		return claims, ""
	}

	// If token looks like an API key, try that.
	if strings.HasPrefix(token, "yy_") && keys != nil {
		if hasher == nil {
			return nil, "api key authentication not configured"
		}
		hash := hasher.Hash(token)
		key, err := keys.GetByHash(r.Context(), hash)
		if err == nil {
			if APIKeyExpired(key.ExpiresAt, time.Now()) {
				return nil, "api key expired"
			}
			// Update last used time asynchronously.
			go func() { _ = keys.UpdateLastUsed(context.Background(), key.ID, time.Now()) }()

			return &auth.Claims{
				OrgID:    key.OrgID,
				UserID:   key.CreatedBy,
				APIKeyID: key.ID,
				Role:     "api_key",
				Scopes:   key.Scopes,
			}, ""
		}
	}

	return nil, "invalid or expired token"
}

// APIKeyExpired reports whether a key with the given expiry is no longer
// valid at now. A nil expiry never expires.
func APIKeyExpired(expiresAt *time.Time, now time.Time) bool {
	return expiresAt != nil && !now.Before(*expiresAt)
}
