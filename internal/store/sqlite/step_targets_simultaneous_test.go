package sqlite

import (
	"context"
	"io/fs"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/google/uuid"
)

// Migration 043 marks existing step targets simultaneous, so steps that
// paged every target at once keep doing so now the engine honors the flag
// (mon-12, core-monitoring decision 6).
func TestMigration043StepTargetsSimultaneous(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)

	policy := &domain.EscalationPolicy{ID: uuid.New().String(), OrgID: org.ID, Name: "p"}
	if err := s.EscalationPolicies().Create(ctx, policy); err != nil {
		t.Fatal(err)
	}
	stepID := uuid.New().String()
	if err := s.EscalationPolicies().ReplaceSteps(ctx, policy.ID,
		[]domain.EscalationStep{{ID: stepID, PolicyID: policy.ID}},
		map[string][]domain.StepTarget{stepID: {
			{ID: uuid.New().String(), StepID: stepID, TargetType: domain.TargetChannel},
			{ID: uuid.New().String(), StepID: stepID, TargetType: domain.TargetChannel},
		}}); err != nil {
		t.Fatal(err)
	}

	data, err := fs.ReadFile(migrationsFS, "migrations/061_step_targets_simultaneous.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range store.SplitSQLStatements(string(data)) {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("exec migration: %v", err)
		}
	}
	targets, err := s.EscalationPolicies().GetTargets(ctx, stepID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range targets {
		if !tg.Simultaneous {
			t.Fatalf("target %s still sequential after migration", tg.ID)
		}
	}
}
