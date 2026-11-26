// Package events defines the domain events that monitors, alerts and
// maintenance windows raise on lifecycle transitions, and the Emitter
// interface that publishes them.
//
// A nil Emitter is a no-op. Call sites use the package-level helpers
// (EmitAlertFired etc.) so a nil Emitter never panics on method dispatch.
package events

import (
	"context"
	"time"
)

// AlertAcknowledged is raised when a user or integration acknowledges an alert.
type AlertAcknowledged struct {
	AlertID   string    `json:"alert_id"`
	MonitorID string    `json:"monitor_id"`
	AckedBy   string    `json:"acked_by"`
	AckedAt   time.Time `json:"acked_at"`
}

// AlertEscalated is raised when an alert moves to the next escalation step.
type AlertEscalated struct {
	AlertID     string    `json:"alert_id"`
	MonitorID   string    `json:"monitor_id"`
	StepIndex   int64     `json:"step_index"`
	ToChannel   string    `json:"to_channel"`
	EscalatedAt time.Time `json:"escalated_at"`
}

// AlertFired is raised when a new alert opens.
type AlertFired struct {
	AlertID   string    `json:"alert_id"`
	MonitorID string    `json:"monitor_id"`
	Severity  string    `json:"severity"`
	Reason    string    `json:"reason"`
	FiredAt   time.Time `json:"fired_at"`
}

// AlertResolved is raised when an alert closes. ResolvedBy is empty for an
// automatic recovery.
type AlertResolved struct {
	AlertID    string    `json:"alert_id"`
	MonitorID  string    `json:"monitor_id"`
	ResolvedBy string    `json:"resolved_by,omitempty"`
	ResolvedAt time.Time `json:"resolved_at"`
}

// MaintenanceEnded is raised when a maintenance window closes.
type MaintenanceEnded struct {
	WindowID   string    `json:"window_id"`
	EndedAt    time.Time `json:"ended_at"`
	EndedEarly bool      `json:"ended_early,omitempty"`
}

// MaintenanceStarted is raised when a maintenance window opens.
type MaintenanceStarted struct {
	WindowID     string    `json:"window_id"`
	Monitors     []string  `json:"monitors"`
	StartedAt    time.Time `json:"started_at"`
	ScheduledEnd time.Time `json:"scheduled_end,omitempty"`
}

// MonitorDegraded is raised when a monitor enters the degraded state.
type MonitorDegraded struct {
	MonitorID string    `json:"monitor_id"`
	CheckID   string    `json:"check_id"`
	Reason    string    `json:"reason"`
	CheckedAt time.Time `json:"checked_at"`
}

// MonitorDown is raised when a monitor enters the down state.
type MonitorDown struct {
	MonitorID     string    `json:"monitor_id"`
	CheckID       string    `json:"check_id"`
	FailureReason string    `json:"failure_reason"`
	CheckedAt     time.Time `json:"checked_at"`
}

// MonitorHeartbeatMissed is raised when a heartbeat monitor misses its deadline.
type MonitorHeartbeatMissed struct {
	MonitorID  string    `json:"monitor_id"`
	ExpectedBy time.Time `json:"expected_by"`
	LastSeenAt time.Time `json:"last_seen_at,omitempty"`
}

// MonitorUp is raised when a monitor returns to the up state.
type MonitorUp struct {
	MonitorID string    `json:"monitor_id"`
	CheckID   string    `json:"check_id"`
	LatencyMs int64     `json:"latency_ms,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// Emitter publishes domain events. Implementations must be safe for
// concurrent use.
type Emitter interface {
	EmitAlertFired(ctx context.Context, orgID string, d AlertFired) error
	EmitAlertAcknowledged(ctx context.Context, orgID string, d AlertAcknowledged) error
	EmitAlertResolved(ctx context.Context, orgID string, d AlertResolved) error
	EmitAlertEscalated(ctx context.Context, orgID string, d AlertEscalated) error

	EmitMonitorUp(ctx context.Context, orgID, monitorID string, d MonitorUp) error
	EmitMonitorDown(ctx context.Context, orgID, monitorID string, d MonitorDown) error
	EmitMonitorDegraded(ctx context.Context, orgID, monitorID string, d MonitorDegraded) error
	EmitMonitorHeartbeatMissed(ctx context.Context, orgID, monitorID string, d MonitorHeartbeatMissed) error

	EmitMaintenanceStarted(ctx context.Context, orgID string, d MaintenanceStarted) error
	EmitMaintenanceEnded(ctx context.Context, orgID string, d MaintenanceEnded) error
}

// EmitAlertFired calls e.EmitAlertFired, or does nothing when e is nil.
func EmitAlertFired(ctx context.Context, e Emitter, orgID string, d AlertFired) error {
	if e == nil {
		return nil
	}
	return e.EmitAlertFired(ctx, orgID, d)
}

// EmitAlertAcknowledged calls e.EmitAlertAcknowledged, or does nothing when e is nil.
func EmitAlertAcknowledged(ctx context.Context, e Emitter, orgID string, d AlertAcknowledged) error {
	if e == nil {
		return nil
	}
	return e.EmitAlertAcknowledged(ctx, orgID, d)
}

// EmitAlertResolved calls e.EmitAlertResolved, or does nothing when e is nil.
func EmitAlertResolved(ctx context.Context, e Emitter, orgID string, d AlertResolved) error {
	if e == nil {
		return nil
	}
	return e.EmitAlertResolved(ctx, orgID, d)
}

// EmitAlertEscalated calls e.EmitAlertEscalated, or does nothing when e is nil.
func EmitAlertEscalated(ctx context.Context, e Emitter, orgID string, d AlertEscalated) error {
	if e == nil {
		return nil
	}
	return e.EmitAlertEscalated(ctx, orgID, d)
}

// EmitMonitorUp calls e.EmitMonitorUp, or does nothing when e is nil.
func EmitMonitorUp(ctx context.Context, e Emitter, orgID, monitorID string, d MonitorUp) error {
	if e == nil {
		return nil
	}
	return e.EmitMonitorUp(ctx, orgID, monitorID, d)
}

// EmitMonitorDown calls e.EmitMonitorDown, or does nothing when e is nil.
func EmitMonitorDown(ctx context.Context, e Emitter, orgID, monitorID string, d MonitorDown) error {
	if e == nil {
		return nil
	}
	return e.EmitMonitorDown(ctx, orgID, monitorID, d)
}

// EmitMonitorDegraded calls e.EmitMonitorDegraded, or does nothing when e is nil.
func EmitMonitorDegraded(ctx context.Context, e Emitter, orgID, monitorID string, d MonitorDegraded) error {
	if e == nil {
		return nil
	}
	return e.EmitMonitorDegraded(ctx, orgID, monitorID, d)
}

// EmitMonitorHeartbeatMissed calls e.EmitMonitorHeartbeatMissed, or does nothing when e is nil.
func EmitMonitorHeartbeatMissed(ctx context.Context, e Emitter, orgID, monitorID string, d MonitorHeartbeatMissed) error {
	if e == nil {
		return nil
	}
	return e.EmitMonitorHeartbeatMissed(ctx, orgID, monitorID, d)
}

// EmitMaintenanceStarted calls e.EmitMaintenanceStarted, or does nothing when e is nil.
func EmitMaintenanceStarted(ctx context.Context, e Emitter, orgID string, d MaintenanceStarted) error {
	if e == nil {
		return nil
	}
	return e.EmitMaintenanceStarted(ctx, orgID, d)
}

// EmitMaintenanceEnded calls e.EmitMaintenanceEnded, or does nothing when e is nil.
func EmitMaintenanceEnded(ctx context.Context, e Emitter, orgID string, d MaintenanceEnded) error {
	if e == nil {
		return nil
	}
	return e.EmitMaintenanceEnded(ctx, orgID, d)
}
