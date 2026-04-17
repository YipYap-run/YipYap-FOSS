package handlers_test

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func discordInteraction(t *testing.T, interactionType int, customID, userID string) string {
	t.Helper()
	p := map[string]any{
		"type": interactionType,
		"member": map[string]any{
			"user": map[string]string{"id": userID, "username": "jane"},
		},
		"data": map[string]any{
			"custom_id":      customID,
			"component_type": 2,
		},
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// discordTestPrivateKey is set by setupEventsTestServer via the matching public key.
var discordTestPrivateKey ed25519.PrivateKey

func postDiscordWebhook(t *testing.T, baseURL, body string) *http.Response {
	t.Helper()
	timestamp := "1234567890"
	msg := []byte(timestamp + body)
	sig := ed25519.Sign(discordTestPrivateKey, msg)

	req, err := http.NewRequest("POST", baseURL+"/api/v1/webhooks/discord", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature-Ed25519", hex.EncodeToString(sig))
	req.Header.Set("X-Signature-Timestamp", timestamp)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestDiscordInteractivity_Ping(t *testing.T) {
	ts, _, _ := setupEventsTestServer(t)

	body := `{"type":1}`
	resp := postDiscordWebhook(t, ts.URL, body)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result["type"] != float64(1) {
		t.Fatalf("expected PONG response type 1, got %v", result["type"])
	}
}

func TestDiscordInteractivity_Ack(t *testing.T) {
	ts, s, msgBus := setupEventsTestServer(t)

	createMonitorWithIntegrationKey(t, s, "ik-discord-ack")
	ctx := context.Background()
	now := time.Now().UTC()
	alert := &domain.Alert{
		ID:        "alert-discord-ack-1",
		MonitorID: "mon-test-1",
		OrgID:     "org-test-1",
		Status:    domain.AlertFiring,
		Severity:  domain.SeverityCritical,
		StartedAt: now,
	}
	if err := s.Alerts().Create(ctx, alert); err != nil {
		t.Fatal(err)
	}

	var received []byte
	var mu sync.Mutex
	done := make(chan struct{}, 1)
	_ = msgBus.Subscribe("alert.ack", func(ctx context.Context, subject string, data []byte) error {
		mu.Lock()
		received = data
		mu.Unlock()
		select {
		case done <- struct{}{}:
		default:
		}
		return nil
	})

	body := discordInteraction(t, 3, "ack:alert-discord-ack-1", "U123")
	resp := postDiscordWebhook(t, ts.URL, body)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Verify deferred update response (type 6).
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result["type"] != float64(6) {
		t.Fatalf("expected deferred response type 6, got %v", result["type"])
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for alert.ack message")
	}

	mu.Lock()
	defer mu.Unlock()
	var ackEvt domain.AckEvent
	if err := json.Unmarshal(received, &ackEvt); err != nil {
		t.Fatal(err)
	}
	if ackEvt.AlertID != "alert-discord-ack-1" {
		t.Fatalf("expected alert_id=alert-discord-ack-1, got %s", ackEvt.AlertID)
	}
	if ackEvt.UserID != "U123" {
		t.Fatalf("expected user_id=U123, got %s", ackEvt.UserID)
	}
	if ackEvt.Channel != "discord" {
		t.Fatalf("expected channel=discord, got %s", ackEvt.Channel)
	}
}

func TestDiscordInteractivity_Resolve(t *testing.T) {
	ts, s, msgBus := setupEventsTestServer(t)

	createMonitorWithIntegrationKey(t, s, "ik-discord-resolve")
	ctx := context.Background()
	now := time.Now().UTC()
	alert := &domain.Alert{
		ID:        "alert-discord-resolve-1",
		MonitorID: "mon-test-1",
		OrgID:     "org-test-1",
		Status:    domain.AlertFiring,
		Severity:  domain.SeverityCritical,
		StartedAt: now,
	}
	if err := s.Alerts().Create(ctx, alert); err != nil {
		t.Fatal(err)
	}

	var received []byte
	var mu sync.Mutex
	done := make(chan struct{}, 1)
	_ = msgBus.Subscribe("alert.recover", func(ctx context.Context, subject string, data []byte) error {
		mu.Lock()
		received = data
		mu.Unlock()
		select {
		case done <- struct{}{}:
		default:
		}
		return nil
	})

	body := discordInteraction(t, 3, "resolve:alert-discord-resolve-1", "U456")
	resp := postDiscordWebhook(t, ts.URL, body)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for alert.recover message")
	}

	mu.Lock()
	defer mu.Unlock()
	var evt struct {
		MonitorID   string `json:"monitor_id"`
		MonitorName string `json:"monitor_name"`
		Status      string `json:"status"`
	}
	if err := json.Unmarshal(received, &evt); err != nil {
		t.Fatal(err)
	}
	if evt.MonitorID != "mon-test-1" {
		t.Fatalf("expected monitor_id=mon-test-1, got %s", evt.MonitorID)
	}
	if evt.MonitorName != "Test Monitor" {
		t.Fatalf("expected monitor_name=Test Monitor, got %s", evt.MonitorName)
	}
	if evt.Status != "up" {
		t.Fatalf("expected status=up, got %s", evt.Status)
	}
}

func TestDiscordInteractivity_InvalidJSON(t *testing.T) {
	ts, _, _ := setupEventsTestServer(t)

	resp := postDiscordWebhook(t, ts.URL, "not-json")
	defer func() { _ = resp.Body.Close() }()

	// Should still return 200 to Discord even on parse errors.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestDiscordInteractivity_NoCustomID(t *testing.T) {
	ts, _, _ := setupEventsTestServer(t)

	body, _ := json.Marshal(map[string]any{
		"type": 3,
		"member": map[string]any{
			"user": map[string]string{"id": "U1"},
		},
		"data": map[string]any{
			"component_type": 2,
		},
	})
	resp := postDiscordWebhook(t, ts.URL, string(body))
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result["type"] != float64(6) {
		t.Fatalf("expected deferred response type 6, got %v", result["type"])
	}
}

func TestDiscordInteractivity_ResolveAlertNotFound(t *testing.T) {
	ts, _, _ := setupEventsTestServer(t)

	body := discordInteraction(t, 3, "resolve:nonexistent-alert", "U789")
	resp := postDiscordWebhook(t, ts.URL, body)
	defer func() { _ = resp.Body.Close() }()

	// Should still return 200 to Discord even when alert not found.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}
