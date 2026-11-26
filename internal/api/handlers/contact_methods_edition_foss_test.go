package handlers_test

import (
	"net/http"
	"testing"
)

// The self-hosted edition has no SMS or voice provider, so those methods
// are refused instead of silently never delivering.
func TestContactMethodPhoneTypesUnavailableInFOSS(t *testing.T) {
	f := newCMFixture(t)
	for _, typ := range []string{"sms", "voice"} {
		code, out, _ := f.member.do(http.MethodPost, "/users/me/contact-methods", map[string]any{
			"type": typ, "config": map[string]any{"phone": "+12025550123"},
		})
		if code != http.StatusBadRequest {
			t.Fatalf("%s: want 400, got %d %v", typ, code, out)
		}
	}
	// Telegram is not plan-gated in FOSS.
	if code, out, _ := f.member.do(http.MethodPost, "/users/me/contact-methods", map[string]any{
		"type": "telegram", "config": map[string]any{"chat_id": "12345"},
	}); code != http.StatusCreated {
		t.Fatalf("telegram: %d %v", code, out)
	}
}

// The FOSS build lists SMS and voice as unsupported so the form hides them.
func TestContactMethodOptionsFOSS(t *testing.T) {
	f := newCMFixture(t)
	_, opts, _ := f.member.do(http.MethodGet, "/users/me/contact-method-options", nil)
	for _, raw := range opts["types"].([]any) {
		o := raw.(map[string]any)
		phone := o["type"] == "sms" || o["type"] == "voice"
		if o["supported"] == phone || o["available"] == phone {
			t.Fatalf("option %v", o)
		}
		if o["paid_only"] != false {
			t.Fatalf("paid_only in FOSS: %v", o)
		}
	}
}

// The self-hosted edition has no platform chat apps: DMs only send through
// an org channel's own bot, and an app install reference is refused.
func TestContactMethodAppInstallsUnavailableInFOSS(t *testing.T) {
	f := newCMFixture(t)
	code, ch, _ := f.owner.do(http.MethodPost, "/notification-channels", map[string]any{
		"name": "Team Slack", "type": "slack", "enabled": true,
		"config": map[string]any{"bot_token": "xoxb-secret-bot", "channel_id": "C0SHARED"},
	})
	if code != http.StatusCreated {
		t.Fatalf("create slack channel: %d %v", code, ch)
	}
	_, opts, _ := f.member.do(http.MethodGet, "/users/me/contact-method-options", nil)
	for _, raw := range opts["integrations"].([]any) {
		if in := raw.(map[string]any); in["source"] != "own_bot" {
			t.Fatalf("integration %v", in)
		}
	}
	if code, out, _ := f.member.do(http.MethodPost, "/users/me/contact-methods", map[string]any{
		"type": "slack_dm", "config": map[string]any{"slack_user_id": "U0123ABCD", "via_install_id": ch["id"]},
	}); code != http.StatusBadRequest {
		t.Fatalf("app install in FOSS: %d %v", code, out)
	}
	if code, out, _ := f.member.do(http.MethodPost, "/users/me/contact-methods", map[string]any{
		"type": "slack_dm", "config": map[string]any{"slack_user_id": "U0123ABCD", "via_channel_id": ch["id"]},
	}); code != http.StatusCreated {
		t.Fatalf("own bot in FOSS: %d %v", code, out)
	}
}
