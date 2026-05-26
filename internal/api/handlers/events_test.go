package handlers_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

func setupEventsTestServer(t *testing.T) (*httptest.Server, *sqlite.SQLiteStore, bus.Bus) {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	msgBus := bus.NewChannel()
	t.Cleanup(func() { _ = msgBus.Close() })

	// Generate a test Ed25519 keypair for Discord signature verification.
	pub, priv, _ := ed25519.GenerateKey(nil)
	discordTestPrivateKey = priv

	jwt := auth.NewJWTIssuer([]byte("test-secret-key"), 1*time.Hour)
	handler := api.NewServer(s, jwt, msgBus, nil, nil, "", "", api.ServerOptions{
		TelegramWebhookSecret: "test-telegram-secret",
		SlackSigningSecret:    "test-slack-secret",
		DiscordPublicKey:      hex.EncodeToString(pub),
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	return ts, s, msgBus
}

// createMonitorWithIntegrationKey creates an org and a monitor with the given integration key.
func createMonitorWithIntegrationKey(t *testing.T, s *sqlite.SQLiteStore, integrationKey string) *domain.Monitor {
	t.Helper()
	ctx := context.Background()

	org := &domain.Org{
		ID:   "org-test-1",
		Name: "Test Org",
		Slug: "test-org",
	}
	_ = s.Orgs().Create(ctx, org)

	now := time.Now().UTC().Truncate(time.Second)
	m := &domain.Monitor{
		ID:              "mon-test-1",
		OrgID:           org.ID,
		Name:            "Test Monitor",
		Type:            domain.MonitorHTTP,
		Config:          json.RawMessage(`{"url":"https://example.com"}`),
		IntervalSeconds: 60,
		TimeoutSeconds:  10,
		IntegrationKey:  integrationKey,
		Enabled:         true,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.Monitors().Create(ctx, m); err != nil {
		t.Fatal(err)
	}
	return m
}

func postEvent(t *testing.T, ts *httptest.Server, body map[string]interface{}) *http.Response {
	t.Helper()
	data, _ := json.Marshal(body)
	resp, err := http.Post(ts.URL+"/api/v1/events", "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response) map[string]interface{} {
	t.Helper()
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return result
}

func TestEventsAPI_Trigger(t *testing.T) {
	ts, s, msgBus := setupEventsTestServer(t)
	createMonitorWithIntegrationKey(t, s, "ik-trigger-test")

	// Subscribe to alert.trigger to verify the event is published.
	var received []byte
	var mu sync.Mutex
	done := make(chan struct{}, 1)
	_ = msgBus.Subscribe("alert.trigger", func(ctx context.Context, subject string, data []byte) error {
		mu.Lock()
		received = data
		mu.Unlock()
		select {
		case done <- struct{}{}:
		default:
		}
		return nil
	})

	resp := postEvent(t, ts, map[string]interface{}{
		"routing_key":  "ik-trigger-test",
		"event_action": "trigger",
		"dedup_key":    "dedup-1",
		"payload": map[string]interface{}{
			"summary":  "CPU at 95%",
			"severity": "critical",
			"source":   "datadog",
		},
	})

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}

	result := decodeJSON(t, resp)
	if result["status"] != "success" {
		t.Fatalf("expected status=success, got %v", result["status"])
	}
	if result["dedup_key"] != "dedup-1" {
		t.Fatalf("expected dedup_key=dedup-1, got %v", result["dedup_key"])
	}

	// Wait for the bus to deliver the message.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for alert.trigger message")
	}

	mu.Lock()
	defer mu.Unlock()
	var evt struct {
		MonitorID string `json:"monitor_id"`
		Status    string `json:"status"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(received, &evt); err != nil {
		t.Fatal(err)
	}
	if evt.MonitorID != "mon-test-1" {
		t.Fatalf("expected monitor_id=mon-test-1, got %s", evt.MonitorID)
	}
	if evt.Status != "down" {
		t.Fatalf("expected status=down, got %s", evt.Status)
	}
	if evt.Error == "" {
		t.Fatal("expected error field to contain summary")
	}
}

func TestEventsAPI_TriggerAutoDedup(t *testing.T) {
	ts, s, _ := setupEventsTestServer(t)
	createMonitorWithIntegrationKey(t, s, "ik-auto-dedup")

	resp := postEvent(t, ts, map[string]interface{}{
		"routing_key":  "ik-auto-dedup",
		"event_action": "trigger",
		"payload": map[string]interface{}{
			"summary":  "Disk full",
			"severity": "warning",
		},
	})

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}

	result := decodeJSON(t, resp)
	if result["dedup_key"] == nil || result["dedup_key"] == "" {
		t.Fatal("expected auto-generated dedup_key")
	}
}

func TestEventsAPI_InfoSeverityMapsToDegraded(t *testing.T) {
	ts, s, msgBus := setupEventsTestServer(t)
	createMonitorWithIntegrationKey(t, s, "ik-info-test")

	var received []byte
	done := make(chan struct{}, 1)
	_ = msgBus.Subscribe("alert.trigger", func(ctx context.Context, subject string, data []byte) error {
		received = data
		select {
		case done <- struct{}{}:
		default:
		}
		return nil
	})

	resp := postEvent(t, ts, map[string]interface{}{
		"routing_key":  "ik-info-test",
		"event_action": "trigger",
		"dedup_key":    "info-1",
		"payload": map[string]interface{}{
			"summary":  "Something minor",
			"severity": "info",
		},
	})

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}

	var evt struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(received, &evt)
	if evt.Status != "degraded" {
		t.Fatalf("expected status=degraded for info severity, got %s", evt.Status)
	}
}

func TestEventsAPI_InvalidRoutingKey(t *testing.T) {
	ts, _, _ := setupEventsTestServer(t)

	resp := postEvent(t, ts, map[string]interface{}{
		"routing_key":  "nonexistent-key",
		"event_action": "trigger",
		"payload": map[string]interface{}{
			"summary":  "test",
			"severity": "critical",
		},
	})

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestEventsAPI_MissingRoutingKey(t *testing.T) {
	ts, _, _ := setupEventsTestServer(t)

	resp := postEvent(t, ts, map[string]interface{}{
		"event_action": "trigger",
	})

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestEventsAPI_InvalidEventAction(t *testing.T) {
	ts, s, _ := setupEventsTestServer(t)
	createMonitorWithIntegrationKey(t, s, "ik-invalid-action")

	resp := postEvent(t, ts, map[string]interface{}{
		"routing_key":  "ik-invalid-action",
		"event_action": "invalid",
	})

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestEventsAPI_TriggerMissingPayload(t *testing.T) {
	ts, s, _ := setupEventsTestServer(t)
	createMonitorWithIntegrationKey(t, s, "ik-no-payload")

	resp := postEvent(t, ts, map[string]interface{}{
		"routing_key":  "ik-no-payload",
		"event_action": "trigger",
	})

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestEventsAPI_TriggerMissingSummary(t *testing.T) {
	ts, s, _ := setupEventsTestServer(t)
	createMonitorWithIntegrationKey(t, s, "ik-no-summary")

	resp := postEvent(t, ts, map[string]interface{}{
		"routing_key":  "ik-no-summary",
		"event_action": "trigger",
		"payload": map[string]interface{}{
			"severity": "critical",
		},
	})

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestEventsAPI_TriggerInvalidSeverity(t *testing.T) {
	ts, s, _ := setupEventsTestServer(t)
	createMonitorWithIntegrationKey(t, s, "ik-bad-sev")

	resp := postEvent(t, ts, map[string]interface{}{
		"routing_key":  "ik-bad-sev",
		"event_action": "trigger",
		"payload": map[string]interface{}{
			"summary":  "test",
			"severity": "extreme",
		},
	})

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestEventsAPI_AcknowledgeMissingDedupKey(t *testing.T) {
	ts, s, _ := setupEventsTestServer(t)
	createMonitorWithIntegrationKey(t, s, "ik-ack-no-dedup")

	resp := postEvent(t, ts, map[string]interface{}{
		"routing_key":  "ik-ack-no-dedup",
		"event_action": "acknowledge",
	})

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestEventsAPI_ResolveMissingDedupKey(t *testing.T) {
	ts, s, _ := setupEventsTestServer(t)
	createMonitorWithIntegrationKey(t, s, "ik-resolve-no-dedup")

	resp := postEvent(t, ts, map[string]interface{}{
		"routing_key":  "ik-resolve-no-dedup",
		"event_action": "resolve",
	})

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestEventsAPI_AcknowledgeWithActiveAlert(t *testing.T) {
	ts, s, msgBus := setupEventsTestServer(t)
	createMonitorWithIntegrationKey(t, s, "ik-ack-test")

	// Create an active alert for the monitor.
	ctx := context.Background()
	now := time.Now().UTC()
	alert := &domain.Alert{
		ID:        "alert-ack-1",
		MonitorID: "mon-test-1",
		OrgID:     "org-test-1",
		Status:    domain.AlertFiring,
		Severity:  domain.SeverityCritical,
		StartedAt: now,
	}
	if err := s.Alerts().Create(ctx, alert); err != nil {
		t.Fatal(err)
	}

	// Subscribe to alert.ack.
	var received []byte
	done := make(chan struct{}, 1)
	_ = msgBus.Subscribe("alert.ack", func(ctx context.Context, subject string, data []byte) error {
		received = data
		select {
		case done <- struct{}{}:
		default:
		}
		return nil
	})

	resp := postEvent(t, ts, map[string]interface{}{
		"routing_key":  "ik-ack-test",
		"event_action": "acknowledge",
		"dedup_key":    "alert-ack-1",
	})

	if resp.StatusCode != http.StatusAccepted {
		body := decodeJSON(t, resp)
		t.Fatalf("expected 202, got %d: %v", resp.StatusCode, body)
	}

	result := decodeJSON(t, resp)
	if result["dedup_key"] != "alert-ack-1" {
		t.Fatalf("expected dedup_key=alert-ack-1, got %v", result["dedup_key"])
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for alert.ack message")
	}

	var ackEvt domain.AckEvent
	if err := json.Unmarshal(received, &ackEvt); err != nil {
		t.Fatal(err)
	}
	if ackEvt.AlertID != "alert-ack-1" {
		t.Fatalf("expected alert_id=alert-ack-1, got %s", ackEvt.AlertID)
	}
	if ackEvt.Channel != "events_api" {
		t.Fatalf("expected channel=events_api, got %s", ackEvt.Channel)
	}
}

func TestEventsAPI_ResolveWithActiveAlert(t *testing.T) {
	ts, s, msgBus := setupEventsTestServer(t)
	createMonitorWithIntegrationKey(t, s, "ik-resolve-test")

	ctx := context.Background()
	now := time.Now().UTC()
	alert := &domain.Alert{
		ID:        "alert-resolve-1",
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
	done := make(chan struct{}, 1)
	_ = msgBus.Subscribe("alert.recover", func(ctx context.Context, subject string, data []byte) error {
		received = data
		select {
		case done <- struct{}{}:
		default:
		}
		return nil
	})

	resp := postEvent(t, ts, map[string]interface{}{
		"routing_key":  "ik-resolve-test",
		"event_action": "resolve",
		"dedup_key":    "alert-resolve-1",
	})

	if resp.StatusCode != http.StatusAccepted {
		body := decodeJSON(t, resp)
		t.Fatalf("expected 202, got %d: %v", resp.StatusCode, body)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for alert.recover message")
	}

	var evt struct {
		MonitorID string `json:"monitor_id"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(received, &evt); err != nil {
		t.Fatal(err)
	}
	if evt.MonitorID != "mon-test-1" {
		t.Fatalf("expected monitor_id=mon-test-1, got %s", evt.MonitorID)
	}
	if evt.Status != "up" {
		t.Fatalf("expected status=up, got %s", evt.Status)
	}
}

func TestEventsAPI_AcknowledgeNoActiveAlert(t *testing.T) {
	ts, s, _ := setupEventsTestServer(t)
	createMonitorWithIntegrationKey(t, s, "ik-no-alert")

	resp := postEvent(t, ts, map[string]interface{}{
		"routing_key":  "ik-no-alert",
		"event_action": "acknowledge",
		"dedup_key":    "nonexistent-alert",
	})

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}
