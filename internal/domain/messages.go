package domain

import (
	"encoding/json"
	"time"
)

// CheckRequest is published by the web service to request a health check.
// Contains full config so checkers don't need database access.
type CheckRequest struct {
	MonitorID          string          `json:"monitor_id"`
	OrgID              string          `json:"org_id"`
	Name               string          `json:"name"`
	Type               MonitorType     `json:"type"`
	Config             json.RawMessage `json:"config"`
	IntervalSeconds    int             `json:"interval_seconds"`
	TimeoutSeconds     int             `json:"timeout_seconds"`
	LatencyWarningMS   int             `json:"latency_warning_ms,omitempty"`
	LatencyCriticalMS  int             `json:"latency_critical_ms,omitempty"`
	DownSeverity       Severity        `json:"down_severity,omitempty"`
	DegradedSeverity   Severity        `json:"degraded_severity,omitempty"`
	EscalationPolicyID string          `json:"escalation_policy_id,omitempty"`
}

// CheckResult is published by checkers after executing a health check.
// The web service consumes this to write to the database.
type CheckResult struct {
	MonitorID  string      `json:"monitor_id"`
	OrgID      string      `json:"org_id"`
	Type       MonitorType `json:"type,omitempty"`
	Status     CheckStatus `json:"status"`
	LatencyMS  int         `json:"latency_ms"`
	StatusCode int         `json:"status_code,omitempty"`
	Error      string      `json:"error,omitempty"`
	Metadata   string      `json:"metadata,omitempty"`
	TLSExpiry  *time.Time  `json:"tls_expiry_at,omitempty"`
	CheckedAt  time.Time   `json:"checked_at"`
}
