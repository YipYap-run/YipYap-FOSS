package providers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
)

// Verify that Slack implements notify.AckListener at compile time.
var _ notify.AckListener = (*Slack)(nil)

func TestResolveConfig_PerOrgOverride(t *testing.T) {
	override := SlackConfig{
		BotToken:  "xoxb-per-org-token",
		ChannelID: "C-PER-ORG",
	}
	raw, err := json.Marshal(override)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Version prefix 0x00 = plaintext.
	versioned := append([]byte{0x00}, raw...)
	encoded := base64.StdEncoding.EncodeToString(versioned)

	job := domain.NotificationJob{
		ID:           "j1",
		TargetConfig: encoded,
	}

	var dst SlackConfig
	err = resolveConfig(job, nil, &dst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dst.BotToken != "xoxb-per-org-token" {
		t.Errorf("expected per-org bot_token, got %s", dst.BotToken)
	}
	if dst.ChannelID != "C-PER-ORG" {
		t.Errorf("expected per-org channel_id, got %s", dst.ChannelID)
	}
}

func TestResolveConfig_EmptyFallback(t *testing.T) {
	job := domain.NotificationJob{
		ID:           "j2",
		TargetConfig: "",
	}

	var dst SlackConfig
	err := resolveConfig(job, nil, &dst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// dst should remain zero-value.
	if dst.BotToken != "" || dst.ChannelID != "" {
		t.Errorf("expected zero-value config, got %+v", dst)
	}
}

func TestResolveConfig_WithDecrypt(t *testing.T) {
	override := SlackConfig{
		BotToken:  "xoxb-decrypted",
		ChannelID: "C-DECRYPTED",
	}
	plaintext, _ := json.Marshal(override)

	// Simulate encryption: the "ciphertext" is just the plaintext reversed.
	ciphertext := make([]byte, len(plaintext))
	for i, b := range plaintext {
		ciphertext[len(plaintext)-1-i] = b
	}

	decrypt := func(ct []byte) ([]byte, error) {
		out := make([]byte, len(ct))
		for i, b := range ct {
			out[len(ct)-1-i] = b
		}
		return out, nil
	}

	// Version prefix 0x01 = encrypted.
	versioned := append([]byte{0x01}, ciphertext...)
	encoded := base64.StdEncoding.EncodeToString(versioned)

	job := domain.NotificationJob{
		ID:           "j3",
		TargetConfig: encoded,
	}

	var dst SlackConfig
	err := resolveConfig(job, decrypt, &dst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dst.BotToken != "xoxb-decrypted" {
		t.Errorf("expected decrypted bot_token, got %s", dst.BotToken)
	}
	if dst.ChannelID != "C-DECRYPTED" {
		t.Errorf("expected decrypted channel_id, got %s", dst.ChannelID)
	}
}

func TestResolveConfig_EncryptedWithoutDecryptFunc(t *testing.T) {
	// Version prefix 0x01 = encrypted, but no decrypt function provided.
	versioned := append([]byte{0x01}, []byte(`{"bot_token":"secret"}`)...)
	encoded := base64.StdEncoding.EncodeToString(versioned)

	job := domain.NotificationJob{
		ID:           "j3b",
		TargetConfig: encoded,
	}

	var dst SlackConfig
	err := resolveConfig(job, nil, &dst)
	if err == nil {
		t.Fatal("expected error for encrypted config without decrypt function")
	}
	if !strings.Contains(err.Error(), "encrypted config but no decrypt function configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolveConfig_DecryptError(t *testing.T) {
	decrypt := func([]byte) ([]byte, error) {
		return nil, errors.New("bad key")
	}

	// Version prefix 0x01 = encrypted.
	versioned := append([]byte{0x01}, []byte("encrypted-blob")...)
	encoded := base64.StdEncoding.EncodeToString(versioned)

	job := domain.NotificationJob{
		ID:           "j4",
		TargetConfig: encoded,
	}

	var dst SlackConfig
	err := resolveConfig(job, decrypt, &dst)
	if err == nil {
		t.Fatal("expected error from decrypt failure")
	}
	if !strings.Contains(err.Error(), "decrypt target config") {
		t.Errorf("expected wrapped decrypt error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "bad key") {
		t.Errorf("expected original error message preserved, got: %v", err)
	}
}

func TestResolveConfig_InvalidJSON(t *testing.T) {
	job := domain.NotificationJob{
		ID:           "j5",
		TargetConfig: "not-json",
	}

	var dst SlackConfig
	err := resolveConfig(job, nil, &dst)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestResolveConfig_LegacyRawJSON(t *testing.T) {
	// Raw JSON that is not valid base64 should fall through to direct unmarshal.
	job := domain.NotificationJob{
		ID:           "j6",
		TargetConfig: `{"bot_token":"legacy","channel_id":"C-LEGACY"}`,
	}

	var dst SlackConfig
	err := resolveConfig(job, nil, &dst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dst.BotToken != "legacy" {
		t.Errorf("expected bot_token 'legacy', got %s", dst.BotToken)
	}
}

func makeSlackInteractionRequest(t *testing.T, payload string) *http.Request {
	t.Helper()
	form := url.Values{"payload": {payload}}
	req := httptest.NewRequest(http.MethodPost, "/integrations/slack/actions",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestParseAck_AckAlert(t *testing.T) {
	s := &Slack{}
	payload, _ := json.Marshal(map[string]any{
		"type": "block_actions",
		"user": map[string]string{"id": "U123", "name": "jane"},
		"actions": []map[string]string{
			{"action_id": "ack_alert", "value": "alert-1"},
		},
	})

	req := makeSlackInteractionRequest(t, string(payload))
	evt, err := s.ParseAck(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt.AlertID != "alert-1" {
		t.Errorf("expected alert_id=alert-1, got %s", evt.AlertID)
	}
	if evt.UserID != "U123" {
		t.Errorf("expected user_id=U123, got %s", evt.UserID)
	}
	if evt.Channel != "slack" {
		t.Errorf("expected channel=slack, got %s", evt.Channel)
	}
}

func TestParseAck_ResolveAlert(t *testing.T) {
	s := &Slack{}
	payload, _ := json.Marshal(map[string]any{
		"type": "block_actions",
		"user": map[string]string{"id": "U456", "name": "bob"},
		"actions": []map[string]string{
			{"action_id": "resolve_alert", "value": "alert-2"},
		},
	})

	req := makeSlackInteractionRequest(t, string(payload))
	evt, err := s.ParseAck(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt.AlertID != "alert-2" {
		t.Errorf("expected alert_id=alert-2, got %s", evt.AlertID)
	}
	if evt.Channel != "slack:resolve" {
		t.Errorf("expected channel=slack:resolve, got %s", evt.Channel)
	}
}

func TestParseAck_MissingPayload(t *testing.T) {
	s := &Slack{}
	req := httptest.NewRequest(http.MethodPost, "/integrations/slack/actions", nil)
	_, err := s.ParseAck(req)
	if err == nil {
		t.Fatal("expected error for missing payload")
	}
}

func TestParseAck_NoActions(t *testing.T) {
	s := &Slack{}
	payload, _ := json.Marshal(map[string]any{
		"type":    "block_actions",
		"user":    map[string]string{"id": "U1"},
		"actions": []map[string]string{},
	})

	req := makeSlackInteractionRequest(t, string(payload))
	_, err := s.ParseAck(req)
	if err == nil {
		t.Fatal("expected error for empty actions")
	}
}

