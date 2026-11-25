package handlers_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// requireKeys fails when obj lacks any key the SPA reads.
func requireKeys(t *testing.T, what string, obj map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := obj[k]; !ok {
			t.Errorf("%s missing %q: %v", what, k, obj)
		}
	}
}

// The contact methods page, the step editor and the on-call page read
// these shapes (see the contract comments in
// web/src/pages/settings/contact-methods.jsx and escalation.jsx).
func TestContactMethodsUIContract(t *testing.T) {
	f := newCMFixture(t)

	code, _, methods := f.member.do(http.MethodGet, "/users/me/contact-methods", nil)
	if code != http.StatusOK || len(methods) == 0 {
		t.Fatalf("list methods: %d %v", code, methods)
	}
	for _, m := range methods {
		requireKeys(t, "method", m.(map[string]any),
			"id", "type", "label", "config", "summary", "verified", "verification_pending", "paid_only", "created_at", "updated_at")
	}

	code, _, rules := f.member.do(http.MethodGet, "/users/me/notification-rules", nil)
	if code != http.StatusOK || len(rules) == 0 {
		t.Fatalf("list rules: %d %v", code, rules)
	}
	for _, r := range rules {
		requireKeys(t, "rule", r.(map[string]any), "id", "contact_method_id", "delay_minutes", "urgency")
	}

	code, opts, _ := f.member.do(http.MethodGet, "/users/me/contact-method-options", nil)
	if code != http.StatusOK {
		t.Fatalf("options: %d %v", code, opts)
	}
	requireKeys(t, "options", opts, "types", "integrations", "limits")
	types, _ := opts["types"].([]any)
	if len(types) == 0 {
		t.Fatalf("options.types empty: %v", opts)
	}
	for _, ty := range types {
		requireKeys(t, "option type", ty.(map[string]any), "type", "supported", "paid_only", "available")
	}
	requireKeys(t, "limits", opts["limits"].(map[string]any),
		"max_methods", "max_rules", "max_delay_minutes", "max_label_length", "code_ttl_minutes")

	// Empty lists are arrays, not null, so the page can map over them.
	cmDeleteRules(t, f.member)
	resp := doJSONReq(t, http.MethodGet, f.srv.URL+"/api/v1/users/me/notification-rules", f.member.token, nil)
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(raw)) != "[]" {
		t.Fatalf("empty rules list = %q, want []", raw)
	}

	// Readiness is a map keyed by user id.
	code, ready, _ := f.member.do(http.MethodGet, "/users/paging-readiness", nil)
	if code != http.StatusOK {
		t.Fatalf("readiness: %d", code)
	}
	if _, ok := ready["users"].(map[string]any); !ok {
		t.Fatalf("readiness users is not an object: %v", ready)
	}
}
