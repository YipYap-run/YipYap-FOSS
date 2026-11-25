package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

type captureRT struct {
	body []byte
	resp string
}

func (c *captureRT) RoundTrip(r *http.Request) (*http.Response, error) {
	c.body, _ = io.ReadAll(r.Body)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(c.resp)),
		Request:    r,
	}, nil
}

// decodedBody re-encodes a JSON body without HTML escaping so assertions can
// match link markup literally.
func decodedBody(t *testing.T, raw []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return buf.String()
}

func byoJob(cfg any) domain.NotificationJob {
	raw, _ := json.Marshal(cfg)
	return domain.NotificationJob{
		ID:           "j1",
		AlertID:      "alert-123",
		MonitorName:  "API",
		Severity:     "critical",
		Message:      "Alert: API is firing",
		TargetConfig: string(raw),
	}
}

// notif-12: a bring-your-own Slack bot cannot route button clicks to YipYap,
// so the message must not carry Ack/Resolve buttons, only a console link.
func TestSlackSend_BYOBotHasNoButtonsAndLinksToConsole(t *testing.T) {
	rt := &captureRT{resp: `{"ok":true,"ts":"1.2"}`}
	s := NewSlack(SlackConfig{}, nil).WithConsoleURL("https://app.example.com/")
	s.client = &http.Client{Transport: rt}

	if _, err := s.Send(context.Background(), byoJob(SlackConfig{BotToken: "xoxb-1", ChannelID: "C1"})); err != nil {
		t.Fatalf("send: %v", err)
	}
	body := decodedBody(t, rt.body)
	if strings.Contains(body, `"actions"`) || strings.Contains(body, "ack_alert") || strings.Contains(body, "resolve_alert") {
		t.Fatalf("BYO Slack message still has action buttons: %s", body)
	}
	if !strings.Contains(body, "<https://app.example.com/alerts/alert-123|Open in YipYap>") {
		t.Fatalf("missing console link: %s", body)
	}
}

func TestDiscordSend_BYOBotHasNoButtonsAndLinksToConsole(t *testing.T) {
	rt := &captureRT{resp: `{"id":"m1"}`}
	d := NewDiscord(DiscordConfig{}, nil).WithConsoleURL("https://app.example.com")
	d.client = &http.Client{Transport: rt}

	if _, err := d.Send(context.Background(), byoJob(DiscordConfig{BotToken: "tok", ChannelID: "42"})); err != nil {
		t.Fatalf("send: %v", err)
	}
	body := decodedBody(t, rt.body)
	if strings.Contains(body, `"components"`) || strings.Contains(body, "ack:") {
		t.Fatalf("BYO Discord message still has buttons: %s", body)
	}
	if !strings.Contains(body, "[Open in YipYap](https://app.example.com/alerts/alert-123)") {
		t.Fatalf("missing console link: %s", body)
	}
}

func TestAlertConsoleLink(t *testing.T) {
	cases := []struct {
		name, base string
		job        domain.NotificationJob
		want       string
	}{
		{"alert", "https://x", domain.NotificationJob{AlertID: "a1"}, "https://x/alerts/a1"},
		{"escaped", "https://x", domain.NotificationJob{AlertID: "a/1"}, "https://x/alerts/a%2F1"},
		{"test alert", "https://x", domain.NotificationJob{AlertID: "test-abc"}, "https://x/alerts"},
		{"no base uses ack url", "", domain.NotificationJob{AlertID: "a1", AckURL: "https://ack"}, "https://ack"},
		{"nothing", "", domain.NotificationJob{AlertID: "a1"}, ""},
	}
	for _, tc := range cases {
		if got := alertConsoleLink(tc.base, tc.job); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestSlackBlocks_NoLinkWithoutConsoleURL(t *testing.T) {
	blocks := slackMessageBlocks(domain.NotificationJob{AlertID: "a1"}, "")
	raw, _ := json.Marshal(blocks)
	if strings.Contains(string(raw), "Open in YipYap") || strings.Contains(string(raw), `"actions"`) {
		t.Fatalf("unexpected blocks: %s", raw)
	}
}
