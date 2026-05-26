package sqlite

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func TestEscalationPolicyCreateAndGet(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)

	p := &domain.EscalationPolicy{
		ID:    uuid.New().String(),
		OrgID: org.ID,
		Name:  "Critical Policy",
		Loop:  true,
	}
	if err := s.EscalationPolicies().Create(ctx, p); err != nil {
		t.Fatal(err)
	}

	got, err := s.EscalationPolicies().GetByID(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Critical Policy" || !got.Loop {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestEscalationReplaceSteps(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	policy := createTestEscalationPolicy(t, s, org.ID)

	step1ID := uuid.New().String()
	step2ID := uuid.New().String()
	steps := []domain.EscalationStep{
		{ID: step1ID, PolicyID: policy.ID, Position: 0, WaitSeconds: 300, RepeatCount: 2},
		{ID: step2ID, PolicyID: policy.ID, Position: 1, WaitSeconds: 600, IsTerminal: true},
	}
	targets := map[string][]domain.StepTarget{
		step1ID: {
			{ID: uuid.New().String(), StepID: step1ID, TargetType: domain.TargetOnCallPrimary, ChannelID: "sms", Simultaneous: true},
		},
	}

	if err := s.EscalationPolicies().ReplaceSteps(ctx, policy.ID, steps, targets); err != nil {
		t.Fatal(err)
	}

	gotSteps, err := s.EscalationPolicies().GetSteps(ctx, policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSteps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(gotSteps))
	}
	if gotSteps[1].IsTerminal != true {
		t.Fatal("expected step 2 to be terminal")
	}

	gotTargets, err := s.EscalationPolicies().GetTargets(ctx, step1ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotTargets) != 1 || gotTargets[0].ChannelID != "sms" {
		t.Fatalf("unexpected targets: %+v", gotTargets)
	}
}

func TestEscalationGetNextStep(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	policy := createTestEscalationPolicy(t, s, org.ID)

	steps := []domain.EscalationStep{
		{ID: uuid.New().String(), PolicyID: policy.ID, Position: 0, WaitSeconds: 300},
		{ID: uuid.New().String(), PolicyID: policy.ID, Position: 1, WaitSeconds: 600},
	}
	if err := s.EscalationPolicies().ReplaceSteps(ctx, policy.ID, steps, nil); err != nil {
		t.Fatal(err)
	}

	next, err := s.EscalationPolicies().GetNextStep(ctx, policy.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if next == nil || next.Position != 1 {
		t.Fatalf("expected step at position 1, got %+v", next)
	}

	// No step after position 1.
	next, err = s.EscalationPolicies().GetNextStep(ctx, policy.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if next != nil {
		t.Fatalf("expected nil, got %+v", next)
	}
}
