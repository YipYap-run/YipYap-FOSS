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
