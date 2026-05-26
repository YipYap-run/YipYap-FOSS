package cloudevents

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
)

// Emitter abstracts CloudEvent emission so callers can be constructed with
// or without real NATS connectivity. A nil Emitter is a no-op, that's the
// intended pattern in tests and FOSS builds without the feature enabled.
//
// Call sites should use the package-level helpers (EmitAlertFired etc.) so
// that a nil Emitter is handled transparently instead of panicking on method
// dispatch.
type Emitter interface {
	EmitAlertFired(ctx context.Context, orgID string, d types.AlertFiredV1Data) error
	EmitAlertAcknowledged(ctx context.Context, orgID string, d types.AlertAcknowledgedV1Data) error
	EmitAlertResolved(ctx context.Context, orgID string, d types.AlertResolvedV1Data) error
	EmitAlertEscalated(ctx context.Context, orgID string, d types.AlertEscalatedV1Data) error

	EmitMonitorUp(ctx context.Context, orgID, monitorID string, d types.MonitorUpV1Data) error
	EmitMonitorDown(ctx context.Context, orgID, monitorID string, d types.MonitorDownV1Data) error
	EmitMonitorDegraded(ctx context.Context, orgID, monitorID string, d types.MonitorDegradedV1Data) error
	EmitMonitorHeartbeatMissed(ctx context.Context, orgID, monitorID string, d types.MonitorHeartbeatMissedV1Data) error

	EmitMaintenanceStarted(ctx context.Context, orgID string, d types.MaintenanceStartedV1Data) error
	EmitMaintenanceEnded(ctx context.Context, orgID string, d types.MaintenanceEndedV1Data) error
}

// EmitAlertFired is a nil-safe wrapper around Emitter.EmitAlertFired.
// When e is nil this is a no-op returning nil.
func EmitAlertFired(ctx context.Context, e Emitter, orgID string, d types.AlertFiredV1Data) error {
	if e == nil {
		return nil
	}
	return e.EmitAlertFired(ctx, orgID, d)
}

// EmitAlertAcknowledged is a nil-safe wrapper around Emitter.EmitAlertAcknowledged.
func EmitAlertAcknowledged(ctx context.Context, e Emitter, orgID string, d types.AlertAcknowledgedV1Data) error {
	if e == nil {
		return nil
	}
	return e.EmitAlertAcknowledged(ctx, orgID, d)
}

// EmitAlertResolved is a nil-safe wrapper around Emitter.EmitAlertResolved.
func EmitAlertResolved(ctx context.Context, e Emitter, orgID string, d types.AlertResolvedV1Data) error {
	if e == nil {
		return nil
	}
	return e.EmitAlertResolved(ctx, orgID, d)
}

// EmitAlertEscalated is a nil-safe wrapper around Emitter.EmitAlertEscalated.
func EmitAlertEscalated(ctx context.Context, e Emitter, orgID string, d types.AlertEscalatedV1Data) error {
	if e == nil {
		return nil
	}
	return e.EmitAlertEscalated(ctx, orgID, d)
}

// EmitMonitorUp is a nil-safe wrapper around Emitter.EmitMonitorUp.
func EmitMonitorUp(ctx context.Context, e Emitter, orgID, monitorID string, d types.MonitorUpV1Data) error {
	if e == nil {
		return nil
	}
	return e.EmitMonitorUp(ctx, orgID, monitorID, d)
}

// EmitMonitorDown is a nil-safe wrapper around Emitter.EmitMonitorDown.
func EmitMonitorDown(ctx context.Context, e Emitter, orgID, monitorID string, d types.MonitorDownV1Data) error {
	if e == nil {
		return nil
	}
	return e.EmitMonitorDown(ctx, orgID, monitorID, d)
}

// EmitMonitorDegraded is a nil-safe wrapper around Emitter.EmitMonitorDegraded.
func EmitMonitorDegraded(ctx context.Context, e Emitter, orgID, monitorID string, d types.MonitorDegradedV1Data) error {
	if e == nil {
		return nil
	}
	return e.EmitMonitorDegraded(ctx, orgID, monitorID, d)
}

// EmitMonitorHeartbeatMissed is a nil-safe wrapper around Emitter.EmitMonitorHeartbeatMissed.
func EmitMonitorHeartbeatMissed(ctx context.Context, e Emitter, orgID, monitorID string, d types.MonitorHeartbeatMissedV1Data) error {
	if e == nil {
		return nil
	}
	return e.EmitMonitorHeartbeatMissed(ctx, orgID, monitorID, d)
}

// EmitMaintenanceStarted is a nil-safe wrapper around Emitter.EmitMaintenanceStarted.
func EmitMaintenanceStarted(ctx context.Context, e Emitter, orgID string, d types.MaintenanceStartedV1Data) error {
	if e == nil {
		return nil
	}
	return e.EmitMaintenanceStarted(ctx, orgID, d)
}

// EmitMaintenanceEnded is a nil-safe wrapper around Emitter.EmitMaintenanceEnded.
func EmitMaintenanceEnded(ctx context.Context, e Emitter, orgID string, d types.MaintenanceEndedV1Data) error {
	if e == nil {
		return nil
	}
	return e.EmitMaintenanceEnded(ctx, orgID, d)
}

// EmitAlertFired constructs an alert.fired.v1 CloudEvent and publishes it.
func (p *Publisher) EmitAlertFired(ctx context.Context, orgID string, d types.AlertFiredV1Data) error {
	ev, err := NewAlertFiredV1(OrgSource(orgID), d)
	if err != nil {
		return err
	}
	return p.Publish(ctx, orgID, ev)
}

// EmitAlertAcknowledged constructs an alert.acknowledged.v1 CloudEvent and publishes it.
func (p *Publisher) EmitAlertAcknowledged(ctx context.Context, orgID string, d types.AlertAcknowledgedV1Data) error {
	ev, err := NewAlertAcknowledgedV1(OrgSource(orgID), d)
	if err != nil {
		return err
	}
	return p.Publish(ctx, orgID, ev)
}

// EmitAlertResolved constructs an alert.resolved.v1 CloudEvent and publishes it.
func (p *Publisher) EmitAlertResolved(ctx context.Context, orgID string, d types.AlertResolvedV1Data) error {
	ev, err := NewAlertResolvedV1(OrgSource(orgID), d)
	if err != nil {
		return err
	}
	return p.Publish(ctx, orgID, ev)
}

// EmitAlertEscalated constructs an alert.escalated.v1 CloudEvent and publishes it.
func (p *Publisher) EmitAlertEscalated(ctx context.Context, orgID string, d types.AlertEscalatedV1Data) error {
	ev, err := NewAlertEscalatedV1(OrgSource(orgID), d)
	if err != nil {
		return err
	}
	return p.Publish(ctx, orgID, ev)
}

// EmitMonitorUp constructs a monitor.up.v1 CloudEvent and publishes it.
func (p *Publisher) EmitMonitorUp(ctx context.Context, orgID, monitorID string, d types.MonitorUpV1Data) error {
	ev, err := NewMonitorUpV1(MonitorSource(orgID, monitorID), d)
	if err != nil {
		return err
	}
	return p.Publish(ctx, orgID, ev)
}

// EmitMonitorDown constructs a monitor.down.v1 CloudEvent and publishes it.
func (p *Publisher) EmitMonitorDown(ctx context.Context, orgID, monitorID string, d types.MonitorDownV1Data) error {
	ev, err := NewMonitorDownV1(MonitorSource(orgID, monitorID), d)
	if err != nil {
		return err
	}
	return p.Publish(ctx, orgID, ev)
}

// EmitMonitorDegraded constructs a monitor.degraded.v1 CloudEvent and publishes it.
func (p *Publisher) EmitMonitorDegraded(ctx context.Context, orgID, monitorID string, d types.MonitorDegradedV1Data) error {
	ev, err := NewMonitorDegradedV1(MonitorSource(orgID, monitorID), d)
	if err != nil {
		return err
	}
	return p.Publish(ctx, orgID, ev)
}

// EmitMonitorHeartbeatMissed constructs a monitor.heartbeat_missed.v1 CloudEvent and publishes it.
func (p *Publisher) EmitMonitorHeartbeatMissed(ctx context.Context, orgID, monitorID string, d types.MonitorHeartbeatMissedV1Data) error {
	ev, err := NewMonitorHeartbeatMissedV1(MonitorSource(orgID, monitorID), d)
	if err != nil {
		return err
	}
	return p.Publish(ctx, orgID, ev)
}

// EmitMaintenanceStarted constructs a maintenance.started.v1 CloudEvent and publishes it.
// Maintenance windows are org-scoped (they can affect one or many monitors);
// the source is the org URI, not a monitor URI.
func (p *Publisher) EmitMaintenanceStarted(ctx context.Context, orgID string, d types.MaintenanceStartedV1Data) error {
	ev, err := NewMaintenanceStartedV1(OrgSource(orgID), d)
	if err != nil {
		return err
	}
	return p.Publish(ctx, orgID, ev)
}

// EmitMaintenanceEnded constructs a maintenance.ended.v1 CloudEvent and publishes it.
func (p *Publisher) EmitMaintenanceEnded(ctx context.Context, orgID string, d types.MaintenanceEndedV1Data) error {
	ev, err := NewMaintenanceEndedV1(OrgSource(orgID), d)
	if err != nil {
		return err
	}
	return p.Publish(ctx, orgID, ev)
}
