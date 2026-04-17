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

// DiscordConfig holds Discord bot settings.
type DiscordConfig struct {
	BotToken  string `json:"bot_token"`
	ChannelID string `json:"channel_id"`
}

// Discord delivers notifications via the Discord bot API.
type Discord struct {
	fallback DiscordConfig
	decrypt  notify.DecryptFunc
	client   *http.Client
}

// NewDiscord creates a Discord notifier.
func NewDiscord(fallback DiscordConfig, decrypt notify.DecryptFunc) *Discord {
	return &Discord{
		fallback: fallback,
		decrypt:  decrypt,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (d *Discord) Channel() string    { return "discord" }
func (d *Discord) MaxConcurrency() int { return 5 }

func (d *Discord) Send(ctx context.Context, job domain.NotificationJob) (string, error) {
	cfg := d.fallback
	if err := resolveConfig(job, d.decrypt, &cfg); err != nil {
		return "", fmt.Errorf("resolve discord config: %w", err)
	}

	url := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages", cfg.ChannelID)

	content := fmt.Sprintf("**[%s] %s**\n%s", job.Severity, job.MonitorName, job.Message)
	if job.RunbookURL != "" {
		content += fmt.Sprintf("\n:clipboard: [Runbook](%s)", job.RunbookURL)
	}
	if job.ServiceName != "" {
		content += fmt.Sprintf("\n**Service:** %s", job.ServiceName)
	}
	if len(job.DependencyStatus) > 0 {
		var deps []struct {
			ServiceName  string `json:"service_name"`
			Relationship string `json:"relationship"`
			IsAlerting   bool   `json:"is_alerting"`
		}
		if err := json.Unmarshal(job.DependencyStatus, &deps); err == nil && len(deps) > 0 {
			content += "\n**Dependencies:**"
			for _, dep := range deps {
				icon := ":white_check_mark:"
				if dep.IsAlerting {
					icon = ":red_circle:"
				}
				content += fmt.Sprintf("\n%s %s (%s)", icon, dep.ServiceName, dep.Relationship)
			}
		}
	}
	if len(job.ContextLinks) > 0 {
		var links []struct {
			Label string `json:"label"`
			URL   string `json:"url"`
		}
		if err := json.Unmarshal(job.ContextLinks, &links); err == nil && len(links) > 0 {
			var linkParts []string
			for _, link := range links {
				linkParts = append(linkParts, fmt.Sprintf("[%s](%s)", link.Label, link.URL))
			}
			content += "\n**Quick Links:** " + strings.Join(linkParts, " | ")
		}
	}

	payload := map[string]any{
		"content": content,
		"components": []map[string]any{
			{
				"type": 1, // action row
				"components": []map[string]any{
					{
						"type":      2, // button
						"style":     1, // primary
						"label":     "Acknowledge",
						"custom_id": fmt.Sprintf("ack:%s", job.AlertID),
					},
					{
						"type":      2, // button
						"style":     4, // danger (red)
						"label":     "Resolve",
						"custom_id": fmt.Sprintf("resolve:%s", job.AlertID),
					},
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal discord payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build discord request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot "+cfg.BotToken)

	resp, err := d.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("discord POST: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("discord returned status %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	return result.ID, nil
}

// discordInteractionPayload represents a Discord interaction callback.
type discordInteractionPayload struct {
	Type   int `json:"type"`
	Member struct {
		User struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"user"`
	} `json:"member"`
	Data struct {
		CustomID      string `json:"custom_id"`
		ComponentType int    `json:"component_type"`
	} `json:"data"`
}

// ParseAck implements notify.AckListener. It extracts an AckEvent from a
// Discord interaction callback (JSON POST body with type 3 MESSAGE_COMPONENT).
func (d *Discord) ParseAck(r *http.Request) (*domain.AckEvent, error) {
	var payload discordInteractionPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode discord interaction: %w", err)
	}

	if payload.Data.CustomID == "" {
		return nil, fmt.Errorf("missing custom_id in interaction data")
	}

	parts := strings.SplitN(payload.Data.CustomID, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid custom_id format: %s", payload.Data.CustomID)
	}

	action := parts[0]
	alertID := parts[1]

	channel := "discord"
	if action == "resolve" {
		channel = "discord:resolve"
	}

	return &domain.AckEvent{
		AlertID: alertID,
		UserID:  payload.Member.User.ID,
		Channel: channel,
	}, nil
}
