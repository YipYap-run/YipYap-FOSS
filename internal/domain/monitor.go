package domain

import (
	"encoding/json"
	"time"
)

type MonitorType string

const (
	MonitorHTTP      MonitorType = "http"
	MonitorTCP       MonitorType = "tcp"
	MonitorPing      MonitorType = "ping"
	MonitorDNS       MonitorType = "dns"
	MonitorHeartbeat MonitorType = "heartbeat"
)

type CheckStatus string

const (
	StatusUp       CheckStatus = "up"
	StatusDown     CheckStatus = "down"
	StatusDegraded CheckStatus = "degraded"
)

type Monitor struct {
	ID                 string          `json:"id"`
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
	Regions            []string        `json:"regions,omitempty"`
	EscalationPolicyID string          `json:"escalation_policy_id,omitempty"`
	HeartbeatToken     string          `json:"heartbeat_token,omitempty"`
	IntegrationKey     string          `json:"integration_key,omitempty"`
	RunbookURL         string          `json:"runbook_url,omitempty"`
	ServiceID          string          `json:"service_id,omitempty"`
	Enabled            bool            `json:"enabled"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

type MonitorLabel struct {
	MonitorID string `json:"monitor_id"`
	Key       string `json:"key"`
	Value     string `json:"value"`
}

type MonitorCheck struct {
	ID         string      `json:"id"`
	MonitorID  string      `json:"monitor_id"`
	Status     CheckStatus `json:"status"`
	LatencyMS  int         `json:"latency_ms"`
	StatusCode int         `json:"status_code,omitempty"`
	Error      string      `json:"error,omitempty"`
	Metadata   string      `json:"metadata,omitempty"`
	TLSExpiry  *time.Time  `json:"tls_expiry_at,omitempty"`
	CheckedAt  time.Time   `json:"checked_at"`
}

type MonitorRollup struct {
	MonitorID    string    `json:"monitor_id"`
	Period       string    `json:"period"`
	PeriodStart  time.Time `json:"period_start"`
	UptimePct    float64   `json:"uptime_pct"`
	AvgLatencyMS float64   `json:"avg_latency_ms"`
	P95LatencyMS float64   `json:"p95_latency_ms"`
	P99LatencyMS float64   `json:"p99_latency_ms"`
	CheckCount   int       `json:"check_count"`
	FailureCount int       `json:"failure_count"`
}

type HTTPCheckConfig struct {
	Method         string            `json:"method"`
	URL            string            `json:"url"`
	Headers        map[string]string `json:"headers,omitempty"`
	Body           string            `json:"body,omitempty"`
	ExpectedStatus int               `json:"expected_status"`
	BodyMatch      string            `json:"body_match,omitempty"`
}

type TCPCheckConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type PingCheckConfig struct {
	Host string `json:"host"`
}

type DNSCheckConfig struct {
	Hostname   string `json:"hostname"`
	RecordType string `json:"record_type"`
	Expected   string `json:"expected,omitempty"`
	Nameserver string `json:"nameserver,omitempty"`
}

type HeartbeatCheckConfig struct {
	GracePeriodSeconds int `json:"grace_period_seconds"`
}
