package handlers

import (
	"context"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func TestAckWithContextHandler_HappyPath(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewAckWithContextHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertAckWithContextV1, types.ReplyAlertAckWithContextV1Data{
		AlertID:     alert.ID,
		ReasonCode:  "investigating",
		Note:        "paged on-call",
		ReferenceID: "JIRA-42",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}

	if err := h.Handle(context.Background(), ev, entry, "sub-alice"); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	a, _ := s.Alerts().GetByID(context.Background(), alert.ID)
	if a.Status != domain.AlertAcknowledged {
		t.Errorf("alert status=%q, want acknowledged", a.Status)
	}
	if a.AcknowledgedBy != "sub-alice" {
		t.Errorf("acked_by=%q, want sub-alice", a.AcknowledgedBy)
	}
}

func TestAckWithContextHandler_InvalidReasonCode(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewAckWithContextHandler(s)
	ev := newReplyEvent(t, types.TypeReplyAlertAckWithContextV1, types.ReplyAlertAckWithContextV1Data{
		AlertID:    alert.ID,
		ReasonCode: "because_i_said_so",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected invalid_reason_code rejection")
	}
}
