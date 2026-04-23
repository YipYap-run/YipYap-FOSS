package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
)

const pushoverAPI = "https://api.pushover.net/1/messages.json"

// PushoverConfig is parsed from NotificationChannel.Config.
type PushoverConfig struct {
	APIToken string `json:"api_token"` // Pushover application API token
	UserKey  string `json:"user_key"`  // Pushover user/group key
	Device   string `json:"device"`    // Optional: specific device name
	Sound    string `json:"sound"`     // Optional: notification sound
}

// Pushover delivers notifications via the Pushover push notification service.
type Pushover struct {
	fallback PushoverConfig
	decrypt  notify.DecryptFunc
	client   *http.Client
	apiURL   string // overridable for tests
}

// NewPushover creates a Pushover notifier from the given config.
func NewPushover(fallback PushoverConfig, decrypt notify.DecryptFunc) *Pushover {
	return &Pushover{
		fallback: fallback,
		decrypt:  decrypt,
		client:   &http.Client{Timeout: 30 * time.Second},
		apiURL:   pushoverAPI,
	}
}

func (p *Pushover) Channel() string    { return "pushover" }
func (p *Pushover) MaxConcurrency() int { return 10 }

// pushoverResponse is the JSON response from the Pushover API.
type pushoverResponse struct {
	Status  int    `json:"status"`
	Request string `json:"request"`
}

// severityToPriority maps alert severity to Pushover priority values.
func severityToPriority(severity string) string {
	switch strings.ToLower(severity) {
	case "critical":
		return "2" // emergency
	case "warning":
		return "1" // high
	default:
		return "0" // normal
	}
}

func (p *Pushover) Send(ctx context.Context, job domain.NotificationJob) (string, error) {
	cfg := p.fallback
	if err := resolveConfig(job, p.decrypt, &cfg); err != nil {
		return "", fmt.Errorf("resolve pushover config: %w", err)
	}

	priority := severityToPriority(job.Severity)

	form := url.Values{}
	form.Set("token", cfg.APIToken)
	form.Set("user", cfg.UserKey)
	form.Set("title", "YipYap: "+job.MonitorName)
	form.Set("message", job.Message)
	form.Set("priority", priority)

	if cfg.Sound != "" {
		form.Set("sound", cfg.Sound)
	}
	if cfg.Device != "" {
		form.Set("device", cfg.Device)
	}
	if job.RunbookURL != "" {
		form.Set("url", job.RunbookURL)
		form.Set("url_title", "Runbook")
	} else if job.AckURL != "" {
		form.Set("url", job.AckURL)
		form.Set("url_title", "View in YipYap")
	}

	// Emergency priority requires retry and expire parameters.
	if priority == "2" {
		form.Set("retry", "60")
		form.Set("expire", "3600")
	}

	apiURL := p.apiURL
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build pushover request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("pushover POST: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read pushover response: %w", err)
	}

	var result pushoverResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse pushover response: %w", err)
	}

	if result.Status != 1 {
		return "", fmt.Errorf("pushover error (status %d): %s", result.Status, string(body))
	}

	return result.Request, nil
}
