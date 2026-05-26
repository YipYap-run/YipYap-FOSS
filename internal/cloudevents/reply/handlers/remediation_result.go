package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// autoResolveFreshCheckWindow is the maximum age of the monitor's latest
// UP check that the remediation_result handler will trust when
// auto-resolving an alert. Older UP checks, likely from before the incident
// started, or a transient flip in a flapping monitor, must not drive
// auto-resolve; the alert is left firing for the next real check to decide.
const autoResolveFreshCheckWindow = 60 * time.Second

// allowedRemediationOutcomes enumerates the remediation result enum. Unknown
// values are rejected with slog.Warn per the project default.
var allowedRemediationOutcomes = map[string]store.RemediationStatus{
	"success": store.RemediationSuccess,
	"failure": store.RemediationFailure,
	"partial": store.RemediationPartial,
}

// RemediationResultHandler implements reply.alert.remediation_result.v1. It
// completes a prior alert_remediations row and, when outcome=="success"
// and the monitor's latest check is UP, auto-resolves the alert.
type RemediationResultHandler struct {
	store store.Store
}

// NewRemediationResultHandler returns a RemediationResultHandler backed by s.
func NewRemediationResultHandler(s store.Store) *RemediationResultHandler {
	return &RemediationResultHandler{store: s}
}

// Type returns the CE type this handler registers against.
func (h *RemediationResultHandler) Type() string { return types.TypeReplyAlertRemediationResultV1 }

func (h *RemediationResultHandler) Handle(ctx context.Context, ev ce.Event, entry *reply.Entry, sub string) error {
	if entry == nil {
		return fmt.Errorf("reply remediation_result: nil registry entry")
	}
	var data types.ReplyAlertRemediationResultV1Data
	if err := json.Unmarshal(ev.Data(), &data); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "malformed_data", err.Error())
	}
	if entry.AlertID != "" && data.AlertID != "" && data.AlertID != entry.AlertID {
		return warnAndReject(ctx, h.store, ev, entry, sub, "cross_alert",
			fmt.Sprintf("data.alert_id=%q entry.alert_id=%q", data.AlertID, entry.AlertID))
	}
	if err := requireAlertScopedEntry(ctx, h.store, ev, entry, sub); err != nil {
		return err
	}
	if data.RemediationID == "" {
		return warnAndReject(ctx, h.store, ev, entry, sub, "missing_remediation_id", "")
	}
	outcome, ok := allowedRemediationOutcomes[data.Outcome]
	if !ok {
		return warnAndReject(ctx, h.store, ev, entry, sub, "invalid_outcome",
			fmt.Sprintf("outcome=%q", data.Outcome))
	}
	if err := enforceMaxRunes("message", data.Message, MaxMessageChars); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "field_too_long", err.Error())
	}
	// A2-NM-2: artifact_url is sink-supplied; enforce http/https scheme +
	// length cap before persisting it to the audit/timeline.
	if err := validateReplyURL(data.ArtifactURL); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "url_scheme", err.Error())
	}

	// Look up the in-flight remediation.
	existing, err := h.store.AlertRemediations().GetByID(ctx, data.RemediationID)
	if err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "unknown_remediation",
			fmt.Sprintf("remediation_id=%q: %v", data.RemediationID, err))
	}
	if existing.Status != store.RemediationInProgress {
		return warnAndReject(ctx, h.store, ev, entry, sub, "remediation_already_completed",
			fmt.Sprintf("status=%q", existing.Status))
	}
	// entry.AlertID is non-empty (requireAlertScopedEntry above); verify the
	// remediation's own alert binding matches. Without this, a sink with any
	// valid alertref could close a remediation belonging to a different alert.
	if existing.AlertID != entry.AlertID {
		return warnAndReject(ctx, h.store, ev, entry, sub, "remediation_alert_mismatch",
			fmt.Sprintf("remediation.alert=%q entry.alert=%q", existing.AlertID, entry.AlertID))
	}
	// And that alert must belong to entry.OrgID (closes C-2/C-6 by loading
	// the alert and comparing orgs before any mutation).
	if _, err := loadAndVerifyAlert(ctx, h.store, ev, entry, sub, existing.AlertID); err != nil {
		return err
	}

	if err := h.store.AlertRemediations().Complete(ctx, data.RemediationID, outcome, data.Message, data.ArtifactURL); err != nil {
		// A3-NEW-M-2: Complete now applies a CAS (status='in_progress'
		// guard). A racing concurrent reply that lost the CAS gets
		// ErrAlreadyComplete, surface it as a rejected audit and short
		// out before any auto-resolve / timeline write so the alert is
		// never double-resolved.
		if errors.Is(err, store.ErrAlreadyComplete) {
			return warnAndReject(ctx, h.store, ev, entry, sub, "remediation_already_completed",
				fmt.Sprintf("remediation_id=%q lost CAS", data.RemediationID))
		}
		writeAudit(ctx, h.store, ev, entry, sub, handlerErrorOutcome("complete_remediation"), existing, nil)
		return fmt.Errorf("reply remediation_result: Complete: %w", err)
	}

	alertID := existing.AlertID
	now := time.Now().UTC()

	// Auto-resolve when outcome=success AND the monitor's latest check is
	// up AND that check is fresh (within the autoResolveFreshCheckWindow).
	// A stale UP check (e.g. from before the incident started, or a
	// transient flip in a flapping monitor) must not auto-resolve an
	// otherwise-firing alert, log a warn and let the next real check
	// drive resolution.
	var alertBefore, alertAfter *domain.Alert
	alertBefore, _ = h.store.Alerts().GetByID(ctx, alertID)
	if alertBefore != nil && outcome == store.RemediationSuccess && alertBefore.Status != domain.AlertResolved {
		latest, _ := h.store.Checks().GetLatest(ctx, alertBefore.MonitorID)
		if latest != nil && latest.Status == domain.StatusUp {
			if now.Sub(latest.CheckedAt) <= autoResolveFreshCheckWindow {
				mutated := *alertBefore
				mutated.Status = domain.AlertResolved
				t := now
				mutated.ResolvedAt = &t
				_ = h.store.Alerts().Update(ctx, &mutated)
			} else {
				slog.Warn("reply remediation_result: latest UP check too stale; skipping auto-resolve",
					"alert_id", alertID,
					"monitor_id", alertBefore.MonitorID,
					"latest_checked_at", latest.CheckedAt,
					"age", now.Sub(latest.CheckedAt),
				)
			}
		}
	}
	alertAfter, _ = h.store.Alerts().GetByID(ctx, alertID)

	after, _ := h.store.AlertRemediations().GetByID(ctx, data.RemediationID)
	detail, _ := json.Marshal(map[string]any{
		"remediation_id": data.RemediationID,
		"outcome":        data.Outcome,
		"message":        data.Message,
		"artifact_url":   data.ArtifactURL,
		"sub":            sub,
	})
	_ = h.store.Alerts().CreateEvent(ctx, &domain.AlertEvent{
		ID:        uuid.New().String(),
		AlertID:   alertID,
		EventType: domain.EventRemediationResult,
		Detail:    detail,
		CreatedAt: now,
	})

	writeAudit(ctx, h.store, ev, entry, sub, outcomeAccepted,
		map[string]any{"remediation": existing, "alert": alertBefore},
		map[string]any{"remediation": after, "alert": alertAfter},
	)
	return nil
}
