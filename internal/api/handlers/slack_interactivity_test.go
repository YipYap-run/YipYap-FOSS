package handlers_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func slackPayload(t *testing.T, actionID, value, userID string) string {
	t.Helper()
	p := map[string]any{
		"type": "block_actions",
		"user": map[string]string{"id": userID, "name": "jane"},
		"actions": []map[string]string{
			{"action_id": actionID, "value": value},
		},
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func postSlackAction(t *testing.T, baseURL, payload string) *http.Response {
	t.Helper()
	body := url.Values{"payload": {payload}}.Encode()
	ts := fmt.Sprintf("%d", time.Now().Unix())

	mac := hmac.New(sha256.New, []byte("test-slack-secret"))
	mac.Write([]byte("v0:" + ts + ":" + body))
	sig := "v0=" + hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequest("POST", baseURL+"/api/v1/integrations/slack/actions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", sig)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestSlackInteractivity_Ack(t *testing.T) {
	ts, s, msgBus := setupEventsTestServer(t)

	// Create an org, monitor, and alert.
	createMonitorWithIntegrationKey(t, s, "ik-slack-ack")
	ctx := context.Background()
	now := time.Now().UTC()
	alert := &domain.Alert{
		ID:        "alert-slack-ack-1",
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

	payload := slackPayload(t, "ack_alert", "alert-slack-ack-1", "U123")
	resp := postSlackAction(t, ts.URL, payload)
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
	if ackEvt.AlertID != "alert-slack-ack-1" {
		t.Fatalf("expected alert_id=alert-slack-ack-1, got %s", ackEvt.AlertID)
	}
	if ackEvt.UserID != "U123" {
		t.Fatalf("expected user_id=U123, got %s", ackEvt.UserID)
	}
	if ackEvt.Channel != "slack" {
		t.Fatalf("expected channel=slack, got %s", ackEvt.Channel)
	}
}

func TestSlackInteractivity_Resolve(t *testing.T) {
	ts, s, msgBus := setupEventsTestServer(t)

	createMonitorWithIntegrationKey(t, s, "ik-slack-resolve")
	ctx := context.Background()
	now := time.Now().UTC()
	alert := &domain.Alert{
		ID:        "alert-slack-resolve-1",
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

	payload := slackPayload(t, "resolve_alert", "alert-slack-resolve-1", "U456")
	resp := postSlackAction(t, ts.URL, payload)
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

// signedSlackRawPost sends a signed Slack request with an arbitrary body string.
func signedSlackRawPost(t *testing.T, baseURL, body string) *http.Response {
	t.Helper()
	ts := fmt.Sprintf("%d", time.Now().Unix())
	mac := hmac.New(sha256.New, []byte("test-slack-secret"))
	mac.Write([]byte("v0:" + ts + ":" + body))
	sig := "v0=" + hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequest("POST", baseURL+"/api/v1/integrations/slack/actions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", sig)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestSlackInteractivity_EmptyPayload(t *testing.T) {
	ts, _, _ := setupEventsTestServer(t)

	resp := signedSlackRawPost(t, ts.URL, "")
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestSlackInteractivity_InvalidJSON(t *testing.T) {
	ts, _, _ := setupEventsTestServer(t)

	form := url.Values{"payload": {"not-json"}}
	resp := signedSlackRawPost(t, ts.URL, form.Encode())
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestSlackInteractivity_NoActions(t *testing.T) {
	ts, _, _ := setupEventsTestServer(t)

	p, _ := json.Marshal(map[string]any{
		"type":    "block_actions",
		"user":    map[string]string{"id": "U1"},
		"actions": []map[string]string{},
	})
	form := url.Values{"payload": {string(p)}}
	resp := signedSlackRawPost(t, ts.URL, form.Encode())
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestSlackInteractivity_ResolveAlertNotFound(t *testing.T) {
	ts, _, _ := setupEventsTestServer(t)

	payload := slackPayload(t, "resolve_alert", "nonexistent-alert", "U789")
	resp := postSlackAction(t, ts.URL, payload)
	defer func() { _ = resp.Body.Close() }()

	// Should still return 200 to Slack even when alert not found.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}
