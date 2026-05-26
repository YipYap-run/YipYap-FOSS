package handlers

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func TestEscalatedHandler_TrustFlagRequired(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewEscalatedHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertEscalatedV1, types.ReplyAlertEscalatedV1Data{
		AlertID:     alert.ID,
		NewSeverity: "critical",
		Reason:      "attempted",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}

	// Default cfg: TrustElevated=false → reject.
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{})
	if err := h.Handle(ctx, ev, entry, ""); err == nil {
		t.Fatal("expected trust-flag-required rejection")
	}
	// Alert severity unchanged.
	got, _ := s.Alerts().GetByID(context.Background(), alert.ID)
	if got.Severity != alert.Severity {
		t.Errorf("severity mutated despite trust-flag rejection: %q", got.Severity)
	}
}

func TestEscalatedHandler_HappyPath(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	alert.Severity = domain.SeverityWarning
	if err := s.Alerts().Update(context.Background(), alert); err != nil {
		t.Fatalf("seed Update: %v", err)
	}
	h := NewEscalatedHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertEscalatedV1, types.ReplyAlertEscalatedV1Data{
		AlertID:     alert.ID,
		NewSeverity: "critical",
		Reason:      "pager on fire",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{TrustElevated: true})

	if err := h.Handle(ctx, ev, entry, "sub-ops"); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	got, _ := s.Alerts().GetByID(context.Background(), alert.ID)
	if got.Severity != domain.SeverityCritical {
		t.Errorf("severity=%q, want critical", got.Severity)
	}
}

func TestEscalatedHandler_InvalidSeverity(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewEscalatedHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertEscalatedV1, types.ReplyAlertEscalatedV1Data{
		AlertID:     alert.ID,
		NewSeverity: "apocalyptic",
		Reason:      "fictitious",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{TrustElevated: true})

	if err := h.Handle(ctx, ev, entry, ""); err == nil {
		t.Fatal("expected invalid-severity rejection")
	}
}

// TestEscalatedHandler_StepIndexWritesStepUUID verifies H-19: when a reply
// supplies step_index, the handler looks up the step on the alert's
// monitor's escalation policy and writes the step UUID (not the stringified
// integer) to alerts.current_escalation_step. Otherwise the engine's
// policy-step lookup (matching step.ID) fails and the alert falls out of
// escalation entirely.
func TestEscalatedHandler_StepIndexWritesStepUUID(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)

	// Seed an escalation policy + steps; wire the monitor to it.
	policy := &domain.EscalationPolicy{
		ID:    uuid.New().String(),
		OrgID: alert.OrgID,
		Name:  "default",
	}
	if err := s.EscalationPolicies().Create(context.Background(), policy); err != nil {
		t.Fatalf("EscalationPolicies.Create: %v", err)
	}
	step0ID := uuid.New().String()
	step1ID := uuid.New().String()
	step2ID := uuid.New().String()
	if err := s.EscalationPolicies().ReplaceSteps(context.Background(), policy.ID,
		[]domain.EscalationStep{
			{ID: step0ID, PolicyID: policy.ID, Position: 0, WaitSeconds: 60},
			{ID: step1ID, PolicyID: policy.ID, Position: 1, WaitSeconds: 120},
			{ID: step2ID, PolicyID: policy.ID, Position: 2, WaitSeconds: 300},
		},
		map[string][]domain.StepTarget{},
	); err != nil {
		t.Fatalf("ReplaceSteps: %v", err)
	}
	mon, _ := s.Monitors().GetByID(context.Background(), alert.MonitorID)
	mon.EscalationPolicyID = policy.ID
	if err := s.Monitors().Update(context.Background(), mon); err != nil {
		t.Fatalf("Monitors.Update: %v", err)
	}

	h := NewEscalatedHandler(s)
	ev := newReplyEvent(t, types.TypeReplyAlertEscalatedV1, types.ReplyAlertEscalatedV1Data{
		AlertID:   alert.ID,
		StepIndex: 2,
		Reason:    "manual escalation",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{TrustElevated: true})

	if err := h.Handle(ctx, ev, entry, ""); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	got, _ := s.Alerts().GetByID(context.Background(), alert.ID)
	if got.CurrentEscalationStep != step2ID {
		t.Errorf("CurrentEscalationStep=%q, want step UUID %q", got.CurrentEscalationStep, step2ID)
	}
}

// TestEscalatedHandler_StepIndexOutOfRangeRejected verifies that an
// attacker-supplied step_index exceeding the policy length is rejected.
func TestEscalatedHandler_StepIndexOutOfRangeRejected(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	policy := &domain.EscalationPolicy{ID: uuid.New().String(), OrgID: alert.OrgID, Name: "p"}
	if err := s.EscalationPolicies().Create(context.Background(), policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}
	if err := s.EscalationPolicies().ReplaceSteps(context.Background(), policy.ID,
		[]domain.EscalationStep{{ID: uuid.New().String(), PolicyID: policy.ID, Position: 0, WaitSeconds: 60}},
		map[string][]domain.StepTarget{}); err != nil {
		t.Fatalf("replace steps: %v", err)
	}
	mon, _ := s.Monitors().GetByID(context.Background(), alert.MonitorID)
	mon.EscalationPolicyID = policy.ID
	_ = s.Monitors().Update(context.Background(), mon)

	h := NewEscalatedHandler(s)
	ev := newReplyEvent(t, types.TypeReplyAlertEscalatedV1, types.ReplyAlertEscalatedV1Data{
		AlertID:   alert.ID,
		StepIndex: 9999,
		Reason:    "out of range",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{TrustElevated: true})
	if err := h.Handle(ctx, ev, entry, ""); err == nil {
		t.Fatal("expected invalid_step_index rejection")
	}
}

// TestEscalatedHandler_StepIndexNoPolicyRejected verifies rejection when
// the alert's monitor has no escalation policy wired at all.
func TestEscalatedHandler_StepIndexNoPolicyRejected(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s) // seedAlert does not attach a policy
	h := NewEscalatedHandler(s)
	ev := newReplyEvent(t, types.TypeReplyAlertEscalatedV1, types.ReplyAlertEscalatedV1Data{
		AlertID:   alert.ID,
		StepIndex: 1,
		Reason:    "no policy on monitor",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{TrustElevated: true})
	if err := h.Handle(ctx, ev, entry, ""); err == nil {
		t.Fatal("expected invalid_step_index rejection for monitor with no policy")
	}
}


func TestEscalatedHandler_NoMutationRejected(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	h := NewEscalatedHandler(s)

	ev := newReplyEvent(t, types.TypeReplyAlertEscalatedV1, types.ReplyAlertEscalatedV1Data{
		AlertID: alert.ID,
		Reason:  "no-op",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}
	ctx := reply.WithConfig(context.Background(), reply.DispatcherConfig{TrustElevated: true})

	if err := h.Handle(ctx, ev, entry, ""); err == nil {
		t.Fatal("expected no-mutation rejection")
	}
}
