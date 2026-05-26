package handlers

import (
	"context"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
)

func TestOwnershipHandler_HappyPath_User(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	u := seedUser(t, s, alert.OrgID)
	h := NewOwnershipHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertOwnershipV1, types.ReplyAlertOwnershipV1Data{
		AlertID:   alert.ID,
		OwnerKind: "user",
		OwnerID:   u.ID,
		Reason:    "primary on-call",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}

	if err := h.Handle(context.Background(), ev, entry, "sub-x"); err != nil {
		t.Fatalf("Handle: %v", err)
	}
}

func TestOwnershipHandler_HappyPath_Team(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	team := seedTeam(t, s, alert.OrgID)
	h := NewOwnershipHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertOwnershipV1, types.ReplyAlertOwnershipV1Data{
		AlertID:   alert.ID,
		OwnerKind: "team",
		OwnerID:   team.ID,
		Reason:    "infra team owns this",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}

	if err := h.Handle(context.Background(), ev, entry, ""); err != nil {
		t.Fatalf("Handle: %v", err)
	}
}

func TestOwnershipHandler_InvalidOwnerKind(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewOwnershipHandler(s)
	ev := newReplyEvent(t, types.TypeReplyAlertOwnershipV1, types.ReplyAlertOwnershipV1Data{
		AlertID:   alert.ID,
		OwnerKind: "robot",
		OwnerID:   "x",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected invalid_owner_kind rejection")
	}
}

func TestOwnershipHandler_UnknownUser(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewOwnershipHandler(s)
	ev := newReplyEvent(t, types.TypeReplyAlertOwnershipV1, types.ReplyAlertOwnershipV1Data{
		AlertID:   alert.ID,
		OwnerKind: "user",
		OwnerID:   "nope",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected unknown_owner rejection")
	}
}

func TestOwnershipHandler_CrossOrgRejected(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	other := seedAlert(t, s) // different org
	u := seedUser(t, s, other.OrgID)
	h := NewOwnershipHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertOwnershipV1, types.ReplyAlertOwnershipV1Data{
		AlertID:   alert.ID,
		OwnerKind: "user",
		OwnerID:   u.ID,
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected cross_org_owner rejection")
	}
}
