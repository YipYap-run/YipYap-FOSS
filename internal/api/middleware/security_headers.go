package middleware

import (
	"net/http"
	"strings"
)

// SecurityHeaders returns middleware that sets common security-related HTTP
// response headers. When publicBaseURL starts with "https://" a
// Strict-Transport-Security header is also added.
func SecurityHeaders(publicBaseURL string) func(http.Handler) http.Handler {
	isHTTPS := strings.HasPrefix(publicBaseURL, "https://")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
			w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			w.Header().Set("Content-Security-Policy",
				"default-src 'self'; "+
					"script-src 'self'; "+
					"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; "+
					"font-src 'self' https://fonts.gstatic.com; "+
					"img-src 'self' data:; "+
					"connect-src 'self' wss:; "+
					"frame-ancestors 'none'")
			if isHTTPS {
				w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}
