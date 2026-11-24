package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelegramInteractivity_SendsChatIDOnStart(t *testing.T) {
	var gotPath string
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	h := NewTelegramInteractivityHandler(nil, nil, "secret", "bot-token")
	h.apiBase = srv.URL
	h.client = srv.Client()

	body := `{"update_id":1,"message":{"message_id":2,"from":{"id":3,"username":"jane"},"chat":{"id":-1001234567890,"type":"group"},"text":"/start"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/telegram", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
	rec := httptest.NewRecorder()
	h.Handle(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if gotPath != "/botbot-token/sendMessage" {
		t.Fatalf("path = %q", gotPath)
	}
	if got["chat_id"] != float64(-1001234567890) {
		t.Fatalf("chat_id = %v", got["chat_id"])
	}
	text, _ := got["text"].(string)
	if !strings.Contains(text, "-1001234567890") {
		t.Fatalf("text = %q", text)
	}
}
