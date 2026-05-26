package handlers

import (
	"context"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func TestDeduplicatedHandler_HappyPath(t *testing.T) {
	s := newTestStore(t)
	primary := seedAlert(t, s)
	// Create a second alert in the SAME org so the dedup target exists
	// cross-row but intra-org. Note: Alerts().Update does NOT touch org_id,
	// so we must create in-org from the start.
	dup := seedAlertInOrg(t, s, primary.OrgID)

	h := NewDeduplicatedHandler(s)
	ev := newReplyEvent(t, types.TypeReplyAlertDeduplicatedV1, types.ReplyAlertDeduplicatedV1Data{
		AlertID:     dup.ID,
		DuplicateOf: primary.ID,
		Reason:      "same root cause",
	})
	entry := &reply.Entry{AlertID: dup.ID, OrgID: primary.OrgID}

	if err := h.Handle(context.Background(), ev, entry, "sub-alice"); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	events, _ := s.Alerts().ListEvents(context.Background(), dup.ID)
	found := false
	for _, e := range events {
		if e.EventType == domain.EventDuplicated {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected EventDuplicated in timeline, got %+v", events)
	}
	audit, _ := s.CloudEventReplyAudit().ListByAlert(context.Background(), dup.ID, 10)
	if len(audit) != 1 || audit[0].Outcome != "accepted" {
		t.Fatalf("audit=%v", audit)
	}
}

func TestDeduplicatedHandler_MissingDuplicateOf(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewDeduplicatedHandler(s)
	ev := newReplyEvent(t, types.TypeReplyAlertDeduplicatedV1, types.ReplyAlertDeduplicatedV1Data{
		AlertID:     alert.ID,
		DuplicateOf: "",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}

	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected error for missing duplicate_of")
	}
}

func TestDeduplicatedHandler_UnknownDuplicateOf(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewDeduplicatedHandler(s)
	ev := newReplyEvent(t, types.TypeReplyAlertDeduplicatedV1, types.ReplyAlertDeduplicatedV1Data{
		AlertID:     alert.ID,
		DuplicateOf: "nonexistent",
		Reason:      "oops",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}

	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected error for unknown duplicate_of")
	}
}

func TestDeduplicatedHandler_CrossAlertRejected(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewDeduplicatedHandler(s)
	ev := newReplyEvent(t, types.TypeReplyAlertDeduplicatedV1, types.ReplyAlertDeduplicatedV1Data{
		AlertID:     "someone-else",
		DuplicateOf: "x",
		Reason:      "hijack",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}

	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected cross-alert rejection")
	}
}

// TestDeduplicatedHandler_MonitorLevelAlertrefRejected verifies C-2: a
// monitor-scoped entry (entry.AlertID == "") cannot be used to dedup an
// alert the attacker supplies via data.AlertID.
func TestDeduplicatedHandler_MonitorLevelAlertrefRejected(t *testing.T) {
	s := newTestStore(t)
	victim := seedAlert(t, s)
	h := NewDeduplicatedHandler(s)
	ev := newReplyEvent(t, types.TypeReplyAlertDeduplicatedV1, types.ReplyAlertDeduplicatedV1Data{
		AlertID:     victim.ID,
		DuplicateOf: "whatever",
		Reason:      "hijack via monitor-level alertref",
	})
	// attacker's entry has a different org and NO alert id (monitor-scoped).
	entry := &reply.Entry{OrgID: "attacker-org", MonitorID: "mon-X"}

	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected alert_scope_required rejection for monitor-scoped entry")
	}
}

// TestDeduplicatedHandler_CrossOrgAlertRejected verifies C-2: even when
// entry.AlertID and data.AlertID match, if the loaded alert belongs to a
// different org than the entry, reject.
func TestDeduplicatedHandler_CrossOrgAlertRejected(t *testing.T) {
	s := newTestStore(t)
	victim := seedAlert(t, s) // belongs to org-A
	h := NewDeduplicatedHandler(s)
	ev := newReplyEvent(t, types.TypeReplyAlertDeduplicatedV1, types.ReplyAlertDeduplicatedV1Data{
		AlertID:     victim.ID,
		DuplicateOf: "whatever",
		Reason:      "hijack via cross-org entry",
	})
	// Attacker sends a matching alertref but entry is from a different org.
	entry := &reply.Entry{AlertID: victim.ID, OrgID: "attacker-org"}

	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected cross_org_alert rejection")
	}
}

func TestDeduplicatedHandler_AllowsDedupOfResolved(t *testing.T) {
	s := newTestStore(t)
	primary := seedAlert(t, s)
	primary.Status = domain.AlertResolved
	if err := s.Alerts().Update(context.Background(), primary); err != nil {
		t.Fatalf("Update primary resolved: %v", err)
	}
	dup := seedAlertInOrg(t, s, primary.OrgID)

	h := NewDeduplicatedHandler(s)
	ev := newReplyEvent(t, types.TypeReplyAlertDeduplicatedV1, types.ReplyAlertDeduplicatedV1Data{
		AlertID:     dup.ID,
		DuplicateOf: primary.ID,
		Reason:      "late straggler",
	})
	entry := &reply.Entry{AlertID: dup.ID, OrgID: primary.OrgID}

	if err := h.Handle(context.Background(), ev, entry, ""); err != nil {
		t.Fatalf("Handle should allow dedup of resolved primary: %v", err)
	}
}
