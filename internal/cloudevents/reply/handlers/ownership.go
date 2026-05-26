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

var allowedOwnerKinds = map[string]struct{}{
	"user": {},
	"team": {},
}

// OwnershipHandler implements reply.alert.ownership.v1. It mutates
// alerts.owner_kind + alerts.owner_id after verifying the target exists in
// the same org as the alert.
type OwnershipHandler struct {
	store store.Store
}

// NewOwnershipHandler returns an OwnershipHandler backed by s.
func NewOwnershipHandler(s store.Store) *OwnershipHandler {
	return &OwnershipHandler{store: s}
}

// Type returns the CE type this handler registers against.
func (h *OwnershipHandler) Type() string { return types.TypeReplyAlertOwnershipV1 }

func (h *OwnershipHandler) Handle(ctx context.Context, ev ce.Event, entry *reply.Entry, sub string) error {
	if entry == nil {
		return fmt.Errorf("reply ownership: nil registry entry")
	}
	var data types.ReplyAlertOwnershipV1Data
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
	if _, ok := allowedOwnerKinds[data.OwnerKind]; !ok {
		return warnAndReject(ctx, h.store, ev, entry, sub, "invalid_owner_kind",
			fmt.Sprintf("owner_kind=%q", data.OwnerKind))
	}
	if data.OwnerID == "" {
		return warnAndReject(ctx, h.store, ev, entry, sub, "missing_owner_id", "")
	}
	if err := enforceMaxRunes("reason", data.Reason, MaxReasonChars); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "field_too_long", err.Error())
	}

	alertID := effectiveAlertID(entry.AlertID, data.AlertID)
	if alertID == "" {
		return warnAndReject(ctx, h.store, ev, entry, sub, "missing_alert_id", "")
	}

	// Verify owner exists in the correct org. For users we ALSO reject
	// disabled accounts (Round-1 residual), without this, a sink can
	// transfer ownership to a deactivated user, breaking on-call rotations.
	switch data.OwnerKind {
	case "user":
		u, err := h.store.Users().GetByID(ctx, data.OwnerID)
		if err != nil {
			return warnAndReject(ctx, h.store, ev, entry, sub, "unknown_owner",
				fmt.Sprintf("user=%q: %v", data.OwnerID, err))
		}
		if entry.OrgID != "" && u.OrgID != entry.OrgID {
			return warnAndReject(ctx, h.store, ev, entry, sub, "cross_org_owner",
				fmt.Sprintf("user.org=%q entry.org=%q", u.OrgID, entry.OrgID))
		}
		if u.DisabledAt != nil {
			return warnAndReject(ctx, h.store, ev, entry, sub, "owner_disabled",
				fmt.Sprintf("user=%q disabled_at=%s", u.ID, u.DisabledAt))
		}
	case "team":
		team, err := h.store.Teams().GetByID(ctx, data.OwnerID)
		if err != nil {
			return warnAndReject(ctx, h.store, ev, entry, sub, "unknown_owner",
				fmt.Sprintf("team=%q: %v", data.OwnerID, err))
		}
		if entry.OrgID != "" && team.OrgID != entry.OrgID {
			return warnAndReject(ctx, h.store, ev, entry, sub, "cross_org_owner",
				fmt.Sprintf("team.org=%q entry.org=%q", team.OrgID, entry.OrgID))
		}
	}

	before, err := loadAndVerifyAlert(ctx, h.store, ev, entry, sub, alertID)
	if err != nil {
		return err
	}
	if err := h.store.Alerts().SetOwner(ctx, alertID, data.OwnerKind, data.OwnerID, data.Reason); err != nil {
		writeAudit(ctx, h.store, ev, entry, sub, handlerErrorOutcome("set_owner"), before, nil)
		return fmt.Errorf("reply ownership: SetOwner: %w", err)
	}
	after, _ := h.store.Alerts().GetByID(ctx, alertID)
	writeAudit(ctx, h.store, ev, entry, sub, outcomeAccepted, before, after)
	return nil
}
