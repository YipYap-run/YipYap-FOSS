package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	ce "github.com/cloudevents/sdk-go/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// DeduplicatedHandler implements reply.alert.deduplicated.v1. It marks the
// replied-to alert as a duplicate of another alert in the same org. Resolved
// primaries are allowed (a runbook may close an incident and tag late
// stragglers).
type DeduplicatedHandler struct {
	store store.Store
}

// NewDeduplicatedHandler returns a DeduplicatedHandler backed by s.
func NewDeduplicatedHandler(s store.Store) *DeduplicatedHandler {
	return &DeduplicatedHandler{store: s}
}

// Type returns the CE type this handler registers against.
func (h *DeduplicatedHandler) Type() string { return types.TypeReplyAlertDeduplicatedV1 }

func (h *DeduplicatedHandler) Handle(ctx context.Context, ev ce.Event, entry *reply.Entry, sub string) error {
	if entry == nil {
		return fmt.Errorf("reply deduplicated: nil registry entry")
	}
	var data types.ReplyAlertDeduplicatedV1Data
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
	if data.DuplicateOf == "" {
		return warnAndReject(ctx, h.store, ev, entry, sub, "missing_duplicate_of",
			"data.duplicate_of is required")
	}

	alertID := effectiveAlertID(entry.AlertID, data.AlertID)
	if alertID == "" {
		return warnAndReject(ctx, h.store, ev, entry, sub, "missing_alert_id", "no alert_id on entry or data")
	}
	if alertID == data.DuplicateOf {
		return warnAndReject(ctx, h.store, ev, entry, sub, "self_duplicate",
			"alert cannot be a duplicate of itself")
	}

	// Verify duplicate_of exists in the same org. Resolved alerts are allowed.
	primary, err := h.store.Alerts().GetByID(ctx, data.DuplicateOf)
	if err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "unknown_duplicate_of",
			fmt.Sprintf("duplicate_of=%q: %v", data.DuplicateOf, err))
	}
	if entry.OrgID != "" && primary.OrgID != entry.OrgID {
		return warnAndReject(ctx, h.store, ev, entry, sub, "cross_org_duplicate",
			fmt.Sprintf("primary.org=%q entry.org=%q", primary.OrgID, entry.OrgID))
	}

	before, err := loadAndVerifyAlert(ctx, h.store, ev, entry, sub, alertID)
	if err != nil {
		return err
	}
	if err := h.store.Alerts().MarkDuplicate(ctx, alertID, data.DuplicateOf, data.Reason); err != nil {
		writeAudit(ctx, h.store, ev, entry, sub, handlerErrorOutcome("mark_duplicate"), before, nil)
		return fmt.Errorf("reply deduplicated: MarkDuplicate: %w", err)
	}
	after, _ := h.store.Alerts().GetByID(ctx, alertID)
	writeAudit(ctx, h.store, ev, entry, sub, outcomeAccepted, before, after)
	return nil
}
