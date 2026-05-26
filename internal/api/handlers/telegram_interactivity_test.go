package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func telegramCallbackBody(t *testing.T, action, alertID string, userID int) string {
	t.Helper()
	payload := map[string]any{
		"update_id": 12345,
		"callback_query": map[string]any{
			"id": "query-id-1",
			"from": map[string]any{
				"id":       userID,
				"username": "jane",
			},
			"data": action + ":" + alertID,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func postTelegramWebhook(t *testing.T, baseURL, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", baseURL+"/api/v1/webhooks/telegram", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "test-telegram-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestTelegramInteractivity_Ack(t *testing.T) {
	ts, s, msgBus := setupEventsTestServer(t)

	createMonitorWithIntegrationKey(t, s, "ik-tg-ack")
	ctx := context.Background()
	now := time.Now().UTC()
	alert := &domain.Alert{
		ID:        "alert-tg-ack-1",
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

	body := telegramCallbackBody(t, "ack", "alert-tg-ack-1", 67890)
	resp := postTelegramWebhook(t, ts.URL, body)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
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
	if ackEvt.AlertID != "alert-tg-ack-1" {
		t.Fatalf("expected alert_id=alert-tg-ack-1, got %s", ackEvt.AlertID)
	}
	if ackEvt.UserID != "67890" {
		t.Fatalf("expected user_id=67890, got %s", ackEvt.UserID)
	}
	if ackEvt.Channel != "telegram" {
		t.Fatalf("expected channel=telegram, got %s", ackEvt.Channel)
	}
}

func TestTelegramInteractivity_Resolve(t *testing.T) {
	ts, s, msgBus := setupEventsTestServer(t)

	createMonitorWithIntegrationKey(t, s, "ik-tg-resolve")
	ctx := context.Background()
	now := time.Now().UTC()
	alert := &domain.Alert{
		ID:        "alert-tg-resolve-1",
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

	body := telegramCallbackBody(t, "resolve", "alert-tg-resolve-1", 11111)
	resp := postTelegramWebhook(t, ts.URL, body)
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

func TestTelegramInteractivity_NonCallbackUpdate(t *testing.T) {
	ts, _, _ := setupEventsTestServer(t)

	// A message update (no callback_query) should return 200 with no action.
	body := `{"update_id": 12345, "message": {"text": "hello"}}`
	resp := postTelegramWebhook(t, ts.URL, body)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestTelegramInteractivity_InvalidJSON(t *testing.T) {
	ts, _, _ := setupEventsTestServer(t)

	resp := postTelegramWebhook(t, ts.URL, "not-json{{{")
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestTelegramInteractivity_MalformedCallbackData(t *testing.T) {
	ts, _, _ := setupEventsTestServer(t)

	// callback_data with no colon separator.
	body := `{"update_id": 12345, "callback_query": {"id": "q1", "from": {"id": 1, "username": "x"}, "data": "invalid-no-colon"}}`
	resp := postTelegramWebhook(t, ts.URL, body)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}
