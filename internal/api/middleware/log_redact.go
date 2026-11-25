package middleware

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// redactedQueryParams are query parameters that can carry bearer credentials.
// Clients should not put tokens in URLs, but old links and third-party
// callers still do, so the request log never prints their values.
var redactedQueryParams = []string{"token", "access_token", "id_token", "refresh_token"}

type origRequestURIKey struct{}

// RedactLogQuery wraps a request logger (for example chi's middleware.Logger)
// so it sees a RequestURI with credential query values replaced by
// "REDACTED". Handlers further down still get the original RequestURI, and
// r.URL is never changed.
func RedactLogQuery(logger func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		restore := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if orig, ok := r.Context().Value(origRequestURIKey{}).(string); ok {
				r = r.WithContext(r.Context())
				r.RequestURI = orig
			}
			next.ServeHTTP(w, r)
		})
		logged := logger(restore)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			redacted, changed := RedactRequestURI(r.RequestURI)
			if !changed {
				logged.ServeHTTP(w, r)
				return
			}
			r2 := r.WithContext(context.WithValue(r.Context(), origRequestURIKey{}, r.RequestURI))
			r2.RequestURI = redacted
			logged.ServeHTTP(w, r2)
		})
	}
}

// RedactRequestURI replaces the values of credential query parameters in a
// request URI. It reports whether anything changed.
func RedactRequestURI(uri string) (string, bool) {
	i := strings.IndexByte(uri, '?')
	if i < 0 {
		return uri, false
	}
	path, rawQuery := uri[:i], uri[i+1:]
	parts := strings.Split(rawQuery, "&")
	changed := false
	for j, p := range parts {
		key, _, hasValue := strings.Cut(p, "=")
		name, err := url.QueryUnescape(key)
		if err != nil {
			name = key
		}
		if !hasValue || !isRedactedParam(name) {
			continue
		}
		parts[j] = key + "=REDACTED"
		changed = true
	}
	if !changed {
		return uri, false
	}
	return path + "?" + strings.Join(parts, "&"), true
}

func isRedactedParam(name string) bool {
	for _, p := range redactedQueryParams {
		if strings.EqualFold(name, p) {
			return true
		}
	}
	return false
}
