package middleware

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

func cspDirectives(t *testing.T, policy string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, part := range strings.Split(policy, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, _ := strings.Cut(part, " ")
		if _, dup := out[name]; dup {
			t.Fatalf("duplicate CSP directive %q", name)
		}
		out[name] = strings.TrimSpace(value)
	}
	return out
}

func TestSecurityHeadersCSP(t *testing.T) {
	h := SecurityHeaders("https://yipyap.example")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	got := cspDirectives(t, rec.Header().Get("Content-Security-Policy"))
	want := map[string]string{
		"default-src":     "'self'",
		"script-src":      "'self'",
		"style-src":       "'self' 'unsafe-inline' https://fonts.googleapis.com",
		"font-src":        "'self' https://fonts.gstatic.com",
		"img-src":         "'self' data:",
		"connect-src":     "'self'",
		"object-src":      "'none'",
		"base-uri":        "'none'",
		"form-action":     "'self'",
		"frame-ancestors": "'none'",
	}
	if len(got) != len(want) {
		names := make([]string, 0, len(got))
		for k := range got {
			names = append(names, k)
		}
		sort.Strings(names)
		t.Errorf("got directives %v, want exactly %d", names, len(want))
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s = %q, want %q", name, got[name], value)
		}
	}
	for _, v := range got {
		for _, src := range strings.Fields(v) {
			if src == "wss:" || src == "ws:" || src == "*" || src == "https:" {
				t.Errorf("CSP allows any host via %q", src)
			}
		}
	}
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Error("missing HSTS for https base URL")
	}
}

func TestSecurityHeadersNoHSTSOverHTTP(t *testing.T) {
	h := SecurityHeaders("http://localhost:8080")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS must not be sent for an http base URL")
	}
	if rec.Header().Get("Content-Security-Policy") != ContentSecurityPolicy {
		t.Error("CSP must be the same regardless of scheme")
	}
}
