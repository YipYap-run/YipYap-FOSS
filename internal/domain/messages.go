package domain

import (
	"encoding/json"
	"time"
)

// CheckRequest is published by the web service to request a health check.
// Contains full config so checkers don't need database access.
type CheckRequest struct {
	MonitorID          string              `json:"monitor_id"`
	OrgID              string              `json:"org_id"`
	Name               string              `json:"name"`
	Type               MonitorType         `json:"type"`
	Config             json.RawMessage     `json:"config"`
	IntervalSeconds    int                 `json:"interval_seconds"`
	TimeoutSeconds     int                 `json:"timeout_seconds"`
	LatencyWarningMS   int                 `json:"latency_warning_ms,omitempty"`
	LatencyCriticalMS  int                 `json:"latency_critical_ms,omitempty"`
	DownSeverity       Severity            `json:"down_severity,omitempty"`
	DegradedSeverity   Severity            `json:"degraded_severity,omitempty"`
	EscalationPolicyID string              `json:"escalation_policy_id,omitempty"`
	MatchRules         []MonitorMatchRule  `json:"match_rules,omitempty"`
	// IssuedAt is when the scheduler published the request. Checkers skip
	// requests older than StaleAfter so a backlog built up during an outage
	// is not replayed against customer endpoints. Zero means the publisher
	// predates the field; such requests always run. (core-11)
	IssuedAt time.Time `json:"issued_at,omitzero"`
}

// MinCheckRequestStaleAfter is the floor for StaleAfter. It absorbs clock
// skew between the web and checker hosts and normal queueing delay, so short
// interval monitors are not skipped while the checkers are healthy.
const MinCheckRequestStaleAfter = 30 * time.Second

// StaleAfter is how old a request may be before a checker skips it: one
// monitor interval, but never less than MinCheckRequestStaleAfter. A newer
// request for the same monitor is due by then, so skipping loses nothing.
func (r CheckRequest) StaleAfter() time.Duration {
	d := time.Duration(r.IntervalSeconds) * time.Second
	if d < MinCheckRequestStaleAfter {
		d = MinCheckRequestStaleAfter
	}
	return d
}

// Stale reports whether the request is older than StaleAfter at now.
// Requests without IssuedAt are never stale, so checkers keep running
// requests from publishers deployed before the field existed.
func (r CheckRequest) Stale(now time.Time) bool {
	if r.IssuedAt.IsZero() {
		return false
	}
	return now.Sub(r.IssuedAt) > r.StaleAfter()
}

// CheckResult is published by checkers after executing a health check.
// The web service consumes this to write to the database.
type CheckResult struct {
	MonitorID      string      `json:"monitor_id"`
	OrgID          string      `json:"org_id"`
	Type           MonitorType `json:"type,omitempty"`
	Status         CheckStatus `json:"status"`
	LatencyMS      int         `json:"latency_ms"`
	StatusCode     int         `json:"status_code,omitempty"`
	Error          string      `json:"error,omitempty"`
	Metadata       string      `json:"metadata,omitempty"`
	TLSExpiry      *time.Time  `json:"tls_expiry_at,omitempty"`
	CheckedAt      time.Time   `json:"checked_at"`
	MatchedStateID string      `json:"matched_state_id,omitempty"`
}
