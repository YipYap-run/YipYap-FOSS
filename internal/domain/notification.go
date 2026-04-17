package domain

import "encoding/json"

type NotificationChannel struct {
	ID      string `json:"id"`
	OrgID   string `json:"org_id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Config  string `json:"-"`
	Enabled bool   `json:"enabled"`
}

type NotificationJob struct {
	ID           string `json:"id"`
	AlertID      string `json:"alert_id"`
	OrgID        string `json:"org_id"`
	MonitorName  string `json:"monitor_name"`
	Severity     string `json:"severity"`
	Channel      string `json:"channel"`
	TargetConfig string `json:"target_config"`
	Message      string `json:"message"`
	AckURL       string `json:"ack_url"`
	DedupeKey        string          `json:"dedupe_key"`
	RunbookURL       string          `json:"runbook_url,omitempty"`
	ServiceName      string          `json:"service_name,omitempty"`
	ServiceNotes     string          `json:"service_notes,omitempty"`
	ContextLinks     json.RawMessage `json:"context_links,omitempty"`
	DependencyStatus json.RawMessage `json:"dependency_status,omitempty"`
}

type AckEvent struct {
	AlertID string `json:"alert_id"`
	UserID  string `json:"user_id"`
	Channel string `json:"channel"`
}
