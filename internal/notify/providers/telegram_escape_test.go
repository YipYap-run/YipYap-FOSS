package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func telegramEscapeJob() domain.NotificationJob {
	return domain.NotificationJob{
		AlertID:          "alert-1",
		MonitorName:      "API <prod> & co",
		Severity:         "critical",
		Message:          "status <500> & \"down\"",
		ServiceName:      "Checkout <eu>",
		RunbookURL:       `https://wiki.example/run?a=1&b="x"`,
		DependencyStatus: json.RawMessage(`[{"service_name":"DB <primary>","relationship":"depends_on & more","is_alerting":true}]`),
		ContextLinks:     json.RawMessage(`[{"label":"Logs <tail>","url":"https://logs.example/?q=a&b=<c>"},{"label":"Bad","url":"javascript:alert(1)"}]`),
	}
}

func TestTelegramMessageText_EscapesEveryValue(t *testing.T) {
	text := telegramMessageText(telegramEscapeJob())
	for _, want := range []string{
		"[critical] API &lt;prod&gt; &amp; co",
		"status &lt;500&gt; &amp; &#34;down&#34;",
		"<b>Service:</b> Checkout &lt;eu&gt;",
		"DB &lt;primary&gt; (depends_on &amp; more)",
		`<a href="https://wiki.example/run?a=1&amp;b=&#34;x&#34;">Runbook</a>`,
		`<a href="https://logs.example/?q=a&amp;b=&lt;c&gt;">Logs &lt;tail&gt;</a>`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	for _, bad := range []string{"<prod>", "<500>", "<eu>", "<primary>", "<tail>", "javascript:"} {
		if strings.Contains(text, bad) {
			t.Errorf("unescaped %q in:\n%s", bad, text)
		}
	}
	// A non-http link keeps its label as plain text.
	if !strings.Contains(text, "| Bad") {
		t.Errorf("expected plain label for rejected link in:\n%s", text)
	}
	// Only the tags this code writes may appear.
	stripped := text
	for _, tag := range []string{"<b>", "</b>", "</a>"} {
		stripped = strings.ReplaceAll(stripped, tag, "")
	}
	stripped = strings.ReplaceAll(stripped, `<a href="`, "")
	if strings.Contains(stripped, "<") {
		t.Errorf("stray '<' left in:\n%s", text)
	}
}

func TestTelegramSend_PostsEscapedHTML(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bottok/sendMessage" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42}}`))
	}))
	defer srv.Close()

	tg := NewTelegram(TelegramConfig{BotToken: "tok", ChatID: "123"}, nil)
	tg.apiBase = srv.URL
	tg.client = srv.Client()
	id, err := tg.Send(context.Background(), telegramEscapeJob())
	if err != nil {
		t.Fatal(err)
	}
	if id != "42" {
		t.Fatalf("id = %q", id)
	}
	if got["parse_mode"] != "HTML" {
		t.Fatalf("parse_mode = %v", got["parse_mode"])
	}
	text, _ := got["text"].(string)
	if !strings.Contains(text, "API &lt;prod&gt; &amp; co") {
		t.Fatalf("text not escaped: %q", text)
	}
}
