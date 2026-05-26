package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func newReplyEvent(t *testing.T, typ string, data any) ce.Event {
	t.Helper()
	ev := ce.NewEvent()
	ev.SetSpecVersion("1.0")
	ev.SetID("reply-" + typ)
	ev.SetType(typ)
	ev.SetSource("https://consumer.example.com/")
	ev.SetTime(time.Now().UTC())
	if data != nil {
		if err := ev.SetData(ce.ApplicationJSON, data); err != nil {
			t.Fatalf("SetData: %v", err)
		}
	}
	return ev
}

// newReplyEventRaw builds an event with a raw (possibly malformed) JSON
// body so malformed-data cases can be exercised.
func newReplyEventRaw(t *testing.T, typ string, raw []byte) ce.Event {
	t.Helper()
	ev := ce.NewEvent()
	ev.SetSpecVersion("1.0")
	ev.SetID("reply-" + typ)
	ev.SetType(typ)
	ev.SetSource("https://consumer.example.com/")
	ev.SetTime(time.Now().UTC())
	if err := ev.SetData(ce.ApplicationJSON, json.RawMessage(raw)); err != nil {
		t.Fatalf("SetData raw: %v", err)
	}
	return ev
}

func TestClaimedHandler_Type(t *testing.T) {
	h := NewClaimedHandler(nil)
	if got, want := h.Type(), types.TypeReplyAlertClaimedV1; got != want {
		t.Errorf("Type=%q, want %q", got, want)
	}
}

func TestClaimedHandler_HappyPath(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewClaimedHandler(s)

	claimedAt := time.Now().UTC().Truncate(time.Second)
	ev := newReplyEvent(t, types.TypeReplyAlertClaimedV1, types.ReplyAlertClaimedV1Data{
		AlertID:   alert.ID,
		Who:       "alice",
		ClaimedAt: claimedAt,
		Note:      "on it",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}

	if err := h.Handle(context.Background(), ev, entry, "oidc-sub-alice"); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	events, err := s.Alerts().ListEvents(context.Background(), alert.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events=%d, want 1", len(events))
	}
	if events[0].EventType != domain.EventReplyClaimed {
		t.Errorf("EventType=%q, want %q", events[0].EventType, domain.EventReplyClaimed)
	}
	var p ClaimedPayload
	if err := json.Unmarshal(events[0].Detail, &p); err != nil {
		t.Fatalf("unmarshal detail: %v (raw=%s)", err, events[0].Detail)
	}
	// A3-NEW-M-3: Actor is the OIDC sub when present; data.Who is recorded
	// only as ReportedAs (descriptive label, never the actor identity).
	if p.Actor != "oidc-sub-alice" {
		t.Errorf("Actor=%q, want oidc-sub-alice", p.Actor)
	}
	if p.ReportedAs != "alice" {
		t.Errorf("ReportedAs=%q, want alice", p.ReportedAs)
	}
	if p.Note != "on it" || p.Sub != "oidc-sub-alice" {
		t.Errorf("payload mismatch: %+v", p)
	}
	if !p.ClaimedAt.Equal(claimedAt) {
		t.Errorf("ClaimedAt=%v, want %v", p.ClaimedAt, claimedAt)
	}
}

// TestClaimedHandler_ActorFallsBackToSyntheticID verifies A3-NEW-M-3 when
// the reply has no OIDC subject: data.Who="alice" is NOT recorded as the
// actor, the synthetic "cloudevent:reply:<id>" is used and Who lands in
// ReportedAs.
func TestClaimedHandler_ActorFallsBackToSyntheticID(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewClaimedHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertClaimedV1, types.ReplyAlertClaimedV1Data{
		AlertID:   alert.ID,
		Who:       "alice",
		ClaimedAt: time.Now().UTC(),
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	// No OIDC sub.
	if err := h.Handle(context.Background(), ev, entry, ""); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	events, _ := s.Alerts().ListEvents(context.Background(), alert.ID)
	if len(events) != 1 {
		t.Fatalf("events=%d, want 1", len(events))
	}
	var p ClaimedPayload
	_ = json.Unmarshal(events[0].Detail, &p)
	wantActor := "cloudevent:reply:" + ev.ID()
	if p.Actor != wantActor {
		t.Errorf("Actor=%q, want %q (must NOT trust data.Who as identity)", p.Actor, wantActor)
	}
	if p.ReportedAs != "alice" {
		t.Errorf("ReportedAs=%q, want alice", p.ReportedAs)
	}
	if p.Sub != "" {
		t.Errorf("Sub=%q, want empty", p.Sub)
	}
}

// TestClaimedHandler_MonitorScopedEntryRejected verifies R2-H5 (Phase-2
// handlers must close the monitor-level alertref pivot).
func TestClaimedHandler_MonitorScopedEntryRejected(t *testing.T) {
	s := newTestStore(t)
	victim := seedAlert(t, s)
	h := NewClaimedHandler(s)

	// Sink holds a monitor-scoped alertref (no AlertID), supplies the
	// victim's alert id in data, without R2-H5 this would graffiti the
	// victim's timeline.
	ev := newReplyEvent(t, types.TypeReplyAlertClaimedV1, types.ReplyAlertClaimedV1Data{
		AlertID:   victim.ID,
		Who:       "mallory",
		ClaimedAt: time.Now().UTC(),
	})
	entry := &reply.Entry{OrgID: victim.OrgID, MonitorID: "mon-X"} // no AlertID
	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected alert_scope_required rejection")
	}
	evs, _ := s.Alerts().ListEvents(context.Background(), victim.ID)
	if len(evs) != 0 {
		t.Fatalf("victim timeline got %d events; expected 0 after rejection", len(evs))
	}
}

func TestClaimedHandler_CrossAlertHijack(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewClaimedHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertClaimedV1, types.ReplyAlertClaimedV1Data{
		AlertID:   "someone-elses-alert",
		Who:       "mallory",
		ClaimedAt: time.Now().UTC(),
	})
	entry := &reply.Entry{AlertID: alert.ID}

	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected cross-alert error, got nil")
	}

	events, _ := s.Alerts().ListEvents(context.Background(), alert.ID)
	if len(events) != 0 {
		t.Fatalf("events=%d, want 0 (nothing should be written on rejection)", len(events))
	}
}

func TestClaimedHandler_BadData(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewClaimedHandler(s)

	ev := newReplyEventRaw(t, types.TypeReplyAlertClaimedV1, []byte(`{"who":123}`)) // wrong type for who
	entry := &reply.Entry{AlertID: alert.ID}

	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected unmarshal error, got nil")
	}
}

func TestClaimedHandler_NilEntry(t *testing.T) {
	h := NewClaimedHandler(newTestStore(t))
	ev := newReplyEvent(t, types.TypeReplyAlertClaimedV1, types.ReplyAlertClaimedV1Data{
		Who:       "x",
		AlertID:   "a1",
		ClaimedAt: time.Now().UTC(),
	})
	if err := h.Handle(context.Background(), ev, nil, ""); err == nil {
		t.Fatal("expected error for nil entry")
	}
}

func TestClaimedHandler_MissingWho(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewClaimedHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertClaimedV1, types.ReplyAlertClaimedV1Data{
		AlertID:   alert.ID,
		ClaimedAt: time.Now().UTC(),
	})
	entry := &reply.Entry{AlertID: alert.ID}

	err := h.Handle(context.Background(), ev, entry, "")
	if err == nil {
		t.Fatal("expected missing-who error, got nil")
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected error kind: %v", err)
	}
}
