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

// RemediationStartedHandler implements reply.alert.remediation_started.v1.
// It creates an alert_remediations row whose expected duration is clamped
// by the channel's MaxRemediationSeconds cap.
type RemediationStartedHandler struct {
	store store.Store
}

// NewRemediationStartedHandler returns a RemediationStartedHandler backed by s.
func NewRemediationStartedHandler(s store.Store) *RemediationStartedHandler {
	return &RemediationStartedHandler{store: s}
}

// Type returns the CE type this handler registers against.
func (h *RemediationStartedHandler) Type() string { return types.TypeReplyAlertRemediationStartedV1 }

func (h *RemediationStartedHandler) Handle(ctx context.Context, ev ce.Event, entry *reply.Entry, sub string) error {
	if entry == nil {
		return fmt.Errorf("reply remediation_started: nil registry entry")
	}
	var data types.ReplyAlertRemediationStartedV1Data
	if err := json.Unmarshal(ev.Data(), &data); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "malformed_data", err.Error())
	}
	if err := enforceMaxRunes("description", data.Description, MaxDescriptionChars); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "field_too_long", err.Error())
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
	alertID := effectiveAlertID(entry.AlertID, data.AlertID)
	if alertID == "" {
		return warnAndReject(ctx, h.store, ev, entry, sub, "missing_alert_id", "")
	}
	if _, err := loadAndVerifyAlert(ctx, h.store, ev, entry, sub, alertID); err != nil {
		return err
	}

	cfg, _ := reply.ConfigFromContext(ctx)
	duration := cfg.ClampRemediationSeconds(int(data.ExpectedDurationSeconds))

	now := time.Now().UTC()
	row := &store.AlertRemediation{
		RemediationID: data.RemediationID,
		AlertID:       alertID,
		Status:        store.RemediationInProgress,
		StartedAt:     now,
		ExpiresAt:     now.Add(time.Duration(duration) * time.Second),
		Description:   data.Description,
		SourceReplyID: ev.ID(),
	}
	if err := h.store.AlertRemediations().Create(ctx, row); err != nil {
		writeAudit(ctx, h.store, ev, entry, sub, handlerErrorOutcome("create_remediation"), nil, nil)
		return fmt.Errorf("reply remediation_started: create: %w", err)
	}

	detail, _ := json.Marshal(map[string]any{
		"remediation_id":   data.RemediationID,
		"description":      data.Description,
		"expected_seconds": duration,
		"sub":              sub,
	})
	_ = h.store.Alerts().CreateEvent(ctx, &domain.AlertEvent{
		ID:        uuid.New().String(),
		AlertID:   alertID,
		EventType: domain.EventRemediationStarted,
		Detail:    detail,
		CreatedAt: now,
	})

	writeAudit(ctx, h.store, ev, entry, sub, outcomeAccepted, nil, row)
	return nil
}
