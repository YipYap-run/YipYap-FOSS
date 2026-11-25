package middleware

import (
	"net/http"
	"strings"
)

// secretPathPrefixes are routes whose last path segment is a bearer token.
var secretPathPrefixes = []string{"/ack/"}

// RedactSecretPaths replaces the token segment of RequestURI for routes that
// carry a bearer token in the path, so the request logger that runs after it
// never writes the token. Routing uses r.URL, which is left alone.
func RedactSecretPaths(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, p := range secretPathPrefixes {
			if strings.HasPrefix(r.URL.Path, p) {
				r2 := r.WithContext(r.Context())
				r2.RequestURI = p + "[redacted]"
				next.ServeHTTP(w, r2)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
