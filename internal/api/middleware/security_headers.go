package middleware

import (
	"net/http"
	"strings"
)

// ContentSecurityPolicy is the policy sent on every response.
//
// connect-src is 'self' only: CSP3 browsers treat 'self' as also matching
// ws:/wss: on the same host, which is all the SPA WebSocket (/ws) needs. A bare
// wss: would allow WebSocket connections to any host.
//
// base-uri and form-action do not fall back to default-src, so they are set
// explicitly. form-action 'self' is enough because every SPA form is handled in
// JavaScript, and the OIDC login and Stripe checkout/portal flows leave the
// site through link or location navigation, not form posts.
const ContentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
	"font-src 'self' https://fonts.gstatic.com; " +
	"img-src 'self' data:; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

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
			w.Header().Set("Content-Security-Policy", ContentSecurityPolicy)
			if isHTTPS {
				w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}
