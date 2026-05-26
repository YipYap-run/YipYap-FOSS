package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// seedRemediation creates an in-progress remediation row for an alert.
func seedRemediation(t *testing.T, s store.Store, alertID, remID string) {
	t.Helper()
	now := time.Now().UTC()
	err := s.AlertRemediations().Create(context.Background(), &store.AlertRemediation{
		RemediationID: remID,
		AlertID:       alertID,
		Status:        store.RemediationInProgress,
		StartedAt:     now,
		ExpiresAt:     now.Add(10 * time.Minute),
		Description:   "rolling restart",
	})
	if err != nil {
		t.Fatalf("seedRemediation: %v", err)
	}
}

func TestRemediationResultHandler_HappyPath_Failure(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	seedRemediation(t, s, alert.ID, "rem-1")
	h := NewRemediationResultHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertRemediationResultV1, types.ReplyAlertRemediationResultV1Data{
		AlertID:       alert.ID,
		RemediationID: "rem-1",
		Outcome:       "failure",
		Message:       "oom",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}

	if err := h.Handle(context.Background(), ev, entry, ""); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	got, _ := s.AlertRemediations().GetByID(context.Background(), "rem-1")
	if got.Status != store.RemediationFailure {
		t.Errorf("status=%q, want failure", got.Status)
	}
	// Alert still firing (failure doesn't auto-resolve).
	a, _ := s.Alerts().GetByID(context.Background(), alert.ID)
	if a.Status != domain.AlertFiring {
		t.Errorf("alert status=%q, want firing", a.Status)
	}
}

func TestRemediationResultHandler_InvalidOutcome(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	seedRemediation(t, s, alert.ID, "rem-2")
	h := NewRemediationResultHandler(s)
	ev := newReplyEvent(t, types.TypeReplyAlertRemediationResultV1, types.ReplyAlertRemediationResultV1Data{
		AlertID:       alert.ID,
		RemediationID: "rem-2",
		Outcome:       "cataclysm",
		Message:       "?",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected invalid_outcome rejection")
	}
}

func TestRemediationResultHandler_UnknownRemediation(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewRemediationResultHandler(s)
	ev := newReplyEvent(t, types.TypeReplyAlertRemediationResultV1, types.ReplyAlertRemediationResultV1Data{
		AlertID:       alert.ID,
		RemediationID: "does-not-exist",
		Outcome:       "success",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected unknown_remediation rejection")
	}
}

func TestRemediationResultHandler_AutoResolveWhenMonitorUp(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	seedRemediation(t, s, alert.ID, "rem-autores")

	// Seed a monitor check with status=up.
	if err := s.Checks().Create(context.Background(), &domain.MonitorCheck{
		ID:        uuid.New().String(),
		MonitorID: alert.MonitorID,
		Status:    domain.StatusUp,
		CheckedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Checks.Create: %v", err)
	}

	h := NewRemediationResultHandler(s)
	ev := newReplyEvent(t, types.TypeReplyAlertRemediationResultV1, types.ReplyAlertRemediationResultV1Data{
		AlertID:       alert.ID,
		RemediationID: "rem-autores",
		Outcome:       "success",
		Message:       "fixed",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	if err := h.Handle(context.Background(), ev, entry, ""); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	a, _ := s.Alerts().GetByID(context.Background(), alert.ID)
	if a.Status != domain.AlertResolved {
		t.Errorf("alert should be auto-resolved; status=%q", a.Status)
	}
}

func TestRemediationResultHandler_NoAutoResolveWhenMonitorDown(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	seedRemediation(t, s, alert.ID, "rem-noautores")

	if err := s.Checks().Create(context.Background(), &domain.MonitorCheck{
		ID:        uuid.New().String(),
		MonitorID: alert.MonitorID,
		Status:    domain.StatusDown,
		CheckedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Checks.Create: %v", err)
	}

	h := NewRemediationResultHandler(s)
	ev := newReplyEvent(t, types.TypeReplyAlertRemediationResultV1, types.ReplyAlertRemediationResultV1Data{
		AlertID:       alert.ID,
		RemediationID: "rem-noautores",
		Outcome:       "success",
		Message:       "claimed fixed",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	if err := h.Handle(context.Background(), ev, entry, ""); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	a, _ := s.Alerts().GetByID(context.Background(), alert.ID)
	if a.Status == domain.AlertResolved {
		t.Errorf("alert should NOT auto-resolve when monitor still down")
	}
}
