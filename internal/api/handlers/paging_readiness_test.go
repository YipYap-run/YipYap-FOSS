package handlers_test

import (
	"context"
	"net/http"
	"testing"
)

func cmMemberID(t *testing.T, f cmFixture) string {
	t.Helper()
	member, err := f.s.Users().GetByEmail(context.Background(), "cm-member@test.com")
	if err != nil {
		t.Fatal(err)
	}
	return member.ID
}

func cmDeleteRules(t *testing.T, c cmClient) {
	t.Helper()
	_, _, rules := c.do(http.MethodGet, "/users/me/notification-rules", nil)
	for _, r := range rules {
		id := r.(map[string]any)["id"].(string)
		if code, _, _ := c.do(http.MethodDelete, "/users/me/notification-rules/"+id, nil); code != http.StatusNoContent {
			t.Fatalf("delete rule: %d", code)
		}
	}
}

// The on-call page and step editor warn about members nobody can page. The
// readiness endpoint must follow the member's rules.
func TestPagingReadinessFollowsRules(t *testing.T) {
	f := newCMFixture(t)
	memberID := cmMemberID(t, f)

	// The member's verified account email is seeded with an immediate rule.
	code, body, _ := f.member.do(http.MethodGet, "/users/paging-readiness", nil)
	if code != http.StatusOK {
		t.Fatalf("readiness: %d %v", code, body)
	}
	users, _ := body["users"].(map[string]any)
	m, _ := users[memberID].(map[string]any)
	if m == nil || m["pageable"] != true {
		t.Fatalf("member readiness = %v", users)
	}
	if types, _ := m["types"].([]any); len(types) != 1 || types[0] != "email" {
		t.Fatalf("member types = %v", m["types"])
	}

	// Remove every rule: the member can no longer be paged.
	cmDeleteRules(t, f.member)
	_, body, _ = f.owner.do(http.MethodGet, "/users/paging-readiness", nil)
	users, _ = body["users"].(map[string]any)
	if _, present := users[memberID]; present {
		t.Fatalf("member without rules still listed: %v", users)
	}

	// Another org sees nothing of this org's members.
	_, body, _ = f.other.do(http.MethodGet, "/users/paging-readiness", nil)
	users, _ = body["users"].(map[string]any)
	if _, present := users[memberID]; present {
		t.Fatal("readiness leaked across orgs")
	}
}

// An unverified method with a rule does not make a member pageable.
func TestPagingReadinessIgnoresUnverified(t *testing.T) {
	f := newCMFixture(t)
	memberID := cmMemberID(t, f)
	cmDeleteRules(t, f.member)
	code, m, _ := f.member.do(http.MethodPost, "/users/me/contact-methods", map[string]any{
		"type": "email", "label": "Personal", "config": map[string]any{"address": "me@example.com"},
	})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, m)
	}
	if code, _, _ := f.member.do(http.MethodPost, "/users/me/notification-rules", map[string]any{
		"contact_method_id": m["id"], "delay_minutes": 0, "urgency": "any",
	}); code != http.StatusCreated {
		t.Fatalf("rule: %d", code)
	}
	_, body, _ := f.member.do(http.MethodGet, "/users/paging-readiness", nil)
	users, _ := body["users"].(map[string]any)
	if _, present := users[memberID]; present {
		t.Fatalf("unverified method counted: %v", users)
	}
}
