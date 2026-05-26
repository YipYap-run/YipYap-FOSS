package checker

import (
	"net/http"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func hdr(k, v string) http.Header {
	h := http.Header{}
	h.Set(k, v)
	return h
}

// TestMatchesRule_HeaderModes covers response-header matching, including the
// equals/contains/regex modes used to assert things like a 302's Location.
func TestMatchesRule_HeaderModes(t *testing.T) {
	cases := []struct {
		name    string
		rule    domain.MonitorMatchRule
		headers http.Header
		want    bool
	}{
		{
			name:    "presence: header exists, no expected value",
			rule:    domain.MonitorMatchRule{HeaderMatch: "Location"},
			headers: hdr("Location", "https://example.com/login"),
			want:    true,
		},
		{
			name:    "presence: header missing",
			rule:    domain.MonitorMatchRule{HeaderMatch: "Location"},
			headers: http.Header{},
			want:    false,
		},
		{
			name:    "equals (legacy empty mode): exact value matches",
			rule:    domain.MonitorMatchRule{HeaderMatch: "Location", HeaderValue: "https://example.com/login"},
			headers: hdr("Location", "https://example.com/login"),
			want:    true,
		},
		{
			name:    "equals: different value does not match",
			rule:    domain.MonitorMatchRule{HeaderMatch: "Location", HeaderValue: "https://example.com/login", HeaderMatchMode: "equals"},
			headers: hdr("Location", "https://example.com/dashboard"),
			want:    false,
		},
		{
			name:    "contains: substring matches",
			rule:    domain.MonitorMatchRule{HeaderMatch: "Location", HeaderValue: "/login", HeaderMatchMode: "contains"},
			headers: hdr("Location", "https://example.com/login?next=/"),
			want:    true,
		},
		{
			name:    "contains: substring absent does not match",
			rule:    domain.MonitorMatchRule{HeaderMatch: "Location", HeaderValue: "/login", HeaderMatchMode: "contains"},
			headers: hdr("Location", "https://example.com/dashboard"),
			want:    false,
		},
		{
			name:    "regex: pattern matches",
			rule:    domain.MonitorMatchRule{HeaderMatch: "Location", HeaderValue: `^https://[^/]+/login`, HeaderMatchMode: "regex"},
			headers: hdr("Location", "https://example.com/login"),
			want:    true,
		},
		{
			name:    "regex: pattern does not match",
			rule:    domain.MonitorMatchRule{HeaderMatch: "Location", HeaderValue: `^https://secure\.`, HeaderMatchMode: "regex"},
			headers: hdr("Location", "https://example.com/login"),
			want:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchesRule(tc.rule, 302, "", tc.headers)
			if got != tc.want {
				t.Fatalf("matchesRule = %v, want %v", got, tc.want)
			}
		})
	}
}
