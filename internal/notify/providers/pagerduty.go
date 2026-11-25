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

const pagerDutyEventsURL = "https://events.pagerduty.com/v2/enqueue"

type PagerDutyConfig struct {
	RoutingKey string `json:"routing_key"`
}

type PagerDuty struct {
	fallback PagerDutyConfig
	decrypt  notify.DecryptFunc
	client   *http.Client
	apiURL   string
}

func NewPagerDuty(fallback PagerDutyConfig, decrypt notify.DecryptFunc) *PagerDuty {
	return &PagerDuty{
		fallback: fallback,
		decrypt:  decrypt,
		client:   newSSRFGuardedHTTPClient(30 * time.Second),
		apiURL:   pagerDutyEventsURL,
	}
}

func (p *PagerDuty) Channel() string     { return "pagerduty" }
func (p *PagerDuty) MaxConcurrency() int { return 10 }

func pagerDutySeverity(severity string) string {
	switch strings.ToLower(severity) {
	case "critical":
		return "critical"
	case "warning":
		return "warning"
	case "info":
		return "info"
	default:
		return "error"
	}
}

type pagerDutyLink struct {
	Href string `json:"href"`
	Text string `json:"text"`
}

type pagerDutyPayload struct {
	Summary       string            `json:"summary"`
	Source        string            `json:"source"`
	Severity      string            `json:"severity"`
	Timestamp     string            `json:"timestamp,omitempty"`
	CustomDetails map[string]string `json:"custom_details,omitempty"`
}

type pagerDutyEvent struct {
	RoutingKey  string            `json:"routing_key"`
	EventAction string            `json:"event_action"`
	DedupKey    string            `json:"dedup_key,omitempty"`
	Payload     *pagerDutyPayload `json:"payload,omitempty"`
	Links       []pagerDutyLink   `json:"links,omitempty"`
}

func buildPagerDutyEvent(cfg PagerDutyConfig, job domain.NotificationJob) pagerDutyEvent {
	dedupKey := job.DedupeKey
	if dedupKey == "" {
		dedupKey = job.AlertID
	}
	evt := pagerDutyEvent{
		RoutingKey:  cfg.RoutingKey,
		EventAction: domain.EventActionTrigger,
		DedupKey:    dedupKey,
	}
	if job.EventAction == domain.EventActionResolve {
		evt.EventAction = domain.EventActionResolve
		return evt
	}

	details := map[string]string{}
	if job.AlertID != "" {
		details["alert_id"] = job.AlertID
	}
	if job.ServiceName != "" {
		details["service"] = job.ServiceName
	}
	if job.RunbookURL != "" {
		details["runbook_url"] = job.RunbookURL
	}

	evt.Payload = &pagerDutyPayload{
		Summary:       fmt.Sprintf("[%s] %s: %s", job.Severity, job.MonitorName, job.Message),
		Source:        "yipyap",
		Severity:      pagerDutySeverity(job.Severity),
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		CustomDetails: details,
	}
	for _, href := range []string{job.RunbookURL, job.AckURL} {
		if href == "" {
			continue
		}
		text := "View in YipYap"
		if href == job.RunbookURL {
			text = "Runbook"
		}
		evt.Links = append(evt.Links, pagerDutyLink{Href: href, Text: text})
	}
	return evt
}

type pagerDutyResponse struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	DedupKey string `json:"dedup_key"`
}

func (p *PagerDuty) Send(ctx context.Context, job domain.NotificationJob) (string, error) {
	cfg := p.fallback
	if err := resolveConfig(job, p.decrypt, &cfg); err != nil {
		return "", fmt.Errorf("resolve pagerduty config: %w", err)
	}
	if cfg.RoutingKey == "" {
		return "", fmt.Errorf("pagerduty routing key is not configured")
	}

	evt := buildPagerDutyEvent(cfg, job)
	body, err := json.Marshal(evt)
	if err != nil {
		return "", fmt.Errorf("marshal pagerduty event: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.apiURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build pagerduty request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("pagerduty POST: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", fmt.Errorf("read pagerduty response: %w", err)
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("pagerduty returned status %d: %s", resp.StatusCode, respBody)
	}

	var result pagerDutyResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse pagerduty response: %w", err)
	}
	if result.Status != "success" {
		return "", fmt.Errorf("pagerduty error: %s", string(respBody))
	}
	return result.DedupKey, nil
}
