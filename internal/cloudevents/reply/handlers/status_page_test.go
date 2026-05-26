package handlers

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
)

func TestStatusPageHandler_HappyPath(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewStatusPageHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertStatusPageV1, types.ReplyAlertStatusPageV1Data{
		AlertID:       alert.ID,
		Provider:      "statuspage_io",
		URL:           "https://status.example.com/incidents/abc",
		PublicMessage: "investigating elevated error rates",
		UpdatedAt:     time.Now().UTC(),
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}

	if err := h.Handle(context.Background(), ev, entry, ""); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	refs, err := s.AlertStatusPageRefs().ListByAlert(context.Background(), alert.ID)
	if err != nil {
		t.Fatalf("ListByAlert: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("refs=%d, want 1", len(refs))
	}
}

func TestStatusPageHandler_InvalidProvider(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewStatusPageHandler(s)
	ev := newReplyEvent(t, types.TypeReplyAlertStatusPageV1, types.ReplyAlertStatusPageV1Data{
		AlertID:  alert.ID,
		Provider: "geocities",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected invalid_provider rejection")
	}
}

func TestStatusPageHandler_PublicMessageTruncated(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewStatusPageHandler(s)

	long := strings.Repeat("x", 5000)
	ev := newReplyEvent(t, types.TypeReplyAlertStatusPageV1, types.ReplyAlertStatusPageV1Data{
		AlertID:       alert.ID,
		Provider:      "statuspage_io",
		URL:           "https://status.example.com/",
		PublicMessage: long,
		UpdatedAt:     time.Now().UTC(),
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	if err := h.Handle(context.Background(), ev, entry, ""); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	refs, _ := s.AlertStatusPageRefs().ListByAlert(context.Background(), alert.ID)
	if len(refs) != 1 {
		t.Fatalf("refs=%d", len(refs))
	}
	if len(refs[0].PublicMessage) > 2048 {
		t.Errorf("PublicMessage not truncated to 2KiB: len=%d", len(refs[0].PublicMessage))
	}
}
