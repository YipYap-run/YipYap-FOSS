package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// telegramLinkedChatID is the chat linked to org-test-1 by linkTelegramChat.
const telegramLinkedChatID = int64(-1001234567890)

func telegramCallbackBody(t *testing.T, action, alertID string, userID int) string {
	t.Helper()
	return telegramCallbackBodyFromChat(t, action, alertID, userID, telegramLinkedChatID)
}

func telegramCallbackBodyFromChat(t *testing.T, action, alertID string, userID int, chatID int64) string {
	t.Helper()
	payload := map[string]any{
		"update_id": 12345,
		"callback_query": map[string]any{
			"id": "query-id-1",
			"from": map[string]any{
				"id":       userID,
				"username": "jane",
			},
			"message": map[string]any{
				"message_id": 77,
				"chat":       map[string]any{"id": chatID, "type": "supergroup"},
				"text":       "[critical] Test Monitor",
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

// linkTelegramChat gives orgID an enabled telegram channel bound to chatID.
func linkTelegramChat(t *testing.T, s store.Store, orgID string, chatID int64, enabled bool) {
	t.Helper()
	ch := &domain.NotificationChannel{
		ID:      fmt.Sprintf("tg-%s-%d-%v", orgID, chatID, enabled),
		OrgID:   orgID,
		Type:    "telegram",
		Name:    "Telegram",
		Config:  fmt.Sprintf(`{"chat_id":"%d","chat_title":"Ops"}`, chatID),
		Enabled: enabled,
	}
	if err := s.NotificationChannels().Create(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
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
	linkTelegramChat(t, s, "org-test-1", telegramLinkedChatID, true)

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
	if ackEvt.UserID != "Telegram user @jane" {
		t.Fatalf("expected user_id=Telegram user @jane, got %s", ackEvt.UserID)
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
	linkTelegramChat(t, s, "org-test-1", telegramLinkedChatID, true)

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
		AlertID     string `json:"alert_id"`
		ResolvedBy  string `json:"resolved_by"`
		Channel     string `json:"channel"`
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
	if evt.AlertID != "alert-tg-resolve-1" || evt.ResolvedBy != "Telegram user @jane" || evt.Channel != "telegram" {
		t.Fatalf("recover event = %+v", evt)
	}
}

// A click is honoured only from a chat linked to an enabled telegram
// channel of the alert's org (notif-16).
func TestTelegramInteractivity_RejectsClicksFromUnlinkedChats(t *testing.T) {
	ts, s, msgBus := setupEventsTestServer(t)
	createMonitorWithIntegrationKey(t, s, "ik-tg-foreign")
	ctx := context.Background()
	alert := &domain.Alert{
		ID: "alert-tg-foreign", MonitorID: "mon-test-1", OrgID: "org-test-1",
		Status: domain.AlertFiring, Severity: domain.SeverityCritical, StartedAt: time.Now().UTC(),
	}
	if err := s.Alerts().Create(ctx, alert); err != nil {
		t.Fatal(err)
	}
	// Another org linked chat -200; org-test-1 has -300 linked but disabled.
	other := &domain.Org{ID: "org-other", Name: "Other", Slug: "other"}
	if err := s.Orgs().Create(ctx, other); err != nil {
		t.Fatal(err)
	}
	linkTelegramChat(t, s, "org-other", -200, true)
	linkTelegramChat(t, s, "org-test-1", -300, false)

	var mu sync.Mutex
	var got []string
	for _, subj := range []string{"alert.ack", "alert.recover"} {
		_ = msgBus.Subscribe(subj, func(ctx context.Context, subject string, data []byte) error {
			mu.Lock()
			got = append(got, subject)
			mu.Unlock()
			return nil
		})
	}

	cases := []struct {
		name   string
		chatID int64
		alert  string
	}{
		{"foreign org chat", -200, "alert-tg-foreign"},
		{"disabled channel", -300, "alert-tg-foreign"},
		{"unknown chat", -999, "alert-tg-foreign"},
		{"unknown alert", telegramLinkedChatID, "alert-missing"},
	}
	for _, tc := range cases {
		for _, action := range []string{"ack", "resolve"} {
			resp := postTelegramWebhook(t, ts.URL, telegramCallbackBodyFromChat(t, action, tc.alert, 5, tc.chatID))
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s %s: status %d", tc.name, action, resp.StatusCode)
			}
		}
	}
	// A callback with no message (so no chat) is rejected too.
	resp := postTelegramWebhook(t, ts.URL, `{"update_id":1,"callback_query":{"id":"q","from":{"id":1},"data":"ack:alert-tg-foreign"}}`)
	_ = resp.Body.Close()

	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 0 {
		t.Fatalf("published %v for clicks from unlinked chats", got)
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
