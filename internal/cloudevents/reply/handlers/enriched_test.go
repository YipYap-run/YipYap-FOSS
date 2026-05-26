package handlers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func TestEnrichedHandler_Type(t *testing.T) {
	h := NewEnrichedHandler(nil)
	if got, want := h.Type(), types.TypeReplyAlertEnrichedV1; got != want {
		t.Errorf("Type=%q, want %q", got, want)
	}
}

func TestEnrichedHandler_HappyPath(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewEnrichedHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertEnrichedV1, types.ReplyAlertEnrichedV1Data{
		AlertID: alert.ID,
		Attachments: []map[string]any{
			{"label": "runbook", "href": "https://runbooks.example/alert/1", "kind": "runbook"},
			{"label": "screenshot", "preview": "data:image/png;base64,AAAA"},
		},
	})
	entry := &reply.Entry{AlertID: alert.ID}

	if err := h.Handle(context.Background(), ev, entry, "oidc-sub-bob"); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	events, err := s.Alerts().ListEvents(context.Background(), alert.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events=%d, want 1", len(events))
	}
	if events[0].EventType != domain.EventReplyEnriched {
		t.Errorf("EventType=%q, want %q", events[0].EventType, domain.EventReplyEnriched)
	}
	var p EnrichedPayload
	if err := json.Unmarshal(events[0].Detail, &p); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if len(p.Attachments) != 2 {
		t.Fatalf("attachments=%d, want 2", len(p.Attachments))
	}
	if p.Attachments[0].Label != "runbook" || p.Attachments[0].Href == "" {
		t.Errorf("attachment[0] mismatch: %+v", p.Attachments[0])
	}
	if p.Sub != "oidc-sub-bob" {
		t.Errorf("sub=%q, want oidc-sub-bob", p.Sub)
	}
}

func TestEnrichedHandler_CrossAlertHijack(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewEnrichedHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertEnrichedV1, types.ReplyAlertEnrichedV1Data{
		AlertID: "someone-else",
		Attachments: []map[string]any{
			{"label": "x"},
		},
	})
	entry := &reply.Entry{AlertID: alert.ID}

	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected cross-alert error")
	}
	evs, _ := s.Alerts().ListEvents(context.Background(), alert.ID)
	if len(evs) != 0 {
		t.Fatalf("events=%d, want 0", len(evs))
	}
}

func TestEnrichedHandler_BadData(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewEnrichedHandler(s)

	ev := newReplyEventRaw(t, types.TypeReplyAlertEnrichedV1, []byte(`{"attachments":"not-an-array"}`))
	entry := &reply.Entry{AlertID: alert.ID}

	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestEnrichedHandler_TooManyAttachments(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewEnrichedHandler(s)

	atts := make([]map[string]any, 0, 21)
	for i := 0; i < 21; i++ {
		atts = append(atts, map[string]any{"label": "l"})
	}
	ev := newReplyEvent(t, types.TypeReplyAlertEnrichedV1, types.ReplyAlertEnrichedV1Data{
		AlertID:     alert.ID,
		Attachments: atts,
	})
	entry := &reply.Entry{AlertID: alert.ID}

	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected too-many-attachments error")
	}
	evs, _ := s.Alerts().ListEvents(context.Background(), alert.ID)
	if len(evs) != 0 {
		t.Fatalf("events=%d, want 0 after rejection", len(evs))
	}
}

func TestEnrichedHandler_MissingLabel(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewEnrichedHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertEnrichedV1, types.ReplyAlertEnrichedV1Data{
		AlertID: alert.ID,
		Attachments: []map[string]any{
			{"href": "https://x"},
		},
	})
	entry := &reply.Entry{AlertID: alert.ID}

	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected missing-label error")
	}
}
