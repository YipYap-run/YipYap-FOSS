package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func TestWebhook_Send(t *testing.T) {
	var received webhookPayload
	var gotHeaders http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			http.Error(w, "bad json", 400)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	wh := NewWebhook(WebhookConfig{
		URL:     srv.URL,
		Headers: map[string]string{"X-Custom": "test-value"},
	}, nil)

	job := domain.NotificationJob{
		ID:          "j1",
		AlertID:     "a1",
		OrgID:       "org1",
		MonitorName: "web-check",
		Severity:    "critical",
		Message:     "host is down",
		AckURL:      "https://example.com/ack/a1",
	}

	msgID, err := wh.Send(context.Background(), job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msgID == "" {
		t.Fatal("expected non-empty message ID")
	}

	// Verify payload.
	if received.JobID != "j1" {
		t.Errorf("expected job_id j1, got %s", received.JobID)
	}
	if received.AlertID != "a1" {
		t.Errorf("expected alert_id a1, got %s", received.AlertID)
	}
	if received.MonitorName != "web-check" {
		t.Errorf("expected monitor_name web-check, got %s", received.MonitorName)
	}
	if received.Severity != "critical" {
		t.Errorf("expected severity critical, got %s", received.Severity)
	}
	if received.Message != "host is down" {
		t.Errorf("expected message 'host is down', got %s", received.Message)
	}
	if received.AckURL != "https://example.com/ack/a1" {
		t.Errorf("expected ack_url, got %s", received.AckURL)
	}

	// Verify custom header.
	if gotHeaders.Get("X-Custom") != "test-value" {
		t.Errorf("expected X-Custom header, got %s", gotHeaders.Get("X-Custom"))
	}
	if gotHeaders.Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", gotHeaders.Get("Content-Type"))
	}
}

func TestWebhook_SendError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	wh := NewWebhook(WebhookConfig{URL: srv.URL}, nil)
	_, err := wh.Send(context.Background(), domain.NotificationJob{ID: "j2"})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestResolveFormat(t *testing.T) {
	tests := []struct {
		name   string
		cfg    WebhookConfig
		expect string
	}{
		// Explicit format overrides auto-detection.
		{
			name:   "explicit slack format",
			cfg:    WebhookConfig{URL: "https://discord.com/api/webhooks/123/abc", Format: "slack"},
			expect: "slack",
		},
		{
			name:   "explicit lark format on non-lark URL",
			cfg:    WebhookConfig{URL: "https://example.com/hook", Format: "lark"},
			expect: "lark",
		},
		// Explicit "generic" stays generic even on Discord URL.
		{
			name:   "explicit generic overrides discord URL",
			cfg:    WebhookConfig{URL: "https://discord.com/api/webhooks/123/abc", Format: "generic"},
			expect: "generic",
		},
		// Empty format falls back to auto-detect.
		{
			name:   "empty format auto-detects discord",
			cfg:    WebhookConfig{URL: "https://discord.com/api/webhooks/123/abc"},
			expect: "discord",
		},
		{
			name:   "empty format auto-detects discordapp",
			cfg:    WebhookConfig{URL: "https://discordapp.com/api/webhooks/123/abc"},
			expect: "discord",
		},
		{
			name:   "empty format auto-detects slack",
			cfg:    WebhookConfig{URL: "https://hooks.slack.com/services/T00/B00/xxx"},
			expect: "slack",
		},
		{
			name:   "empty format auto-detects teams via office.com",
			cfg:    WebhookConfig{URL: "https://xxx.webhook.office.com/webhook/aaa"},
			expect: "teams",
		},
		{
			name:   "empty format auto-detects teams via logic.azure.com",
			cfg:    WebhookConfig{URL: "https://prod-01.logic.azure.com:443/workflows/aaa"},
			expect: "teams",
		},
		{
			name:   "empty format auto-detects google_chat",
			cfg:    WebhookConfig{URL: "https://chat.googleapis.com/v1/spaces/XXX/messages"},
			expect: "google_chat",
		},
		{
			name:   "empty format auto-detects lark feishu",
			cfg:    WebhookConfig{URL: "https://open.feishu.cn/open-apis/bot/v2/hook/xxx"},
			expect: "lark",
		},
		{
			name:   "empty format auto-detects lark larksuite",
			cfg:    WebhookConfig{URL: "https://open.larksuite.com/open-apis/bot/v2/hook/xxx"},
			expect: "lark",
		},
		{
			name:   "empty format auto-detects dingtalk",
			cfg:    WebhookConfig{URL: "https://oapi.dingtalk.com/robot/send?access_token=xxx"},
			expect: "dingtalk",
		},
		{
			name:   "empty format auto-detects synology",
			cfg:    WebhookConfig{URL: "https://nas.example.com/webapi/SYNO.Chat.External/1/incoming"},
			expect: "synology",
		},
		// Unknown URL defaults to generic.
		{
			name:   "unknown URL defaults to generic",
			cfg:    WebhookConfig{URL: "https://example.com/myhook"},
			expect: "generic",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveFormat(tc.cfg)
			if got != tc.expect {
				t.Errorf("resolveFormat(%+v) = %q, want %q", tc.cfg, got, tc.expect)
			}
		})
	}
}

func testJob() domain.NotificationJob {
	return domain.NotificationJob{
		ID:          "j1",
		AlertID:     "a1",
		OrgID:       "org1",
		MonitorName: "API Check",
		Severity:    "critical",
		Message:     "host is down",
		AckURL:      "https://example.com/ack/a1",
	}
}

func TestWebhook_SendSlackFormat(t *testing.T) {
	var rawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	wh := NewWebhook(WebhookConfig{URL: srv.URL, Format: "slack"}, nil)
	_, err := wh.Send(context.Background(), testJob())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(rawBody, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := m["text"]; !ok {
		t.Error("expected 'text' field in Slack payload")
	}
	if _, ok := m["job_id"]; ok {
		t.Error("Slack payload should not contain 'job_id'")
	}
}

func TestWebhook_SendDiscordFormat(t *testing.T) {
	var rawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	wh := NewWebhook(WebhookConfig{URL: srv.URL, Format: "discord"}, nil)
	_, err := wh.Send(context.Background(), testJob())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(rawBody, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := m["content"]; !ok {
		t.Error("expected 'content' field in Discord payload")
	}
}

func TestWebhook_SendTeamsFormat(t *testing.T) {
	var rawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	wh := NewWebhook(WebhookConfig{URL: srv.URL, Format: "teams"}, nil)
	_, err := wh.Send(context.Background(), testJob())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(rawBody, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if typ, _ := m["type"].(string); typ != "message" {
		t.Errorf("expected type 'message', got %q", typ)
	}
	attachments, ok := m["attachments"].([]any)
	if !ok || len(attachments) == 0 {
		t.Fatal("expected non-empty 'attachments' array in Teams payload")
	}
	att, _ := attachments[0].(map[string]any)
	if ct, _ := att["contentType"].(string); ct != "application/vnd.microsoft.card.adaptive" {
		t.Errorf("expected Adaptive Card contentType, got %q", ct)
	}
	content, _ := att["content"].(map[string]any)
	if content["type"] != "AdaptiveCard" {
		t.Errorf("expected content.type 'AdaptiveCard', got %v", content["type"])
	}
}

func TestWebhook_SendLarkFormat(t *testing.T) {
	var rawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	wh := NewWebhook(WebhookConfig{URL: srv.URL, Format: "lark"}, nil)
	_, err := wh.Send(context.Background(), testJob())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(rawBody, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if msgType, _ := m["msg_type"].(string); msgType != "text" {
		t.Errorf("expected msg_type 'text', got %q", msgType)
	}
}

func TestWebhook_SendDingTalkFormat(t *testing.T) {
	var rawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	wh := NewWebhook(WebhookConfig{URL: srv.URL, Format: "dingtalk"}, nil)
	_, err := wh.Send(context.Background(), testJob())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(rawBody, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if msgType, _ := m["msgtype"].(string); msgType != "text" {
		t.Errorf("expected msgtype 'text', got %q", msgType)
	}
}
