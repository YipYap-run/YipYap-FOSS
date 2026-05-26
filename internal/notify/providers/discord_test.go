package providers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
)

// Verify that Discord implements notify.AckListener at compile time.
var _ notify.AckListener = (*Discord)(nil)

func makeDiscordInteractionRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/discord", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestDiscordParseAck_AckAlert(t *testing.T) {
	d := &Discord{}
	payload, _ := json.Marshal(map[string]any{
		"type": 3,
		"member": map[string]any{
			"user": map[string]string{"id": "123456", "username": "jane"},
		},
		"data": map[string]any{
			"custom_id":      "ack:alert-1",
			"component_type": 2,
		},
	})

	req := makeDiscordInteractionRequest(t, string(payload))
	evt, err := d.ParseAck(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt.AlertID != "alert-1" {
		t.Errorf("expected alert_id=alert-1, got %s", evt.AlertID)
	}
	if evt.UserID != "123456" {
		t.Errorf("expected user_id=123456, got %s", evt.UserID)
	}
	if evt.Channel != "discord" {
		t.Errorf("expected channel=discord, got %s", evt.Channel)
	}
}

func TestDiscordParseAck_ResolveAlert(t *testing.T) {
	d := &Discord{}
	payload, _ := json.Marshal(map[string]any{
		"type": 3,
		"member": map[string]any{
			"user": map[string]string{"id": "789", "username": "bob"},
		},
		"data": map[string]any{
			"custom_id":      "resolve:alert-2",
			"component_type": 2,
		},
	})

	req := makeDiscordInteractionRequest(t, string(payload))
	evt, err := d.ParseAck(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt.AlertID != "alert-2" {
		t.Errorf("expected alert_id=alert-2, got %s", evt.AlertID)
	}
	if evt.Channel != "discord:resolve" {
		t.Errorf("expected channel=discord:resolve, got %s", evt.Channel)
	}
}

func TestDiscordParseAck_InvalidJSON(t *testing.T) {
	d := &Discord{}
	req := makeDiscordInteractionRequest(t, "not-json")
	_, err := d.ParseAck(req)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestDiscordParseAck_MissingCustomID(t *testing.T) {
	d := &Discord{}
	payload, _ := json.Marshal(map[string]any{
		"type": 3,
		"member": map[string]any{
			"user": map[string]string{"id": "123"},
		},
		"data": map[string]any{
			"component_type": 2,
		},
	})

	req := makeDiscordInteractionRequest(t, string(payload))
	_, err := d.ParseAck(req)
	if err == nil {
		t.Fatal("expected error for missing custom_id")
	}
}

func TestDiscordParseAck_InvalidCustomIDFormat(t *testing.T) {
	d := &Discord{}
	payload, _ := json.Marshal(map[string]any{
		"type": 3,
		"member": map[string]any{
			"user": map[string]string{"id": "123"},
		},
		"data": map[string]any{
			"custom_id":      "no-colon-here",
			"component_type": 2,
		},
	})

	req := makeDiscordInteractionRequest(t, string(payload))
	_, err := d.ParseAck(req)
	if err == nil {
		t.Fatal("expected error for invalid custom_id format")
	}
}
