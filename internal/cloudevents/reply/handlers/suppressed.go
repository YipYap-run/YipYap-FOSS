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

// SuppressedHandler implements reply.alert.suppressed.v1. It creates an
// alert_suppressions row whose duration is clamped to the channel's
// configured cap (DispatcherConfig.MaxSuppressSeconds, default 3600s).
type SuppressedHandler struct {
	store store.Store
}

// NewSuppressedHandler returns a SuppressedHandler backed by s.
func NewSuppressedHandler(s store.Store) *SuppressedHandler {
	return &SuppressedHandler{store: s}
}

// Type returns the CE type this handler registers against.
func (h *SuppressedHandler) Type() string { return types.TypeReplyAlertSuppressedV1 }

// Handle unmarshals the reply, validates it, creates the suppression row,
// writes an alert_events timeline row, and records the audit entry.
func (h *SuppressedHandler) Handle(ctx context.Context, ev ce.Event, entry *reply.Entry, sub string) error {
	if entry == nil {
		return fmt.Errorf("reply suppressed: nil registry entry")
	}
	var data types.ReplyAlertSuppressedV1Data
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

	if err := enforceMaxRunes("reason", data.Reason, MaxReasonChars); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "field_too_long", err.Error())
	}
	if err := validateMatchFilterValues(data.MatchFilter); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "filter_invalid", err.Error())
	}

	cfg, _ := reply.ConfigFromContext(ctx)
	// Clamp duration: zero => use cap; request > cap => cap. Negative => cap.
	duration := cfg.ClampSuppressSeconds(int(data.DurationSeconds))

	alertID := effectiveAlertID(entry.AlertID, data.AlertID)
	if _, err := loadAndVerifyAlert(ctx, h.store, ev, entry, sub, alertID); err != nil {
		return err
	}
	now := time.Now().UTC()
	row := &store.AlertSuppression{
		ID:            uuid.New().String(),
		OrgID:         entry.OrgID,
		AlertID:       alertID,
		MonitorID:     entry.MonitorID,
		ExpiresAt:     now.Add(time.Duration(duration) * time.Second),
		Reason:        data.Reason,
		SourceReplyID: ev.ID(),
		CreatedAt:     now,
	}
	if len(data.MatchFilter) > 0 {
		mf, err := json.Marshal(data.MatchFilter)
		if err != nil {
			return fmt.Errorf("reply suppressed: marshal match_filter: %w", err)
		}
		row.MatchFilter = mf
	}

	if err := h.store.AlertSuppressions().Create(ctx, row); err != nil {
		writeAudit(ctx, h.store, ev, entry, sub, handlerErrorOutcome("create_suppression"), nil, nil)
		return fmt.Errorf("reply suppressed: create: %w", err)
	}

	// Timeline event so UI can render a pill.
	if alertID != "" {
		payload := map[string]any{
			"suppression_id":   row.ID,
			"duration_seconds": duration,
			"reason":           data.Reason,
			"sub":              sub,
		}
		detail, _ := json.Marshal(payload)
		_ = h.store.Alerts().CreateEvent(ctx, &domain.AlertEvent{
			ID:        uuid.New().String(),
			AlertID:   alertID,
			EventType: domain.EventSuppressed,
			Detail:    detail,
			CreatedAt: now,
		})
	}

	writeAudit(ctx, h.store, ev, entry, sub, outcomeAccepted, nil, row)
	return nil
}
