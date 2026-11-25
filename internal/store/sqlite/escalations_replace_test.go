package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/google/uuid"
)

// A ReplaceSteps call that fails partway (here: a duplicate target id) must
// roll back, leaving the policy's previous steps and targets in place.
func TestReplaceStepsFailureKeepsOldSteps(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	policy := createTestEscalationPolicy(t, s, org.ID)

	oldStep := uuid.New().String()
	oldTarget := uuid.New().String()
	if err := s.EscalationPolicies().ReplaceSteps(ctx, policy.ID,
		[]domain.EscalationStep{{ID: oldStep, PolicyID: policy.ID, Position: 0, WaitSeconds: 300}},
		map[string][]domain.StepTarget{oldStep: {{ID: oldTarget, StepID: oldStep, TargetType: domain.TargetUser, TargetID: "u1"}}},
	); err != nil {
		t.Fatal(err)
	}

	newStep := uuid.New().String()
	dup := uuid.New().String()
	err := s.EscalationPolicies().ReplaceSteps(ctx, policy.ID,
		[]domain.EscalationStep{{ID: newStep, PolicyID: policy.ID, Position: 0, WaitSeconds: 60}},
		map[string][]domain.StepTarget{newStep: {
			{ID: dup, StepID: newStep, TargetType: domain.TargetUser, TargetID: "u2"},
			{ID: dup, StepID: newStep, TargetType: domain.TargetUser, TargetID: "u3"},
		}},
	)
	if err == nil {
		t.Fatal("expected duplicate target id to fail")
	}

	steps, err := s.EscalationPolicies().GetSteps(ctx, policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].ID != oldStep || steps[0].WaitSeconds != 300 {
		t.Fatalf("old steps not intact after failed replace: %+v", steps)
	}
	targets, err := s.EscalationPolicies().GetTargets(ctx, oldStep)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].ID != oldTarget {
		t.Fatalf("old targets not intact after failed replace: %+v", targets)
	}
}

// ReplaceSteps inside Store.Tx must reuse the outer transaction (not open a
// second one on the single SQLite connection, which would deadlock).
func TestReplaceStepsInsideOuterTx(t *testing.T) {
	s := setupTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	org := createTestOrg(t, s)
	policy := createTestEscalationPolicy(t, s, org.ID)
	stepID := uuid.New().String()
	err := s.Tx(ctx, func(tx store.Store) error {
		return tx.EscalationPolicies().ReplaceSteps(ctx, policy.ID,
			[]domain.EscalationStep{{ID: stepID, PolicyID: policy.ID, Position: 0}}, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	steps, _ := s.EscalationPolicies().GetSteps(ctx, policy.ID)
	if len(steps) != 1 || steps[0].ID != stepID {
		t.Fatalf("steps = %+v", steps)
	}
}

func TestCountOpenAlerts(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	policy := createTestEscalationPolicy(t, s, org.ID)
	mon := createTestMonitor(t, s, org.ID)
	mon.EscalationPolicyID = policy.ID
	if err := s.Monitors().Update(ctx, mon); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, st := range []domain.AlertStatus{domain.AlertResolved, domain.AlertFiring} {
		a := &domain.Alert{ID: uuid.New().String(), MonitorID: mon.ID, OrgID: org.ID, Status: st, Severity: domain.SeverityCritical, StartedAt: now}
		if err := s.Alerts().Create(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.EscalationPolicies().CountOpenAlerts(ctx, policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("open alerts = %d, want 1", n)
	}
}
