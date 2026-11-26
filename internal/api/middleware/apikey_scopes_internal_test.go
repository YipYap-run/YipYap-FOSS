package middleware

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeAPIKeyScopes(t *testing.T) {
	cases := []struct {
		in      []string
		want    []string
		wantErr string
	}{
		{in: nil, wantErr: "at least one"},
		{in: []string{}, wantErr: "at least one"},
		{in: []string{"monitors"}, wantErr: "unknown scope"},
		{in: []string{"monitors:delete"}, wantErr: "unknown scope"},
		{in: []string{"widgets:read"}, wantErr: "unknown scope"},
		{in: []string{"users:write"}, wantErr: "unknown scope"},
		{in: []string{"incidents:write"}, wantErr: "unknown scope"},
		{in: []string{"read"}, wantErr: "unknown scope"},
		{in: []string{"alerts:read", " monitors:write ", "monitors:read"}, want: []string{"monitors:write", "alerts:read"}},
		{in: []string{"monitors:write", "full-access"}, want: []string{"full-access"}},
		{in: []string{"alerts:read", "read-only", "monitors:write"}, want: []string{"read-only", "monitors:write"}},
	}
	for _, c := range cases {
		got, err := NormalizeAPIKeyScopes(c.in)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%v: err = %v, want %q", c.in, err, c.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%v: unexpected err %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v: got %v, want %v", c.in, got, c.want)
		}
	}
}

// Every scope the vocabulary advertises must be accepted on create.
func TestVocabularyScopesAreCreatable(t *testing.T) {
	for _, r := range APIKeyScopeResources() {
		for _, s := range r.Scopes() {
			if _, err := NormalizeAPIKeyScopes([]string{s}); err != nil {
				t.Errorf("%s: %v", s, err)
			}
		}
	}
	for _, p := range APIKeyScopePresets() {
		if _, err := NormalizeAPIKeyScopes([]string{p.Key}); err != nil {
			t.Errorf("%s: %v", p.Key, err)
		}
	}
}

// Every route rule must name a resource in the vocabulary.
func TestScopeRoutesUseKnownResources(t *testing.T) {
	for _, rt := range apiKeyScopeRoutes {
		if _, ok := lookupScopeResource(rt.resource); !ok {
			t.Errorf("%s: unknown resource %q", rt.pattern, rt.resource)
		}
		for _, alt := range rt.alsoAccept {
			if _, ok := lookupScopeResource(alt); !ok {
				t.Errorf("%s: unknown alternate resource %q", rt.pattern, alt)
			}
		}
	}
}

func TestAPIKeyRouteAllowed(t *testing.T) {
	cases := []struct {
		name   string
		scopes []string
		method string
		path   string
		want   bool
	}{
		{"read scope reads", []string{"monitors:read"}, http.MethodGet, "/api/v1/monitors", true},
		{"read scope cannot write", []string{"monitors:read"}, http.MethodPost, "/api/v1/monitors", false},
		{"write implies read", []string{"monitors:write"}, http.MethodGet, "/api/v1/monitors/abc/checks", true},
		{"write scope writes", []string{"monitors:write"}, http.MethodDelete, "/api/v1/monitors/abc", true},
		{"other resource denied", []string{"monitors:write"}, http.MethodGet, "/api/v1/alerts", false},
		{"monitor groups map to monitors", []string{"monitors:read"}, http.MethodGet, "/api/v1/monitor-groups", true},
		{"read-only preset reads anything mapped", []string{"read-only"}, http.MethodGet, "/api/v1/teams", true},
		{"read-only preset cannot write", []string{"read-only"}, http.MethodPost, "/api/v1/incidents", false},
		{"read-only plus one write", []string{"read-only", "teams:write"}, http.MethodPost, "/api/v1/teams", true},
		{"incidents are read-only for keys", []string{"full-access"}, http.MethodPost, "/api/v1/incidents", false},
		{"full access writes", []string{"full-access"}, http.MethodPatch, "/api/v1/services/x", true},
		{"status pages are read-only for keys", []string{"full-access"}, http.MethodPatch, "/api/v1/status-pages/x", false},
		{"create incident from alert needs incidents", []string{"alerts:write"}, http.MethodPost, "/api/v1/alerts/a1/create-incident", false},
		{"create incident from alert never via key", []string{"full-access"}, http.MethodPost, "/api/v1/alerts/a1/create-incident", false},
		{"ack needs alerts write", []string{"alerts:read"}, http.MethodPost, "/api/v1/alerts/a1/ack", false},
		{"org read", []string{"org:read"}, http.MethodGet, "/api/v1/org", true},
		{"org api keys not mapped", []string{"full-access"}, http.MethodGet, "/api/v1/org/api-keys", false},
		{"users write never", []string{"full-access"}, http.MethodPatch, "/api/v1/users/u1", false},
		{"account routes closed", []string{"full-access"}, http.MethodPut, "/api/v1/auth/password", false},
		{"billing closed", []string{"full-access"}, http.MethodGet, "/api/v1/billing", false},
		{"legacy empty is full access", nil, http.MethodPost, "/api/v1/teams", true},
		{"legacy empty still closed on account routes", nil, http.MethodPut, "/api/v1/auth/email", false},
		{"legacy bare resource", []string{"alerts"}, http.MethodPost, "/api/v1/alerts/a1/resolve", true},
		{"legacy read", []string{"read"}, http.MethodGet, "/api/v1/schedules", true},
		{"prefix is segment aligned", []string{"monitors:read"}, http.MethodGet, "/api/v1/monitorsx", false},
	}
	for _, c := range cases {
		got, _ := apiKeyRouteAllowed(c.scopes, c.method, c.path)
		if got != c.want {
			t.Errorf("%s: %s %s with %v = %v, want %v", c.name, c.method, c.path, c.scopes, got, c.want)
		}
	}
}
