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

// RouteHandler implements reply.alert.route.v1. It creates an alert routing
// rule row so future notifications for matching alerts in the same org go
// to a different channel. Requires DispatcherConfig.TrustElevated.
type RouteHandler struct {
	store store.Store
}

// NewRouteHandler returns a RouteHandler backed by s.
func NewRouteHandler(s store.Store) *RouteHandler {
	return &RouteHandler{store: s}
}

// Type returns the CE type this handler registers against.
func (h *RouteHandler) Type() string { return types.TypeReplyAlertRouteV1 }

func (h *RouteHandler) Handle(ctx context.Context, ev ce.Event, entry *reply.Entry, sub string) error {
	if entry == nil {
		return fmt.Errorf("reply route: nil registry entry")
	}
	cfg, _ := reply.ConfigFromContext(ctx)
	if !cfg.TrustElevated {
		return warnAndReject(ctx, h.store, ev, entry, sub, "trust_flag_required",
			"channel config missing TrustElevated")
	}
	var data types.ReplyAlertRouteV1Data
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
	if data.TargetChannelID == "" {
		return warnAndReject(ctx, h.store, ev, entry, sub, "missing_target", "")
	}
	if err := enforceMaxRunes("reason", data.Reason, MaxReasonChars); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "field_too_long", err.Error())
	}
	if len(data.MatchFilter) == 0 {
		return warnAndReject(ctx, h.store, ev, entry, sub, "empty_match_filter",
			"routing rule must have at least one filter field")
	}
	if err := validateMatchFilterValues(data.MatchFilter); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "filter_invalid", err.Error())
	}
	// Round-1 residual: target channel must exist, belong to the same org,
	// AND be enabled. Without the enabled check, an attacker can resurrect
	// a disabled channel as a notification target via a routing rule.
	ch, err := h.store.NotificationChannels().GetByID(ctx, data.TargetChannelID)
	if err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "unknown_target_channel",
			fmt.Sprintf("channel=%q: %v", data.TargetChannelID, err))
	}
	if entry.OrgID != "" && ch.OrgID != entry.OrgID {
		return warnAndReject(ctx, h.store, ev, entry, sub, "cross_org_target",
			fmt.Sprintf("channel.org=%q entry.org=%q", ch.OrgID, entry.OrgID))
	}
	if !ch.Enabled {
		return warnAndReject(ctx, h.store, ev, entry, sub, "target_channel_disabled",
			fmt.Sprintf("channel=%q", ch.ID))
	}

	// Validate expires_at: must be in the future, and not exceed the cap.
	now := time.Now().UTC()
	if data.ExpiresAt.IsZero() || !data.ExpiresAt.After(now) {
		return warnAndReject(ctx, h.store, ev, entry, sub, "expires_not_future",
			fmt.Sprintf("expires_at=%s", data.ExpiresAt))
	}
	requestedSeconds := int(data.ExpiresAt.Sub(now).Seconds())
	clamped := cfg.ClampRouteDuration(requestedSeconds)
	expiresAt := data.ExpiresAt
	if clamped < requestedSeconds {
		expiresAt = now.Add(time.Duration(clamped) * time.Second)
	}

	filterJSON, err := json.Marshal(data.MatchFilter)
	if err != nil {
		return fmt.Errorf("reply route: marshal match_filter: %w", err)
	}

	alertID := effectiveAlertID(entry.AlertID, data.AlertID)
	if _, err := loadAndVerifyAlert(ctx, h.store, ev, entry, sub, alertID); err != nil {
		return err
	}
	row := &store.AlertRoutingRule{
		ID:              uuid.New().String(),
		OrgID:           entry.OrgID,
		TargetChannelID: data.TargetChannelID,
		MatchFilter:     filterJSON,
		ExpiresAt:       expiresAt,
		Reason:          data.Reason,
		SourceReplyID:   ev.ID(),
		CreatedAt:       now,
	}
	if err := h.store.AlertRoutingRules().Create(ctx, row); err != nil {
		writeAudit(ctx, h.store, ev, entry, sub, handlerErrorOutcome("create_routing_rule"), nil, nil)
		return fmt.Errorf("reply route: create: %w", err)
	}

	if alertID != "" {
		detail, _ := json.Marshal(map[string]any{
			"routing_rule_id":   row.ID,
			"target_channel_id": data.TargetChannelID,
			"expires_at":        expiresAt,
			"reason":            data.Reason,
			"sub":               sub,
		})
		_ = h.store.Alerts().CreateEvent(ctx, &domain.AlertEvent{
			ID:        uuid.New().String(),
			AlertID:   alertID,
			EventType: domain.EventRouted,
			Detail:    detail,
			CreatedAt: now,
		})
	}

	writeAudit(ctx, h.store, ev, entry, sub, outcomeAccepted, nil, row)
	return nil
}
