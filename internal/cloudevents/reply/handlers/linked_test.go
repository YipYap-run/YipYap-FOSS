package handlers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func TestLinkedHandler_Type(t *testing.T) {
	h := NewLinkedHandler(nil)
	if got, want := h.Type(), types.TypeReplyAlertLinkedV1; got != want {
		t.Errorf("Type=%q, want %q", got, want)
	}
}

func TestLinkedHandler_HappyPath(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewLinkedHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertLinkedV1, types.ReplyAlertLinkedV1Data{
		AlertID: alert.ID,
		Kind:    "jira",
		ID:      "INC-1234",
		URL:     "https://jira.example.com/browse/INC-1234",
		Title:   "Investigate 503s",
	})
	entry := &reply.Entry{AlertID: alert.ID}

	if err := h.Handle(context.Background(), ev, entry, "oidc-sub-carol"); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	events, err := s.Alerts().ListEvents(context.Background(), alert.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events=%d, want 1", len(events))
	}
	if events[0].EventType != domain.EventReplyLinked {
		t.Errorf("EventType=%q, want %q", events[0].EventType, domain.EventReplyLinked)
	}
	var p LinkedPayload
	if err := json.Unmarshal(events[0].Detail, &p); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if p.Kind != "jira" || p.ID != "INC-1234" || p.Sub != "oidc-sub-carol" {
		t.Errorf("payload mismatch: %+v", p)
	}
}

func TestLinkedHandler_CrossAlertHijack(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewLinkedHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertLinkedV1, types.ReplyAlertLinkedV1Data{
		AlertID: "someone-else",
		Kind:    "jira",
		ID:      "X-1",
	})
	entry := &reply.Entry{AlertID: alert.ID}

	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected cross-alert error")
	}
	evs, _ := s.Alerts().ListEvents(context.Background(), alert.ID)
	if len(evs) != 0 {
		t.Fatalf("events=%d, want 0 after rejection", len(evs))
	}
}

func TestLinkedHandler_BadData(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewLinkedHandler(s)

	// kind as an integer violates the typed struct's string field.
	ev := newReplyEventRaw(t, types.TypeReplyAlertLinkedV1, []byte(`{"kind":42}`))
	entry := &reply.Entry{AlertID: alert.ID}

	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestLinkedHandler_InvalidKind(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewLinkedHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertLinkedV1, types.ReplyAlertLinkedV1Data{
		AlertID: alert.ID,
		Kind:    "facebook",
		ID:      "abc",
	})
	entry := &reply.Entry{AlertID: alert.ID}

	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected invalid-kind error")
	}
}

func TestLinkedHandler_MissingID(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewLinkedHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertLinkedV1, types.ReplyAlertLinkedV1Data{
		AlertID: alert.ID,
		Kind:    "jira",
	})
	entry := &reply.Entry{AlertID: alert.ID}

	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected missing-id error")
	}
}

func TestLinkedHandler_AllAllowedKinds(t *testing.T) {
	allowed := []string{"jira", "pagerduty", "slack_thread", "github_issue", "gitlab_issue", "linear", "servicenow", "other"}
	for _, k := range allowed {
		t.Run(k, func(t *testing.T) {
			s := newTestStore(t)
			alert := seedAlert(t, s)
			h := NewLinkedHandler(s)

			ev := newReplyEvent(t, types.TypeReplyAlertLinkedV1, types.ReplyAlertLinkedV1Data{
				AlertID: alert.ID,
				Kind:    k,
				ID:      "x",
			})
			entry := &reply.Entry{AlertID: alert.ID}
			if err := h.Handle(context.Background(), ev, entry, ""); err != nil {
				t.Fatalf("kind=%q: Handle: %v", k, err)
			}
		})
	}
}
