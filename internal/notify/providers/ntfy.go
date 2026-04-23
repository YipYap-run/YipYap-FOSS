package providers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
)

// NtfyConfig is parsed from NotificationChannel.Config.
type NtfyConfig struct {
	ServerURL string `json:"server_url"` // e.g., "https://ntfy.sh" or self-hosted
	Topic     string `json:"topic"`      // the ntfy topic name
	Token     string `json:"token"`      // optional auth token for private topics
}

// Ntfy delivers notifications via ntfy.sh push notifications.
type Ntfy struct {
	fallback NtfyConfig
	decrypt  notify.DecryptFunc
	client   *http.Client
}

// NewNtfy creates an ntfy notifier from the given config.
func NewNtfy(fallback NtfyConfig, decrypt notify.DecryptFunc) *Ntfy {
	return &Ntfy{
		fallback: fallback,
		decrypt:  decrypt,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (n *Ntfy) Channel() string    { return "ntfy" }
func (n *Ntfy) MaxConcurrency() int { return 10 }

// ntfyPriority maps alert severity to ntfy priority values.
func ntfyPriority(severity string) string {
	switch strings.ToLower(severity) {
	case "critical":
		return "5"
	case "warning":
		return "4"
	default:
		return "3"
	}
}

// ntfyTags maps alert severity to ntfy emoji tag names.
func ntfyTags(severity string) string {
	switch strings.ToLower(severity) {
	case "critical":
		return "rotating_light"
	case "warning":
		return "warning"
	default:
		return "information_source"
	}
}

func (n *Ntfy) Send(ctx context.Context, job domain.NotificationJob) (string, error) {
	cfg := n.fallback
	if err := resolveConfig(job, n.decrypt, &cfg); err != nil {
		return "", fmt.Errorf("resolve ntfy config: %w", err)
	}

	serverURL := cfg.ServerURL
	if serverURL == "" {
		serverURL = "https://ntfy.sh"
	}
	url := strings.TrimRight(serverURL, "/") + "/" + cfg.Topic

	body := job.Message
	if job.RunbookURL != "" {
		body += "\n\nRunbook: " + job.RunbookURL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build ntfy request: %w", err)
	}

	req.Header.Set("X-Title", "YipYap Alert: "+job.MonitorName)
	req.Header.Set("X-Priority", ntfyPriority(job.Severity))
	req.Header.Set("X-Tags", ntfyTags(job.Severity))

	if job.RunbookURL != "" {
		req.Header.Set("X-Click", job.RunbookURL)
	}

	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}

	resp, err := n.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ntfy POST: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("ntfy returned status %d", resp.StatusCode)
	}
	return fmt.Sprintf("ntfy:%d", resp.StatusCode), nil
}
