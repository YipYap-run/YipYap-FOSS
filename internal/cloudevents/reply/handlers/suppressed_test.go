package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
)

func TestSuppressedHandler_Type(t *testing.T) {
	h := NewSuppressedHandler(nil)
	if got, want := h.Type(), types.TypeReplyAlertSuppressedV1; got != want {
		t.Errorf("Type=%q, want %q", got, want)
	}
}

func TestSuppressedHandler_HappyPath(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewSuppressedHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertSuppressedV1, types.ReplyAlertSuppressedV1Data{
		AlertID:         alert.ID,
		DurationSeconds: 600,
		Reason:          "known maintenance window",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID, ChannelID: "ch1"}

	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{})
	if err := h.Handle(ctx, ev, entry, "sub-alice"); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	rows, err := s.AlertSuppressions().ListActiveForAlert(context.Background(), alert.ID, time.Now())
	if err != nil {
		t.Fatalf("ListActiveForAlert: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("suppression rows=%d, want 1", len(rows))
	}
	if rows[0].Reason != "known maintenance window" {
		t.Errorf("reason=%q", rows[0].Reason)
	}
	if got := time.Until(rows[0].ExpiresAt).Seconds(); got < 500 || got > 700 {
		t.Errorf("expires ~600s from now, got %v", got)
	}

	// Audit row written.
	audit, err := s.CloudEventReplyAudit().ListByAlert(context.Background(), alert.ID, 10)
	if err != nil {
		t.Fatalf("ListByAlert audit: %v", err)
	}
	if len(audit) != 1 {
		t.Fatalf("audit rows=%d, want 1", len(audit))
	}
	if audit[0].Outcome != "accepted" {
		t.Errorf("audit outcome=%q, want accepted", audit[0].Outcome)
	}
}

func TestSuppressedHandler_CrossAlertRejected(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewSuppressedHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertSuppressedV1, types.ReplyAlertSuppressedV1Data{
		AlertID:         "someone-elses-alert",
		DurationSeconds: 600,
		Reason:          "phishing",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}

	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected cross-alert rejection")
	}

	rows, _ := s.AlertSuppressions().ListActiveForAlert(context.Background(), alert.ID, time.Now())
	if len(rows) != 0 {
		t.Fatalf("no suppression should be created: got %d", len(rows))
	}
}

func TestSuppressedHandler_DurationClamped(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewSuppressedHandler(s)

	// Request 10h; default cap is 3600s.
	ev := newReplyEvent(t, types.TypeReplyAlertSuppressedV1, types.ReplyAlertSuppressedV1Data{
		AlertID:         alert.ID,
		DurationSeconds: 36000,
		Reason:          "attempted overreach",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}

	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{})
	if err := h.Handle(ctx, ev, entry, ""); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	rows, err := s.AlertSuppressions().ListActiveForAlert(context.Background(), alert.ID, time.Now())
	if err != nil {
		t.Fatalf("ListActiveForAlert: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows=%d, want 1", len(rows))
	}
	// expires_at should be ~3600s from now, not 36000s.
	if got := time.Until(rows[0].ExpiresAt).Seconds(); got > 3700 {
		t.Errorf("duration not clamped: expires in %vs", got)
	}
}

// TestSuppressedHandler_MonitorLevelAlertrefRejected verifies C-2: a sink
// holding a monitor-scoped registry entry (entry.AlertID == "") cannot
// suppress an alert by supplying data.AlertID.
func TestSuppressedHandler_MonitorLevelAlertrefRejected(t *testing.T) {
	s := newTestStore(t)
	victim := seedAlert(t, s)
	h := NewSuppressedHandler(s)
	ev := newReplyEvent(t, types.TypeReplyAlertSuppressedV1, types.ReplyAlertSuppressedV1Data{
		AlertID:         victim.ID,
		DurationSeconds: 600,
		Reason:          "hijack via monitor-level alertref",
	})
	entry := &reply.Entry{OrgID: "attacker-org", MonitorID: "mon-X"} // no AlertID
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{})
	if err := h.Handle(ctx, ev, entry, ""); err == nil {
		t.Fatal("expected alert_scope_required rejection")
	}
	rows, _ := s.AlertSuppressions().ListActiveForAlert(context.Background(), victim.ID, time.Now())
	if len(rows) != 0 {
		t.Fatalf("victim alert must not be suppressed; got %d rows", len(rows))
	}
}

// TestSuppressedHandler_CrossOrgAlertRejected verifies C-2: a cross-org
// entry pointing at an alert in another org is rejected after alert load.
func TestSuppressedHandler_CrossOrgAlertRejected(t *testing.T) {
	s := newTestStore(t)
	victim := seedAlert(t, s)
	h := NewSuppressedHandler(s)
	ev := newReplyEvent(t, types.TypeReplyAlertSuppressedV1, types.ReplyAlertSuppressedV1Data{
		AlertID:         victim.ID,
		DurationSeconds: 600,
		Reason:          "cross-org suppression attempt",
	})
	entry := &reply.Entry{AlertID: victim.ID, OrgID: "attacker-org"}
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{})
	if err := h.Handle(ctx, ev, entry, ""); err == nil {
		t.Fatal("expected cross_org_alert rejection")
	}
}

// TestSuppressedHandler_EmptyMatchFilterValueRejected verifies C-5: empty /
// whitespace-only match_filter values must be rejected.
func TestSuppressedHandler_EmptyMatchFilterValueRejected(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewSuppressedHandler(s)
	ev := newReplyEvent(t, types.TypeReplyAlertSuppressedV1, types.ReplyAlertSuppressedV1Data{
		AlertID:         alert.ID,
		DurationSeconds: 60,
		Reason:          "empty-value injection",
		MatchFilter:     map[string]any{"severity": ""},
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{})
	if err := h.Handle(ctx, ev, entry, ""); err == nil {
		t.Fatal("expected empty_match_filter_value rejection")
	}
}

func TestSuppressedHandler_ChannelCapHonored(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewSuppressedHandler(s)

	// Channel config overrides cap to 120s.
	cfg := reply.DispatcherConfig{MaxSuppressSeconds: 120}
	ctx := reply.WithConfig(context.Background(), cfg)

	ev := newReplyEvent(t, types.TypeReplyAlertSuppressedV1, types.ReplyAlertSuppressedV1Data{
		AlertID:         alert.ID,
		DurationSeconds: 999,
		Reason:          "short cap",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}

	if err := h.Handle(ctx, ev, entry, ""); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	rows, _ := s.AlertSuppressions().ListActiveForAlert(context.Background(), alert.ID, time.Now())
	if len(rows) != 1 {
		t.Fatalf("rows=%d, want 1", len(rows))
	}
	if got := time.Until(rows[0].ExpiresAt).Seconds(); got > 130 {
		t.Errorf("channel cap not applied: expires in %vs", got)
	}
}
