package handlers

import (
	"net/http"
	"strings"

	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
)

// SessionCookieName is the HttpOnly cookie that carries the browser session
// JWT. The auth middleware and the /ws upgrade read it.
const SessionCookieName = "yipyap_session"

// sessionCookieSecure reports whether the session cookie must carry the
// Secure flag: when the public base URL is https, or the request itself
// arrived over TLS.
func sessionCookieSecure(r *http.Request, publicBaseURL string) bool {
	if strings.HasPrefix(publicBaseURL, "https://") {
		return true
	}
	return r != nil && r.TLS != nil
}

// writeSessionCookie sets the session JWT as an HttpOnly, SameSite=Strict
// cookie. Every handler that completes a login or a refresh must call this so
// the browser session survives a reload and /ws can authenticate without a
// token in the URL.
func writeSessionCookie(w http.ResponseWriter, r *http.Request, jwt *auth.JWTIssuer, publicBaseURL, token string) {
	maxAge := 0
	if jwt != nil {
		maxAge = int(jwt.TTL().Seconds())
	}
	if maxAge <= 0 {
		maxAge = 86400
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   sessionCookieSecure(r, publicBaseURL),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
	})
}

// clearSessionCookie expires the session cookie.
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}
