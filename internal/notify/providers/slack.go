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

// SlackConfig holds Slack bot settings.
type SlackConfig struct {
	BotToken  string `json:"bot_token"`
	ChannelID string `json:"channel_id"`
}

// Slack delivers notifications via the Slack chat.postMessage API.
type Slack struct {
	fallback SlackConfig
	decrypt  notify.DecryptFunc
	client   *http.Client
}

// NewSlack creates a Slack notifier.
func NewSlack(fallback SlackConfig, decrypt notify.DecryptFunc) *Slack {
	return &Slack{
		fallback: fallback,
		decrypt:  decrypt,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *Slack) Channel() string    { return "slack" }
func (s *Slack) MaxConcurrency() int { return 5 }

func (s *Slack) Send(ctx context.Context, job domain.NotificationJob) (string, error) {
	cfg := s.fallback
	if err := resolveConfig(job, s.decrypt, &cfg); err != nil {
		return "", fmt.Errorf("resolve slack config: %w", err)
	}

	const url = "https://slack.com/api/chat.postMessage"

	blocks := []map[string]any{
		{
			"type": "section",
			"text": map[string]string{
				"type": "mrkdwn",
				"text": fmt.Sprintf("*[%s] %s*\n%s", job.Severity, job.MonitorName, job.Message),
			},
		},
	}

	// Runbook link
	if job.RunbookURL != "" {
		blocks = append(blocks, map[string]any{
			"type": "context",
			"elements": []map[string]any{
				{"type": "mrkdwn", "text": fmt.Sprintf(":clipboard: <%s|Runbook>", job.RunbookURL)},
			},
		})
	}

	// Service context
	if job.ServiceName != "" {
		var contextParts []string
		contextParts = append(contextParts, fmt.Sprintf("*Service:* %s", job.ServiceName))

		// Dependency status
		if len(job.DependencyStatus) > 0 {
			var deps []struct {
				ServiceName  string `json:"service_name"`
				Relationship string `json:"relationship"`
				IsAlerting   bool   `json:"is_alerting"`
			}
			if err := json.Unmarshal(job.DependencyStatus, &deps); err == nil && len(deps) > 0 {
				var depLines []string
				for _, dep := range deps {
					icon := ":white_check_mark:"
					if dep.IsAlerting {
						icon = ":red_circle:"
					}
					depLines = append(depLines, fmt.Sprintf("%s %s (%s)", icon, dep.ServiceName, dep.Relationship))
				}
				contextParts = append(contextParts, "*Dependencies:*\n"+strings.Join(depLines, "\n"))
			}
		}

		// Context links
		if len(job.ContextLinks) > 0 {
			var links []struct {
				Label string `json:"label"`
				URL   string `json:"url"`
			}
			if err := json.Unmarshal(job.ContextLinks, &links); err == nil && len(links) > 0 {
				var linkParts []string
				for _, link := range links {
					linkParts = append(linkParts, fmt.Sprintf("<%s|%s>", link.URL, link.Label))
				}
				contextParts = append(contextParts, "*Quick Links:* "+strings.Join(linkParts, " | "))
			}
		}

		blocks = append(blocks, map[string]any{
			"type": "section",
			"text": map[string]string{"type": "mrkdwn", "text": strings.Join(contextParts, "\n")},
		})
	}

	blocks = append(blocks, map[string]any{
		"type": "actions",
		"elements": []map[string]any{
			{
				"type":      "button",
				"text":      map[string]string{"type": "plain_text", "text": "Acknowledge"},
				"action_id": "ack_alert",
				"value":     job.AlertID,
				"style":     "primary",
			},
			{
				"type":      "button",
				"text":      map[string]string{"type": "plain_text", "text": "Resolve"},
				"action_id": "resolve_alert",
				"value":     job.AlertID,
				"style":     "danger",
			},
		},
	})

	payload := map[string]any{
		"channel": cfg.ChannelID,
		"blocks":  blocks,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal slack payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build slack request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+cfg.BotToken)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("slack POST: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		TS    string `json:"ts"`
	}
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decode slack response: %w", err)
	}
	if !result.OK {
		return "", fmt.Errorf("slack API error: %s", result.Error)
	}
	return result.TS, nil
}

// slackInteractionPayload represents a Slack block_actions interaction callback.
type slackInteractionPayload struct {
	Type    string `json:"type"`
	User    struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"user"`
	Actions []struct {
		ActionID string `json:"action_id"`
		Value    string `json:"value"`
	} `json:"actions"`
}

// ParseAck implements notify.AckListener. It extracts an AckEvent from a Slack
// interactivity callback (form-encoded POST with a "payload" JSON field).
func (s *Slack) ParseAck(r *http.Request) (*domain.AckEvent, error) {
	payloadStr := r.FormValue("payload")
	if payloadStr == "" {
		return nil, fmt.Errorf("missing payload field")
	}

	var payload slackInteractionPayload
	if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
		return nil, fmt.Errorf("unmarshal slack payload: %w", err)
	}

	if len(payload.Actions) == 0 {
		return nil, fmt.Errorf("no actions in payload")
	}

	action := payload.Actions[0]
	channel := "slack"
	if action.ActionID == "resolve_alert" {
		channel = "slack:resolve"
	}

	return &domain.AckEvent{
		AlertID: action.Value,
		UserID:  payload.User.ID,
		Channel: channel,
	}, nil
}
