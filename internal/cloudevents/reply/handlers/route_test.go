package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
)

func TestRouteHandler_TrustFlagRequired(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	ch := seedChannel(t, s, alert.OrgID)
	h := NewRouteHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertRouteV1, types.ReplyAlertRouteV1Data{
		AlertID:         alert.ID,
		TargetChannelID: ch.ID,
		MatchFilter:     map[string]any{"severity": "critical"},
		ExpiresAt:       time.Now().Add(time.Hour),
		Reason:          "pager fatigue",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{})

	if err := h.Handle(ctx, ev, entry, ""); err == nil {
		t.Fatal("expected trust_flag_required rejection")
	}
}

func TestRouteHandler_HappyPath(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	ch := seedChannel(t, s, alert.OrgID)
	h := NewRouteHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertRouteV1, types.ReplyAlertRouteV1Data{
		AlertID:         alert.ID,
		TargetChannelID: ch.ID,
		MatchFilter:     map[string]any{"severity": "critical"},
		ExpiresAt:       time.Now().Add(time.Hour),
		Reason:          "failover",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{TrustElevated: true})

	if err := h.Handle(ctx, ev, entry, ""); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	rules, err := s.AlertRoutingRules().ListActiveForOrg(context.Background(), alert.OrgID, time.Now())
	if err != nil {
		t.Fatalf("ListActiveForOrg: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("rules=%d, want 1", len(rules))
	}
	if rules[0].TargetChannelID != ch.ID {
		t.Errorf("target=%q", rules[0].TargetChannelID)
	}
}

func TestRouteHandler_EmptyMatchFilterRejected(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	ch := seedChannel(t, s, alert.OrgID)
	h := NewRouteHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertRouteV1, types.ReplyAlertRouteV1Data{
		AlertID:         alert.ID,
		TargetChannelID: ch.ID,
		MatchFilter:     map[string]any{},
		ExpiresAt:       time.Now().Add(time.Hour),
		Reason:          "empty",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{TrustElevated: true})
	if err := h.Handle(ctx, ev, entry, ""); err == nil {
		t.Fatal("expected empty_match_filter rejection")
	}
}

func TestRouteHandler_ExpiresInPastRejected(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	ch := seedChannel(t, s, alert.OrgID)
	h := NewRouteHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertRouteV1, types.ReplyAlertRouteV1Data{
		AlertID:         alert.ID,
		TargetChannelID: ch.ID,
		MatchFilter:     map[string]any{"severity": "warning"},
		ExpiresAt:       time.Now().Add(-time.Hour),
		Reason:          "past",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{TrustElevated: true})
	if err := h.Handle(ctx, ev, entry, ""); err == nil {
		t.Fatal("expected expires_not_future rejection")
	}
}

func TestRouteHandler_ExpiresCapClamped(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	ch := seedChannel(t, s, alert.OrgID)
	h := NewRouteHandler(s)

	// Request 7 days ahead; default cap is 24h.
	ev := newReplyEvent(t, types.TypeReplyAlertRouteV1, types.ReplyAlertRouteV1Data{
		AlertID:         alert.ID,
		TargetChannelID: ch.ID,
		MatchFilter:     map[string]any{"severity": "warning"},
		ExpiresAt:       time.Now().Add(7 * 24 * time.Hour),
		Reason:          "too long",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{TrustElevated: true})
	if err := h.Handle(ctx, ev, entry, ""); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	rules, _ := s.AlertRoutingRules().ListActiveForOrg(context.Background(), alert.OrgID, time.Now())
	if len(rules) != 1 {
		t.Fatalf("rules=%d", len(rules))
	}
	// expiresAt should be clamped to ~24h (86400s).
	if got := time.Until(rules[0].ExpiresAt).Seconds(); got > 90000 {
		t.Errorf("expires not clamped: in %vs", got)
	}
}

// TestRouteHandler_EmptyMatchFilterValueRejected verifies C-5: an attacker
// can no longer install a "match-all" rule by supplying {"severity": ""},
// the value-level validator catches it.
func TestRouteHandler_EmptyMatchFilterValueRejected(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	ch := seedChannel(t, s, alert.OrgID)
	h := NewRouteHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertRouteV1, types.ReplyAlertRouteV1Data{
		AlertID:         alert.ID,
		TargetChannelID: ch.ID,
		MatchFilter:     map[string]any{"severity": ""},
		ExpiresAt:       time.Now().Add(time.Hour),
		Reason:          "match-all injection",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{TrustElevated: true})
	if err := h.Handle(ctx, ev, entry, ""); err == nil {
		t.Fatal("expected empty_match_filter_value rejection")
	}
}

// TestRouteHandler_MonitorLevelAlertrefRejected verifies C-2 for route.
func TestRouteHandler_MonitorLevelAlertrefRejected(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	ch := seedChannel(t, s, alert.OrgID)
	h := NewRouteHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertRouteV1, types.ReplyAlertRouteV1Data{
		AlertID:         alert.ID,
		TargetChannelID: ch.ID,
		MatchFilter:     map[string]any{"severity": "critical"},
		ExpiresAt:       time.Now().Add(time.Hour),
		Reason:          "hijack via monitor-level alertref",
	})
	entry := &reply.Entry{OrgID: alert.OrgID, MonitorID: "mon-X"} // no AlertID
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{TrustElevated: true})
	if err := h.Handle(ctx, ev, entry, ""); err == nil {
		t.Fatal("expected alert_scope_required rejection")
	}
}

func TestRouteHandler_CrossOrgChannelRejected(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	other := seedAlert(t, s) // different org
	ch := seedChannel(t, s, other.OrgID)
	h := NewRouteHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertRouteV1, types.ReplyAlertRouteV1Data{
		AlertID:         alert.ID,
		TargetChannelID: ch.ID,
		MatchFilter:     map[string]any{"severity": "warning"},
		ExpiresAt:       time.Now().Add(time.Hour),
		Reason:          "cross-org",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{TrustElevated: true})
	if err := h.Handle(ctx, ev, entry, ""); err == nil {
		t.Fatal("expected cross_org_target rejection")
	}
}
