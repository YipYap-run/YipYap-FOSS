package escalation

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// suppressingWindow returns the id of a maintenance window that suppresses
// alerts for monitorID at `at`, or "" when none does. A store error counts
// as no window so a database problem never silences a page.
func (e *Engine) suppressingWindow(ctx context.Context, monitorID string, at time.Time) string {
	mws, err := e.store.MaintenanceWindows().ListActiveByMonitor(ctx, monitorID, at)
	if err != nil {
		slog.Warn("escalation: list maintenance windows", "monitor_id", monitorID, "error", err)
		return ""
	}
	for _, mw := range mws {
		if mw.SuppressAlerts {
			return mw.ID
		}
	}
	return ""
}

// markSuppressed records that a maintenance window held this alert's page.
// The timeline event is written once per hold, not on every tick.
func (e *Engine) markSuppressed(ctx context.Context, alert *domain.Alert, state *domain.AlertEscalationState, windowID string) {
	meta := readMeta(state)
	if meta.Suppressed && !meta.RecoveredAfterWindow {
		return
	}
	meta.Suppressed = true
	meta.SuppressedWindowID = windowID
	meta.RecoveredAfterWindow = false
	writeMeta(state, meta)
	if err := e.store.Alerts().UpsertEscalationState(ctx, state); err != nil {
		slog.Error("escalation: record maintenance hold", "alert_id", alert.ID, "error", err)
	}
	detail, _ := json.Marshal(map[string]string{
		"reason":    "maintenance_window",
		"window_id": windowID,
	})
	e.createEvent(ctx, alert.ID, domain.EventSuppressed, "", "", detail)
	e.announce(ctx, alert)
}

// stillFailing reports whether the monitor's latest check is not up. With no
// check on record the alert's own trigger is the latest evidence, so it
// counts as failing.
func (e *Engine) stillFailing(ctx context.Context, monitorID string) bool {
	check, err := e.store.Checks().GetLatest(ctx, monitorID)
	if err != nil || check == nil {
		return true
	}
	return check.Status != domain.StatusUp
}

// handleMaintenance runs before the escalation timer for each firing alert.
// It returns true when the alert must not advance on this tick:
//
//   - While a suppressing window covers the monitor, the alert holds on its
//     current step (no retries, no advancing) and the hold is recorded.
//   - When the window has ended and the alert was held, the current step is
//     paged now if the latest check is still failing, and the step timer
//     restarts. If the monitor is passing, nothing is sent; a recovery
//     resolves the alert, and a later failure while it is still firing
//     pages then.
func (e *Engine) handleMaintenance(ctx context.Context, alert *domain.Alert, monitor *domain.Monitor, step *domain.EscalationStep, state *domain.AlertEscalationState, now time.Time) bool {
	if windowID := e.suppressingWindow(ctx, monitor.ID, now); windowID != "" {
		e.markSuppressed(ctx, alert, state, windowID)
		return true
	}
	meta := readMeta(state)
	if !meta.Suppressed {
		return false
	}

	if !e.stillFailing(ctx, monitor.ID) {
		if !meta.RecoveredAfterWindow {
			meta.RecoveredAfterWindow = true
			writeMeta(state, meta)
			if err := e.store.Alerts().UpsertEscalationState(ctx, state); err != nil {
				slog.Error("escalation: record post-maintenance state", "alert_id", alert.ID, "error", err)
			}
			detail, _ := json.Marshal(map[string]string{
				"reason":    "maintenance_ended_monitor_up",
				"window_id": meta.SuppressedWindowID,
			})
			e.createEvent(ctx, alert.ID, domain.EventSuppressed, "", "", detail)
			e.announce(ctx, alert)
		}
		return true
	}

	slog.Info("escalation: maintenance ended with monitor still failing, paging", "alert_id", alert.ID, "window_id", meta.SuppressedWindowID)
	meta.Suppressed = false
	meta.SuppressedWindowID = ""
	meta.RecoveredAfterWindow = false
	// The step is paged again from the start, which restarts each
	// person's notification rules, so delayed rules held by the window
	// are dropped rather than sent on top.
	meta.Pending = nil
	writeMeta(state, meta)
	state.StepEnteredAt = now
	if err := e.store.Alerts().UpsertEscalationState(ctx, state); err != nil {
		slog.Error("escalation: clear maintenance hold", "alert_id", alert.ID, "error", err)
	}
	e.notifyStep(ctx, alert, monitor, step, state)
	return true
}
