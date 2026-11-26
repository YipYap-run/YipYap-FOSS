package handlers_test

import (
	"net/http"
	"testing"
)

// The options endpoint drives the add-method form. It must list every type,
// expose org DM integrations without their config, and never leak another
// org's integrations.
func TestContactMethodOptions(t *testing.T) {
	f := newCMFixture(t)
	code, ch, _ := f.owner.do(http.MethodPost, "/notification-channels", map[string]any{
		"name": "Team Slack", "type": "slack", "enabled": true,
		"config": map[string]any{"bot_token": "xoxb-secret-bot", "channel_id": "C0SHARED"},
	})
	if code != http.StatusCreated {
		t.Fatalf("create slack channel: %d %v", code, ch)
	}
	if code, out, _ := f.owner.do(http.MethodPost, "/notification-channels", map[string]any{
		"name": "Ntfy", "type": "ntfy", "enabled": true,
		"config": map[string]any{"topic": "team-alerts"},
	}); code != http.StatusCreated {
		t.Fatalf("create ntfy channel: %d %v", code, out)
	}

	code, opts, _ := f.member.do(http.MethodGet, "/users/me/contact-method-options", nil)
	if code != http.StatusOK {
		t.Fatalf("options: %d %v", code, opts)
	}
	types, _ := opts["types"].([]any)
	if len(types) != 10 {
		t.Fatalf("want 10 types, got %v", opts["types"])
	}
	byType := map[string]map[string]any{}
	for _, raw := range types {
		o := raw.(map[string]any)
		byType[o["type"].(string)] = o
	}
	if e := byType["email"]; e["available"] != true || e["supported"] != true || e["paid_only"] != false {
		t.Fatalf("email option = %v", e)
	}
	if s := byType["slack_dm"]; s["integration"] != "slack" {
		t.Fatalf("slack_dm option = %v", s)
	}

	ints, _ := opts["integrations"].([]any)
	if len(ints) != 1 {
		t.Fatalf("want only the slack integration, got %v", opts["integrations"])
	}
	in := ints[0].(map[string]any)
	if in["id"] != ch["id"] || in["name"] != "Team Slack" || in["type"] != "slack" || in["source"] != "own_bot" {
		t.Fatalf("integration = %v", in)
	}
	if _, leaked := in["config"]; leaked {
		t.Fatalf("integration config exposed: %v", in)
	}

	limits, _ := opts["limits"].(map[string]any)
	if limits["max_delay_minutes"].(float64) != 1440 || limits["code_ttl_minutes"].(float64) != 15 {
		t.Fatalf("limits = %v", limits)
	}

	// Another org sees none of these integrations.
	_, otherOpts, _ := f.other.do(http.MethodGet, "/users/me/contact-method-options", nil)
	if ints, _ := otherOpts["integrations"].([]any); len(ints) != 0 {
		t.Fatalf("cross-org integrations leaked: %v", ints)
	}
}

// Two rules with the same method, delay and urgency would page twice.
func TestNotificationRuleRejectsDuplicates(t *testing.T) {
	f := newCMFixture(t)
	_, _, methods := f.member.do(http.MethodGet, "/users/me/contact-methods", nil)
	id := methods[0].(map[string]any)["id"].(string)

	// The seeded rule is (method, 0, any).
	if code, out, _ := f.member.do(http.MethodPost, "/users/me/notification-rules", map[string]any{
		"contact_method_id": id, "delay_minutes": 0, "urgency": "any",
	}); code != http.StatusConflict {
		t.Fatalf("duplicate create: want 409, got %d %v", code, out)
	}
	code, rule, _ := f.member.do(http.MethodPost, "/users/me/notification-rules", map[string]any{
		"contact_method_id": id, "delay_minutes": 10, "urgency": "any",
	})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, rule)
	}
	if code, out, _ := f.member.do(http.MethodPatch, "/users/me/notification-rules/"+rule["id"].(string), map[string]any{
		"delay_minutes": 0,
	}); code != http.StatusConflict {
		t.Fatalf("duplicate update: want 409, got %d %v", code, out)
	}
	// Saving a rule unchanged is not a duplicate of itself.
	if code, out, _ := f.member.do(http.MethodPatch, "/users/me/notification-rules/"+rule["id"].(string), map[string]any{
		"delay_minutes": 10,
	}); code != http.StatusOK {
		t.Fatalf("unchanged update: %d %v", code, out)
	}
}
