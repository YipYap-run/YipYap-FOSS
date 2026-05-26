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

// allowedAckReasonCodes enumerates the reason_code values we accept for
// ack_with_context. Unknown values are rejected and logged via slog so the
// customer can see them in their reply audit log.
var allowedAckReasonCodes = map[string]struct{}{
	"investigating":     {},
	"known_issue":       {},
	"false_positive":    {},
	"duplicate":         {},
	"remediated":        {},
	"deferred":          {},
	"waiting_on_vendor": {},
	"in_maintenance":    {},
	"other":             {},
}

// AckWithContextHandler implements reply.alert.ack_with_context.v1. Acks the
// alert and stores the reason_code + note + external reference_id.
type AckWithContextHandler struct {
	store store.Store
}

// NewAckWithContextHandler returns an AckWithContextHandler backed by s.
func NewAckWithContextHandler(s store.Store) *AckWithContextHandler {
	return &AckWithContextHandler{store: s}
}

// Type returns the CE type this handler registers against.
func (h *AckWithContextHandler) Type() string { return types.TypeReplyAlertAckWithContextV1 }

func (h *AckWithContextHandler) Handle(ctx context.Context, ev ce.Event, entry *reply.Entry, sub string) error {
	if entry == nil {
		return fmt.Errorf("reply ack_with_context: nil registry entry")
	}
	var data types.ReplyAlertAckWithContextV1Data
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
	if _, ok := allowedAckReasonCodes[data.ReasonCode]; !ok {
		return warnAndReject(ctx, h.store, ev, entry, sub, "invalid_reason_code",
			fmt.Sprintf("reason_code=%q", data.ReasonCode))
	}
	if err := enforceMaxRunes("note", data.Note, MaxNoteChars); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "field_too_long", err.Error())
	}
	if err := enforceMaxRunes("reference_id", data.ReferenceID, MaxReferenceIDChars); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "field_too_long", err.Error())
	}
	alertID := effectiveAlertID(entry.AlertID, data.AlertID)
	if alertID == "" {
		return warnAndReject(ctx, h.store, ev, entry, sub, "missing_alert_id", "")
	}

	before, err := loadAndVerifyAlert(ctx, h.store, ev, entry, sub, alertID)
	if err != nil {
		return err
	}
	// ackedBy MUST be a non-spoofable attribution. The verified OIDC subject
	// (sub) is trusted when non-empty. Otherwise, FOSS allows unauth replies
	//, fall back to a synthetic id derived from the CE envelope's id so the
	// audit trail shows this was a reply-channel ack rather than a real user.
	// DO NOT use ev.Source(): it is an attacker-controlled CE attribute.
	// The raw ev.Source() is still captured by writeAudit for forensics.
	ackedBy := sub
	if ackedBy == "" {
		ackedBy = "cloudevent:reply:" + ev.ID()
	}
	if err := h.store.Alerts().AckWithContext(ctx, alertID, data.ReasonCode, data.Note, data.ReferenceID, ackedBy); err != nil {
		writeAudit(ctx, h.store, ev, entry, sub, handlerErrorOutcome("ack"), before, nil)
		return fmt.Errorf("reply ack_with_context: AckWithContext: %w", err)
	}
	after, _ := h.store.Alerts().GetByID(ctx, alertID)
	writeAudit(ctx, h.store, ev, entry, sub, outcomeAccepted, before, after)
	return nil
}
