package handlers

import (
	"context"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
)

func TestMonitorDeregisterHandler_TrustFlagsRequired(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewMonitorDeregisterHandler(s)

	ev := newReplyEvent(t, types.TypeReplyMonitorDeregisterV1, types.ReplyMonitorDeregisterV1Data{
		MonitorID: alert.MonitorID,
		Reason:    "system decommission confirmed by SRE team",
	})
	entry := &reply.Entry{OrgID: alert.OrgID, MonitorID: alert.MonitorID}

	// TrustElevated but no AllowDeregister → reject.
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{TrustElevated: true})
	if err := h.Handle(ctx, ev, entry, ""); err == nil {
		t.Fatal("expected trust_flag_required (AllowDeregister missing)")
	}
	// Neither flag → reject.
	ctx = reply.WithConfig(context.Background(), reply.DispatcherConfig{})
	if err := h.Handle(ctx, ev, entry, ""); err == nil {
		t.Fatal("expected trust_flag_required (nothing set)")
	}

	// Monitor still enabled.
	mon, _ := s.Monitors().GetByID(context.Background(), alert.MonitorID)
	if !mon.Enabled {
		t.Errorf("monitor got disabled despite trust-flag rejection")
	}
}

func TestMonitorDeregisterHandler_HappyPath(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewMonitorDeregisterHandler(s)

	ev := newReplyEvent(t, types.TypeReplyMonitorDeregisterV1, types.ReplyMonitorDeregisterV1Data{
		MonitorID: alert.MonitorID,
		Reason:    "system decommission confirmed by SRE team",
	})
	entry := &reply.Entry{OrgID: alert.OrgID, MonitorID: alert.MonitorID}
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{TrustElevated: true, AllowDeregister: true})

	if err := h.Handle(ctx, ev, entry, "sub-ops"); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	mon, _ := s.Monitors().GetByID(context.Background(), alert.MonitorID)
	if mon.Enabled {
		t.Errorf("monitor should be disabled")
	}
}

func TestMonitorDeregisterHandler_ReasonTooShort(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewMonitorDeregisterHandler(s)
	ev := newReplyEvent(t, types.TypeReplyMonitorDeregisterV1, types.ReplyMonitorDeregisterV1Data{
		MonitorID: alert.MonitorID,
		Reason:    "too short",
	})
	entry := &reply.Entry{OrgID: alert.OrgID, MonitorID: alert.MonitorID}
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{TrustElevated: true, AllowDeregister: true})
	if err := h.Handle(ctx, ev, entry, ""); err == nil {
		t.Fatal("expected reason_too_short rejection")
	}
}

// TestMonitorDeregisterHandler_ReasonWhitespaceOnly verifies H-21: a reason
// made entirely of whitespace (which satisfied len() >= 16 before the fix)
// must be rejected. The old code trusted byte count; the fix counts
// non-whitespace characters via strings.TrimSpace.
func TestMonitorDeregisterHandler_ReasonWhitespaceOnly(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewMonitorDeregisterHandler(s)

	ev := newReplyEvent(t, types.TypeReplyMonitorDeregisterV1, types.ReplyMonitorDeregisterV1Data{
		MonitorID: alert.MonitorID,
		Reason:    "                ", // 16 spaces: passes byte-count; fails trim.
	})
	entry := &reply.Entry{OrgID: alert.OrgID, MonitorID: alert.MonitorID}
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{TrustElevated: true, AllowDeregister: true})
	if err := h.Handle(ctx, ev, entry, ""); err == nil {
		t.Fatal("expected reason_too_short rejection for whitespace-only reason")
	}
	mon, _ := s.Monitors().GetByID(context.Background(), alert.MonitorID)
	if !mon.Enabled {
		t.Errorf("monitor should NOT have been disabled on whitespace reason")
	}
}

func TestMonitorDeregisterHandler_CrossMonitorRejected(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	other := seedAlert(t, s)
	h := NewMonitorDeregisterHandler(s)

	ev := newReplyEvent(t, types.TypeReplyMonitorDeregisterV1, types.ReplyMonitorDeregisterV1Data{
		MonitorID: other.MonitorID, // hijack
		Reason:    "system decommission confirmed by SRE team",
	})
	entry := &reply.Entry{OrgID: alert.OrgID, MonitorID: alert.MonitorID}
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{TrustElevated: true, AllowDeregister: true})
	if err := h.Handle(ctx, ev, entry, ""); err == nil {
		t.Fatal("expected cross_monitor rejection")
	}
	m, _ := s.Monitors().GetByID(context.Background(), other.MonitorID)
	if !m.Enabled {
		t.Errorf("other monitor should not be disabled")
	}
}
