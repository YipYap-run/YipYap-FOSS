package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// allowedEscalationSeverities restricts the severity enum to the three
// canonical values. Unknown values are rejected and logged per the
// project-wide default for reply enum handling.
var allowedEscalationSeverities = map[string]domain.Severity{
	"critical": domain.SeverityCritical,
	"warning":  domain.SeverityWarning,
	"info":     domain.SeverityInfo,
}

// EscalatedHandler implements reply.alert.escalated.v1. It mutates
// alerts.severity and/or alerts.current_escalation_step. Requires the
// channel's DispatcherConfig.TrustElevated flag.
type EscalatedHandler struct {
	store store.Store
}

// NewEscalatedHandler returns an EscalatedHandler backed by s.
func NewEscalatedHandler(s store.Store) *EscalatedHandler {
	return &EscalatedHandler{store: s}
}

// Type returns the CE type this handler registers against.
func (h *EscalatedHandler) Type() string { return types.TypeReplyAlertEscalatedV1 }

// lookupEscalationStepUUID resolves a user-supplied step_index into the
// corresponding step.ID (UUID) for the monitor's escalation policy. Matches
// on domain.EscalationStep.Position (the zero-based ordinal used by
// engine.go, outbound alert.escalated events emit StepIndex =
// step.Position so the round-trip is consistent).
//
// Returns an error if:
//   - the monitor isn't found
//   - the monitor has no escalation policy
//   - no step with the requested Position exists
func (h *EscalatedHandler) lookupEscalationStepUUID(ctx context.Context, monitorID string, stepIndex int64) (string, error) {
	mon, err := h.store.Monitors().GetByID(ctx, monitorID)
	if err != nil {
		return "", fmt.Errorf("monitor not found: %w", err)
	}
	if mon.EscalationPolicyID == "" {
		return "", fmt.Errorf("monitor %q has no escalation policy", monitorID)
	}
	steps, err := h.store.EscalationPolicies().GetSteps(ctx, mon.EscalationPolicyID)
	if err != nil {
		return "", fmt.Errorf("load steps: %w", err)
	}
	for _, st := range steps {
		if int64(st.Position) == stepIndex {
			return st.ID, nil
		}
	}
	return "", fmt.Errorf("step_index=%d out of range for policy %q (%d steps)",
		stepIndex, mon.EscalationPolicyID, len(steps))
}

func (h *EscalatedHandler) Handle(ctx context.Context, ev ce.Event, entry *reply.Entry, sub string) error {
	if entry == nil {
		return fmt.Errorf("reply escalated: nil registry entry")
	}
	cfg, _ := reply.ConfigFromContext(ctx)
	if !cfg.TrustElevated {
		return warnAndReject(ctx, h.store, ev, entry, sub, "trust_flag_required",
			"channel config missing TrustElevated")
	}
	var data types.ReplyAlertEscalatedV1Data
	if err := json.Unmarshal(ev.Data(), &data); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "malformed_data", err.Error())
	}
	if err := enforceMaxRunes("reason", data.Reason, MaxReasonChars); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "field_too_long", err.Error())
	}
	if entry.AlertID != "" && data.AlertID != "" && data.AlertID != entry.AlertID {
		return warnAndReject(ctx, h.store, ev, entry, sub, "cross_alert",
			fmt.Sprintf("data.alert_id=%q entry.alert_id=%q", data.AlertID, entry.AlertID))
	}
	if err := requireAlertScopedEntry(ctx, h.store, ev, entry, sub); err != nil {
		return err
	}
	// Validate severity enum if present. Unknown values reject.
	var newSev domain.Severity
	if data.NewSeverity != "" {
		sev, ok := allowedEscalationSeverities[data.NewSeverity]
		if !ok {
			return warnAndReject(ctx, h.store, ev, entry, sub, "invalid_severity",
				fmt.Sprintf("new_severity=%q", data.NewSeverity))
		}
		newSev = sev
	}
	if data.NewSeverity == "" && data.StepIndex == 0 {
		return warnAndReject(ctx, h.store, ev, entry, sub, "no_mutation",
			"must supply new_severity or step_index")
	}

	alertID := effectiveAlertID(entry.AlertID, data.AlertID)
	if alertID == "" {
		return warnAndReject(ctx, h.store, ev, entry, sub, "missing_alert_id", "")
	}

	before, err := loadAndVerifyAlert(ctx, h.store, ev, entry, sub, alertID)
	if err != nil {
		return err
	}
	mutated := *before
	if newSev != "" {
		mutated.Severity = newSev
	}
	if data.StepIndex > 0 {
		// CurrentEscalationStep is a UUID everywhere else in the codebase
		// (engine.go matches against step.ID). Writing the numeric string
		// directly froze escalation. Look the monitor's policy up, resolve
		// step_index → step.ID, and write the UUID. Reject if the monitor
		// has no policy or the index is out of range.
		stepUUID, lerr := h.lookupEscalationStepUUID(ctx, before.MonitorID, data.StepIndex)
		if lerr != nil {
			return warnAndReject(ctx, h.store, ev, entry, sub, "invalid_step_index", lerr.Error())
		}
		mutated.CurrentEscalationStep = stepUUID
	}

	if err := h.store.Alerts().Update(ctx, &mutated); err != nil {
		writeAudit(ctx, h.store, ev, entry, sub, handlerErrorOutcome("update_alert"), before, nil)
		return fmt.Errorf("reply escalated: Update: %w", err)
	}

	// Timeline event so the UI can render a pill.
	detail, _ := json.Marshal(map[string]any{
		"new_severity": data.NewSeverity,
		"step_index":   data.StepIndex,
		"reason":       data.Reason,
		"sub":          sub,
	})
	_ = h.store.Alerts().CreateEvent(ctx, &domain.AlertEvent{
		ID:        uuid.New().String(),
		AlertID:   alertID,
		EventType: domain.EventEscalatedReply,
		Detail:    detail,
		CreatedAt: time.Now().UTC(),
	})

	after, _ := h.store.Alerts().GetByID(ctx, alertID)
	writeAudit(ctx, h.store, ev, entry, sub, outcomeAccepted, before, after)
	return nil
}
