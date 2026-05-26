package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// ClaimedPayload is the JSON shape persisted in AlertEvent.Detail for a
// reply.claimed annotation. It combines the typed reply data with the
// OIDC-verified `sub` claim so future audit queries can trace who did what.
//
// Field policy (A3-NEW-M-3):
//   - Actor  , the non-spoofable identity used by the audit/timeline as
//     "who performed this action". Always either the OIDC sub (when
//     available) or the synthetic "cloudevent:reply:<id>". NEVER the
//     sink-supplied data.Who.
//   - ReportedAs, the descriptive label the sink supplied (data.Who).
//     Stored verbatim for forensic context but never trusted as identity.
//   - Sub    , the verified OIDC subject when present, otherwise empty.
type ClaimedPayload struct {
	Actor      string    `json:"actor"`
	ReportedAs string    `json:"reported_as,omitempty"`
	ClaimedAt  time.Time `json:"claimed_at"`
	Note       string    `json:"note,omitempty"`
	Sub        string    `json:"sub,omitempty"`
}

// ClaimedHandler annotates the alert timeline when a downstream consumer
// reports that an operator has claimed ownership of an alert. It is a
// read-only annotation, the alert's ack status is NOT mutated.
type ClaimedHandler struct {
	store store.Store
}

// NewClaimedHandler returns a ClaimedHandler backed by s.
func NewClaimedHandler(s store.Store) *ClaimedHandler {
	return &ClaimedHandler{store: s}
}

// Type returns the CE type string this handler registers against.
func (h *ClaimedHandler) Type() string {
	return types.TypeReplyAlertClaimedV1
}

// Handle unmarshals the reply data, validates it against the outbound
// registry entry, and persists a timeline annotation.
func (h *ClaimedHandler) Handle(ctx context.Context, ev ce.Event, entry *reply.Entry, sub string) error {
	if entry == nil {
		return fmt.Errorf("reply claimed: nil registry entry")
	}

	var data types.ReplyAlertClaimedV1Data
	if err := json.Unmarshal(ev.Data(), &data); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "malformed_data", err.Error())
	}
	if data.Who == "" {
		return warnAndReject(ctx, h.store, ev, entry, sub, "missing_who",
			"reply.claimed requires a 'who' label")
	}
	if err := enforceMaxRunes("who", data.Who, MaxReferenceIDChars); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "field_too_long", err.Error())
	}
	if err := enforceMaxRunes("note", data.Note, MaxNoteChars); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "field_too_long", err.Error())
	}
	if entry.AlertID != "" && data.AlertID != "" && data.AlertID != entry.AlertID {
		return warnAndReject(ctx, h.store, ev, entry, sub, "cross_alert",
			fmt.Sprintf("data.alert_id=%q entry.alert_id=%q", data.AlertID, entry.AlertID))
	}
	// R2-H5: Phase-2 handlers must close the monitor-level-alertref pivot.
	// Without this, a sink with a monitor-scoped alertref can graffiti
	// victim-org alert timelines using an attacker-chosen data.AlertID.
	if err := requireAlertScopedEntry(ctx, h.store, ev, entry, sub); err != nil {
		return err
	}

	alertID := effectiveAlertID(entry.AlertID, data.AlertID)
	if alertID == "" {
		return warnAndReject(ctx, h.store, ev, entry, sub, "missing_alert_id", "")
	}
	if _, err := loadAndVerifyAlert(ctx, h.store, ev, entry, sub, alertID); err != nil {
		return err
	}

	payload := ClaimedPayload{
		// A3-NEW-M-3: actor is OIDC-verified or synthetic, never data.Who.
		Actor:      effectiveActor(ev, sub),
		ReportedAs: data.Who,
		ClaimedAt:  data.ClaimedAt,
		Note:       data.Note,
		Sub:        sub,
	}
	if err := writeTimelineEvent(ctx, h.store, alertID, domain.EventReplyClaimed, payload); err != nil {
		writeAudit(ctx, h.store, ev, entry, sub, handlerErrorOutcome("write_timeline"), nil, nil)
		return err
	}
	writeAudit(ctx, h.store, ev, entry, sub, outcomeAccepted, nil, payload)
	return nil
}
