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
	EventAck       AlertEventType = "ack"
	EventEscalated AlertEventType = "escalated"
	EventResolved  AlertEventType = "resolved"
	// EventNotifyFailed records that an escalation step could not page a
	// target (nobody on call, missing channel, queueing failed). Detail
	// carries {"reason": "...", "target_type": "...", "target_id": "..."}.
	EventNotifyFailed AlertEventType = "notify_failed"
	// EventEscalationStopped is written when a step marked "stop
	// escalation" has sent its pages. Nothing further is paged.
	EventEscalationStopped AlertEventType = "escalation_stopped"

	// EventNotified is the legacy pre-send event. It was written when a job
	// was enqueued, before any provider call, so it never meant delivery.
	// New code writes EventNotificationQueued instead; old rows keep this
	// type and stay readable.
	EventNotified AlertEventType = "notified"

	// Delivery events. Queued is written by the escalation engine when a
	// job is handed to the notifier. Sent is written after the provider
	// accepted the notification, and Failed after every retry failed. The
	// detail carries job_id, channel_id, channel_name and, for failures, a
	// sanitized reason.
	EventNotificationQueued AlertEventType = "notification_queued"
	EventNotificationSent   AlertEventType = "notification_sent"
	EventNotificationFailed AlertEventType = "notification_failed"

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

	// EventNotifySkipped records a channel the engine did not send to (for
	// example, an SMS or voice quota was reached). Detail carries the
	// reason and a human-readable message; escalation continues with the
	// step's other channels.
	EventNotifySkipped AlertEventType = "notify_skipped"
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

type PagedTargetKind string

const (
	PagedTargetMethod  PagedTargetKind = "method"
	PagedTargetChannel PagedTargetKind = "channel"
)

type AlertPagedTarget struct {
	AlertID   string
	TargetKey string
	Kind      PagedTargetKind
	UserID    string
	MethodID  string
	ChannelID string
	CreatedAt time.Time
}

const (
	EventActionTrigger = "trigger"
	EventActionResolve = "resolve"
)

// SubjectAlertChanged is a broadcast-only bus subject. The escalation engine
// and the alert API publish an AlertChanged on it whenever an alert opens,
// changes status, escalates or pages someone, and the WebSocket hub relays
// it so open dashboards and alert pages refresh. It is a hint: nothing
// durable consumes it, and a lost message only delays a refresh.
const SubjectAlertChanged = "alert.changed"

// AlertChanged is the payload on SubjectAlertChanged.
type AlertChanged struct {
	OrgID     string      `json:"org_id"`
	AlertID   string      `json:"alert_id"`
	MonitorID string      `json:"monitor_id"`
	Status    AlertStatus `json:"status"`
}

// NewAlertChanged builds the AlertChanged payload for a.
func NewAlertChanged(a *Alert) AlertChanged {
	return AlertChanged{OrgID: a.OrgID, AlertID: a.ID, MonitorID: a.MonitorID, Status: a.Status}
}
