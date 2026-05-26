package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	ce "github.com/cloudevents/sdk-go/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// minDeregisterReasonLen is the minimum length of a valid deregister reason.
// Because this reply takes the monitor offline permanently until an operator
// re-enables it, we require a non-trivial justification for the audit log.
const minDeregisterReasonLen = 16

// MonitorDeregisterHandler implements reply.monitor.deregister.v1. It
// disables the referenced monitor after verifying BOTH the elevated-trust
// flag AND the separate per-channel AllowDeregister gate.
type MonitorDeregisterHandler struct {
	store store.Store
}

// NewMonitorDeregisterHandler returns a MonitorDeregisterHandler backed by s.
func NewMonitorDeregisterHandler(s store.Store) *MonitorDeregisterHandler {
	return &MonitorDeregisterHandler{store: s}
}

// Type returns the CE type this handler registers against.
func (h *MonitorDeregisterHandler) Type() string { return types.TypeReplyMonitorDeregisterV1 }

func (h *MonitorDeregisterHandler) Handle(ctx context.Context, ev ce.Event, entry *reply.Entry, sub string) error {
	if entry == nil {
		return fmt.Errorf("reply monitor.deregister: nil registry entry")
	}
	cfg, _ := reply.ConfigFromContext(ctx)
	if !cfg.TrustElevated || !cfg.AllowDeregister {
		return warnAndReject(ctx, h.store, ev, entry, sub, "trust_flag_required",
			"channel config missing TrustElevated and/or AllowDeregister")
	}
	var data types.ReplyMonitorDeregisterV1Data
	if err := json.Unmarshal(ev.Data(), &data); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "malformed_data", err.Error())
	}
	if data.MonitorID == "" {
		return warnAndReject(ctx, h.store, ev, entry, sub, "missing_monitor_id", "")
	}
	if entry.MonitorID != "" && entry.MonitorID != data.MonitorID {
		return warnAndReject(ctx, h.store, ev, entry, sub, "cross_monitor",
			fmt.Sprintf("data.monitor_id=%q entry.monitor_id=%q", data.MonitorID, entry.MonitorID))
	}
	if err := enforceMaxRunes("reason", data.Reason, MaxReasonChars); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "field_too_long", err.Error())
	}
	trimmed := strings.TrimSpace(data.Reason)
	if len(trimmed) < minDeregisterReasonLen {
		return warnAndReject(ctx, h.store, ev, entry, sub, "reason_too_short",
			fmt.Sprintf("reason must be >= %d non-whitespace chars, got %d", minDeregisterReasonLen, len(trimmed)))
	}

	before, err := h.store.Monitors().GetByID(ctx, data.MonitorID)
	if err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "unknown_monitor",
			fmt.Sprintf("monitor=%q: %v", data.MonitorID, err))
	}
	if entry.OrgID != "" && before.OrgID != entry.OrgID {
		return warnAndReject(ctx, h.store, ev, entry, sub, "cross_org_monitor",
			fmt.Sprintf("monitor.org=%q entry.org=%q", before.OrgID, entry.OrgID))
	}
	if err := h.store.Monitors().Disable(ctx, data.MonitorID, data.Reason); err != nil {
		writeAudit(ctx, h.store, ev, entry, sub, handlerErrorOutcome("disable"), before, nil)
		return fmt.Errorf("reply monitor.deregister: Disable: %w", err)
	}
	after, _ := h.store.Monitors().GetByID(ctx, data.MonitorID)
	writeAudit(ctx, h.store, ev, entry, sub, outcomeAccepted, before, after)
	return nil
}
