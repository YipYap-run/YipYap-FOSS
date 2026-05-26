package domain

import (
	"encoding/json"
	"time"
)

type AlertStatus string

const (
	AlertFiring       AlertStatus = "firing"
	AlertAcknowledged AlertStatus = "acknowledged"
	AlertResolved     AlertStatus = "resolved"
)

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
	SeverityInfo     Severity = "info"
)

type Alert struct {
	ID                    string      `json:"id"`
	MonitorID             string      `json:"monitor_id"`
	OrgID                 string      `json:"org_id"`
	Status                AlertStatus `json:"status"`
	Severity              Severity    `json:"severity"`
	Error                 string      `json:"error,omitempty"`
	StartedAt             time.Time   `json:"started_at"`
	AcknowledgedAt        *time.Time  `json:"acknowledged_at,omitempty"`
	AcknowledgedBy        string      `json:"acknowledged_by,omitempty"`
	ResolvedAt            *time.Time  `json:"resolved_at,omitempty"`
	CurrentEscalationStep string      `json:"current_escalation_step,omitempty"`
	IncidentID            string      `json:"incident_id,omitempty"`
}

type AlertEventType string

const (
	EventTriggered AlertEventType = "triggered"
	EventNotified  AlertEventType = "notified"
	EventAck       AlertEventType = "ack"
	EventEscalated AlertEventType = "escalated"
	EventResolved  AlertEventType = "resolved"

	// Read-only timeline annotations written by Phase-2 reply handlers.
	// These entries never mutate alert state, they exist so the UI can
	// render a timeline pill showing what a downstream consumer reported.
	EventReplyClaimed  AlertEventType = "reply.claimed"
	EventReplyEnriched AlertEventType = "reply.enriched"
	EventReplyLinked   AlertEventType = "reply.linked"

	// Phase-5 mutating-reply timeline events. Each of these accompanies the
	// corresponding state mutation (MarkDuplicate, SetOwner, AckWithContext,
	// or the side-table writes in the remediation/routing/status_page/
	// suppression handlers) so the alert detail page can render a single
	// ordered timeline.
	EventDuplicated           AlertEventType = "duplicated"
	EventOwnershipTransferred AlertEventType = "ownership_transferred"
	EventAckWithContext       AlertEventType = "ack_with_context"
	EventSuppressed           AlertEventType = "suppressed"
	EventEscalatedReply       AlertEventType = "escalated_reply"
	EventRemediationStarted   AlertEventType = "remediation_started"
	EventRemediationResult    AlertEventType = "remediation_result"
	EventRouted               AlertEventType = "routed"
	EventStatusPageSynced     AlertEventType = "status_page_synced"
)

type AlertEvent struct {
	ID           string          `json:"id"`
	AlertID      string          `json:"alert_id"`
	EventType    AlertEventType  `json:"event_type"`
	Channel      string          `json:"channel,omitempty"`
	TargetUserID string          `json:"target_user_id,omitempty"`
	Detail       json.RawMessage `json:"detail,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

type AlertEscalationState struct {
	AlertID           string          `json:"alert_id"`
	CurrentStepID     string          `json:"current_step_id"`
	StepEnteredAt     time.Time       `json:"step_entered_at"`
	RetryCount        int             `json:"retry_count"`
	LastNotifiedAt    *time.Time      `json:"last_notified_at,omitempty"`
	NotificationsSent json.RawMessage `json:"notifications_sent,omitempty"`
}
