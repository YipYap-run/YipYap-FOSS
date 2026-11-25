package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
)

// Verify that Telegram implements notify.AckListener at compile time.
var _ notify.AckListener = (*Telegram)(nil)

func makeTelegramCallbackRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/telegram",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestTelegramParseAck_Ack(t *testing.T) {
	tg := &Telegram{}
	body, _ := json.Marshal(map[string]any{
		"update_id": 12345,
		"callback_query": map[string]any{
			"id": "q1",
			"from": map[string]any{
				"id":       67890,
				"username": "jane",
			},
			"data": "ack:alert-uuid-1",
		},
	})

	req := makeTelegramCallbackRequest(t, string(body))
	evt, err := tg.ParseAck(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt.AlertID != "alert-uuid-1" {
		t.Errorf("expected alert_id=alert-uuid-1, got %s", evt.AlertID)
	}
	if evt.UserID != "67890" {
		t.Errorf("expected user_id=67890, got %s", evt.UserID)
	}
	if evt.Channel != "telegram" {
		t.Errorf("expected channel=telegram, got %s", evt.Channel)
	}
}

func TestTelegramParseAck_Resolve(t *testing.T) {
	tg := &Telegram{}
	body, _ := json.Marshal(map[string]any{
		"update_id": 12346,
		"callback_query": map[string]any{
			"id": "q2",
			"from": map[string]any{
				"id":       11111,
				"username": "bob",
			},
			"data": "resolve:alert-uuid-2",
		},
	})

	req := makeTelegramCallbackRequest(t, string(body))
	evt, err := tg.ParseAck(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt.AlertID != "alert-uuid-2" {
		t.Errorf("expected alert_id=alert-uuid-2, got %s", evt.AlertID)
	}
	if evt.Channel != "telegram:resolve" {
		t.Errorf("expected channel=telegram:resolve, got %s", evt.Channel)
	}
}

func TestTelegramParseAck_NoCallbackQuery(t *testing.T) {
	tg := &Telegram{}
	body := `{"update_id": 12345, "message": {"text": "hello"}}`
	req := makeTelegramCallbackRequest(t, body)
	_, err := tg.ParseAck(req)
	if err == nil {
		t.Fatal("expected error for missing callback_query")
	}
}

func TestTelegramParseAck_MalformedData(t *testing.T) {
	tg := &Telegram{}
	body, _ := json.Marshal(map[string]any{
		"update_id": 12345,
		"callback_query": map[string]any{
			"id":   "q3",
			"from": map[string]any{"id": 1, "username": "x"},
			"data": "no-colon-here",
		},
	})

	req := makeTelegramCallbackRequest(t, string(body))
	_, err := tg.ParseAck(req)
	if err == nil {
		t.Fatal("expected error for malformed callback_data")
	}
}

func TestTelegramParseAck_InvalidJSON(t *testing.T) {
	tg := &Telegram{}
	req := makeTelegramCallbackRequest(t, "not-json{{{")
	_, err := tg.ParseAck(req)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// A channel whose chat has not sent its /link code has no chat_id; the
// send fails without calling Telegram (notif-16).
func TestTelegramSendUnlinkedChannelFails(t *testing.T) {
	tg := NewTelegram(TelegramConfig{BotToken: "platform"}, nil)
	tg.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("telegram API called for an unlinked channel")
		return nil, nil
	})}
	cfg := append([]byte{0x00}, []byte(`{"chat_title":"x"}`)...)
	_, err := tg.Send(context.Background(), domain.NotificationJob{
		AlertID: "a1", MonitorName: "m", TargetConfig: base64.StdEncoding.EncodeToString(cfg),
	})
	if err == nil || !strings.Contains(err.Error(), "not linked") {
		t.Fatalf("err = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
