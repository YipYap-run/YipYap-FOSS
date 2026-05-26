package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
)

func TestRemediationStartedHandler_HappyPath(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewRemediationStartedHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertRemediationStartedV1, types.ReplyAlertRemediationStartedV1Data{
		AlertID:                 alert.ID,
		RemediationID:           "rem-1",
		Description:             "restarting container",
		ExpectedDurationSeconds: 120,
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{})

	if err := h.Handle(ctx, ev, entry, "sub-bob"); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	rows, err := s.AlertRemediations().ListActiveForAlert(context.Background(), alert.ID, time.Now())
	if err != nil {
		t.Fatalf("ListActiveForAlert: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len=%d, want 1", len(rows))
	}
	if rows[0].Description != "restarting container" {
		t.Errorf("description=%q", rows[0].Description)
	}
}

func TestRemediationStartedHandler_DurationClamped(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewRemediationStartedHandler(s)

	// Request 24h; default cap is 600s.
	ev := newReplyEvent(t, types.TypeReplyAlertRemediationStartedV1, types.ReplyAlertRemediationStartedV1Data{
		AlertID:                 alert.ID,
		RemediationID:           "rem-long",
		Description:             "too long",
		ExpectedDurationSeconds: 86400,
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{})

	if err := h.Handle(ctx, ev, entry, ""); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	rows, _ := s.AlertRemediations().ListActiveForAlert(context.Background(), alert.ID, time.Now())
	if len(rows) != 1 {
		t.Fatalf("len=%d, want 1", len(rows))
	}
	if got := time.Until(rows[0].ExpiresAt).Seconds(); got > 700 {
		t.Errorf("duration not clamped: expires in %vs", got)
	}
}

func TestRemediationStartedHandler_MissingRemediationID(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewRemediationStartedHandler(s)
	ev := newReplyEvent(t, types.TypeReplyAlertRemediationStartedV1, types.ReplyAlertRemediationStartedV1Data{
		AlertID:     alert.ID,
		Description: "missing id",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected missing_remediation_id error")
	}
}

func TestRemediationStartedHandler_CrossAlertRejected(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewRemediationStartedHandler(s)
	ev := newReplyEvent(t, types.TypeReplyAlertRemediationStartedV1, types.ReplyAlertRemediationStartedV1Data{
		AlertID:       "someone-else",
		RemediationID: "rem-x",
		Description:   "hijack",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected cross-alert rejection")
	}
}
