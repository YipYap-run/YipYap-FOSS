package checker

import (
	"context"
	"log/slog"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// MonitorTransition describes one persisted check for CloudEvent emission.
type MonitorTransition struct {
	OrgID     string
	MonitorID string
	CheckID   string
	Prev      domain.CheckStatus // "" when the monitor was never checked
	Cur       domain.CheckStatus
	LatencyMS int
	Error     string
	CheckedAt time.Time
}

// EmitMonitorTransition publishes monitor.up/down/degraded.v1 CloudEvents on
// a status change. It is the external eventing path and is additive to the
// alert.trigger/recover publish. The monolith scheduler and the split
// check-result writer both call it so webhook subscribers see the same
// events in either deployment. A nil emitter is a no-op; emission errors are
// logged, not returned, because the check itself is already stored.
func EmitMonitorTransition(ctx context.Context, em cloudevents.Emitter, t MonitorTransition) {
	if em == nil {
		return
	}
	switch t.Cur {
	case domain.StatusUp:
		if t.Prev == domain.StatusDown || t.Prev == domain.StatusDegraded {
			d := types.MonitorUpV1Data{
				MonitorID: t.MonitorID,
				CheckID:   t.CheckID,
				LatencyMs: int64(t.LatencyMS),
				CheckedAt: t.CheckedAt,
			}
			if err := cloudevents.EmitMonitorUp(ctx, em, t.OrgID, t.MonitorID, d); err != nil {
				slog.Error("emit monitor.up.v1", "monitor_id", t.MonitorID, "error", err)
			}
		}
	case domain.StatusDown:
		if t.Prev == "" || t.Prev == domain.StatusUp || t.Prev == domain.StatusDegraded {
			reason := t.Error
			if reason == "" {
				reason = "unknown"
			}
			d := types.MonitorDownV1Data{
				MonitorID:     t.MonitorID,
				CheckID:       t.CheckID,
				FailureReason: reason,
				CheckedAt:     t.CheckedAt,
			}
			if err := cloudevents.EmitMonitorDown(ctx, em, t.OrgID, t.MonitorID, d); err != nil {
				slog.Error("emit monitor.down.v1", "monitor_id", t.MonitorID, "error", err)
			}
		}
	case domain.StatusDegraded:
		if t.Prev != domain.StatusDegraded {
			reason := t.Error
			if reason == "" {
				reason = "degraded"
			}
			d := types.MonitorDegradedV1Data{
				MonitorID: t.MonitorID,
				CheckID:   t.CheckID,
				Reason:    reason,
				CheckedAt: t.CheckedAt,
			}
			if err := cloudevents.EmitMonitorDegraded(ctx, em, t.OrgID, t.MonitorID, d); err != nil {
				slog.Error("emit monitor.degraded.v1", "monitor_id", t.MonitorID, "error", err)
			}
		}
	}
}
