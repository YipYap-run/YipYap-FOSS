package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
)

// WebhookConfig is parsed from NotificationChannel.Config.
type WebhookConfig struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Format  string            `json:"format,omitempty"`
}

// Webhook delivers notifications via HTTP POST to a configured URL.
type Webhook struct {
	fallback WebhookConfig
	decrypt  notify.DecryptFunc
	client   *http.Client
}

// NewWebhook creates a webhook notifier from the given config.
func NewWebhook(fallback WebhookConfig, decrypt notify.DecryptFunc) *Webhook {
	return &Webhook{
		fallback: fallback,
		decrypt:  decrypt,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (w *Webhook) Channel() string    { return "webhook" }
func (w *Webhook) MaxConcurrency() int { return 10 }

// webhookPayload is the JSON body sent to generic webhook endpoints.
type webhookPayload struct {
	JobID            string          `json:"job_id"`
	AlertID          string          `json:"alert_id"`
	OrgID            string          `json:"org_id"`
	MonitorName      string          `json:"monitor_name"`
	Severity         string          `json:"severity"`
	Message          string          `json:"message"`
	AckURL           string          `json:"ack_url"`
	RunbookURL       string          `json:"runbook_url,omitempty"`
	ServiceName      string          `json:"service_name,omitempty"`
	ContextLinks     json.RawMessage `json:"context_links,omitempty"`
	DependencyStatus json.RawMessage `json:"dependency_status,omitempty"`
}

// resolveFormat returns the effective payload format for a webhook config.
// An explicit non-empty, non-"generic" format takes priority; otherwise the
// URL is inspected to auto-detect the destination service.
func resolveFormat(cfg WebhookConfig) string {
	if cfg.Format != "" && cfg.Format != "generic" {
		return cfg.Format
	}
	// If format is explicitly "generic", skip auto-detection.
	if cfg.Format == "generic" {
		return "generic"
	}
	u := cfg.URL
	switch {
	case strings.Contains(u, "discord.com/api/webhooks/") || strings.Contains(u, "discordapp.com/api/webhooks/"):
		return "discord"
	case strings.Contains(u, "hooks.slack.com/"):
		return "slack"
	case strings.Contains(u, "webhook.office.com") || strings.Contains(u, ".logic.azure.com"):
		return "teams"
	case strings.Contains(u, "chat.googleapis.com"):
		return "google_chat"
	case strings.Contains(u, "open.feishu.cn") || strings.Contains(u, "open.larksuite.com"):
		return "lark"
	case strings.Contains(u, "oapi.dingtalk.com"):
		return "dingtalk"
	case strings.Contains(u, "SYNO.Chat.External"):
		return "synology"
	default:
		return "generic"
	}
}

// formatMessage builds a human-readable notification string suitable for
// plain-text webhook formats (Slack, Discord, etc.).
func formatMessage(job domain.NotificationJob) string {
	msg := fmt.Sprintf("[%s] %s\n%s", job.Severity, job.MonitorName, job.Message)
	if job.ServiceName != "" {
		msg += fmt.Sprintf("\nService: %s", job.ServiceName)
	}
	if job.RunbookURL != "" {
		msg += fmt.Sprintf("\nRunbook: %s", job.RunbookURL)
	}
	return msg
}

// buildGenericPayload returns the full YipYap webhook payload.
func buildGenericPayload(job domain.NotificationJob) ([]byte, error) {
	payload := webhookPayload{
		JobID:            job.ID,
		AlertID:          job.AlertID,
		OrgID:            job.OrgID,
		MonitorName:      job.MonitorName,
		Severity:         job.Severity,
		Message:          job.Message,
		AckURL:           job.AckURL,
		RunbookURL:       job.RunbookURL,
		ServiceName:      job.ServiceName,
		ContextLinks:     job.ContextLinks,
		DependencyStatus: job.DependencyStatus,
	}
	return json.Marshal(payload)
}

// buildSlackPayload returns a Slack-compatible incoming webhook payload.
func buildSlackPayload(job domain.NotificationJob) ([]byte, error) {
	return json.Marshal(map[string]string{"text": formatMessage(job)})
}

// buildDiscordPayload returns a Discord webhook payload.
func buildDiscordPayload(job domain.NotificationJob) ([]byte, error) {
	return json.Marshal(map[string]string{"content": formatMessage(job)})
}

// buildTeamsPayload returns a Microsoft Teams Adaptive Card payload.
func buildTeamsPayload(job domain.NotificationJob) ([]byte, error) {
	title := fmt.Sprintf("[%s] %s", job.Severity, job.MonitorName)
	card := map[string]any{
		"type": "message",
		"attachments": []map[string]any{
			{
				"contentType": "application/vnd.microsoft.card.adaptive",
				"content": map[string]any{
					"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
					"type":    "AdaptiveCard",
					"version": "1.2",
					"body": []map[string]any{
						{
							"type":   "TextBlock",
							"text":   title,
							"weight": "Bolder",
							"size":   "Medium",
						},
						{
							"type": "TextBlock",
							"text": job.Message,
							"wrap": true,
						},
					},
				},
			},
		},
	}
	return json.Marshal(card)
}

// buildGoogleChatPayload returns a Google Chat webhook payload.
func buildGoogleChatPayload(job domain.NotificationJob) ([]byte, error) {
	return json.Marshal(map[string]string{"text": formatMessage(job)})
}

// buildLarkPayload returns a Lark/Feishu webhook payload.
func buildLarkPayload(job domain.NotificationJob) ([]byte, error) {
	return json.Marshal(map[string]any{
		"msg_type": "text",
		"content":  map[string]string{"text": formatMessage(job)},
	})
}

// buildDingTalkPayload returns a DingTalk webhook payload.
func buildDingTalkPayload(job domain.NotificationJob) ([]byte, error) {
	return json.Marshal(map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": formatMessage(job)},
	})
}

// buildSynologyPayload returns a Synology Chat webhook payload.
func buildSynologyPayload(job domain.NotificationJob) ([]byte, error) {
	return json.Marshal(map[string]string{"text": formatMessage(job)})
}

func (w *Webhook) Send(ctx context.Context, job domain.NotificationJob) (string, error) {
	cfg := w.fallback
	if err := resolveConfig(job, w.decrypt, &cfg); err != nil {
		return "", fmt.Errorf("resolve webhook config: %w", err)
	}

	format := resolveFormat(cfg)

	var (
		body []byte
		err  error
	)
	switch format {
	case "slack":
		body, err = buildSlackPayload(job)
	case "discord":
		body, err = buildDiscordPayload(job)
	case "teams":
		body, err = buildTeamsPayload(job)
	case "google_chat":
		body, err = buildGoogleChatPayload(job)
	case "lark":
		body, err = buildLarkPayload(job)
	case "dingtalk":
		body, err = buildDingTalkPayload(job)
	case "synology":
		body, err = buildSynologyPayload(job)
	default:
		body, err = buildGenericPayload(job)
	}
	if err != nil {
		return "", fmt.Errorf("marshal webhook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("webhook POST: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("webhook returned status %d: %s", resp.StatusCode, respBody)
	}
	return fmt.Sprintf("webhook:%d", resp.StatusCode), nil
}
