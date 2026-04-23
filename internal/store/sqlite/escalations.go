package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

type escalationPolicyStore struct{ q queryable }

func (s *escalationPolicyStore) Create(ctx context.Context, p *domain.EscalationPolicy) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO escalation_policies (id, org_id, name, loop, max_loops)
		 VALUES (?, ?, ?, ?, ?)`,
		p.ID, p.OrgID, p.Name, boolToInt(p.Loop), p.MaxLoops)
	return err
}

func (s *escalationPolicyStore) GetByID(ctx context.Context, id string) (*domain.EscalationPolicy, error) {
	var p domain.EscalationPolicy
	var loop int
	err := s.q.QueryRowContext(ctx,
		`SELECT id, org_id, name, loop, max_loops FROM escalation_policies WHERE id = ?`, id).
		Scan(&p.ID, &p.OrgID, &p.Name, &loop, &p.MaxLoops)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("escalation policy not found")
		}
		return nil, err
	}
	p.Loop = loop != 0
	return &p, nil
}

func (s *escalationPolicyStore) ListByOrg(ctx context.Context, orgID string, params store.ListParams) ([]*domain.EscalationPolicy, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.q.QueryContext(ctx,
		`SELECT id, org_id, name, loop, max_loops FROM escalation_policies
		 WHERE org_id = ? ORDER BY name LIMIT ? OFFSET ?`,
		orgID, limit, params.Offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.EscalationPolicy
	for rows.Next() {
		var p domain.EscalationPolicy
		var loop int
		if err := rows.Scan(&p.ID, &p.OrgID, &p.Name, &loop, &p.MaxLoops); err != nil {
			return nil, err
		}
		p.Loop = loop != 0
		out = append(out, &p)
	}
	return out, rows.Err()
}

func (s *escalationPolicyStore) Update(ctx context.Context, p *domain.EscalationPolicy) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE escalation_policies SET name = ?, loop = ?, max_loops = ? WHERE id = ?`,
		p.Name, boolToInt(p.Loop), p.MaxLoops, p.ID)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

func (s *escalationPolicyStore) Delete(ctx context.Context, id string) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM escalation_policies WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

func (s *escalationPolicyStore) ReplaceSteps(ctx context.Context, policyID string, steps []domain.EscalationStep, targets map[string][]domain.StepTarget) error {
	// Delete existing steps (cascades to targets).
	if _, err := s.q.ExecContext(ctx, `DELETE FROM escalation_steps WHERE policy_id = ?`, policyID); err != nil {
		return err
	}
	for _, step := range steps {
		if _, err := s.q.ExecContext(ctx,
			`INSERT INTO escalation_steps (id, policy_id, position, wait_seconds, repeat_count, repeat_interval_seconds, is_terminal)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			step.ID, policyID, step.Position, step.WaitSeconds, step.RepeatCount, step.RepeatIntervalSeconds, boolToInt(step.IsTerminal)); err != nil {
			return err
		}
		for _, t := range targets[step.ID] {
			if _, err := s.q.ExecContext(ctx,
				`INSERT INTO step_targets (id, step_id, target_type, target_id, channel_id, simultaneous)
				 VALUES (?, ?, ?, ?, ?, ?)`,
				t.ID, step.ID, string(t.TargetType), t.TargetID, t.ChannelID, boolToInt(t.Simultaneous)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *escalationPolicyStore) GetSteps(ctx context.Context, policyID string) ([]domain.EscalationStep, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT id, policy_id, position, wait_seconds, repeat_count, repeat_interval_seconds, is_terminal
		 FROM escalation_steps WHERE policy_id = ? ORDER BY position`, policyID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []domain.EscalationStep
	for rows.Next() {
		var st domain.EscalationStep
		var isTerminal int
		if err := rows.Scan(&st.ID, &st.PolicyID, &st.Position, &st.WaitSeconds, &st.RepeatCount, &st.RepeatIntervalSeconds, &isTerminal); err != nil {
			return nil, err
		}
		st.IsTerminal = isTerminal != 0
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *escalationPolicyStore) GetTargets(ctx context.Context, stepID string) ([]domain.StepTarget, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT id, step_id, target_type, target_id, channel_id, simultaneous
		 FROM step_targets WHERE step_id = ?`, stepID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []domain.StepTarget
	for rows.Next() {
		var t domain.StepTarget
		var targetType string
		var simultaneous int
		if err := rows.Scan(&t.ID, &t.StepID, &targetType, &t.TargetID, &t.ChannelID, &simultaneous); err != nil {
			return nil, err
		}
		t.TargetType = domain.TargetType(targetType)
		t.Simultaneous = simultaneous != 0
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *escalationPolicyStore) GetNextStep(ctx context.Context, policyID string, currentPosition int) (*domain.EscalationStep, error) {
	var st domain.EscalationStep
	var isTerminal int
	err := s.q.QueryRowContext(ctx,
		`SELECT id, policy_id, position, wait_seconds, repeat_count, repeat_interval_seconds, is_terminal
		 FROM escalation_steps WHERE policy_id = ? AND position > ? ORDER BY position LIMIT 1`,
		policyID, currentPosition).
		Scan(&st.ID, &st.PolicyID, &st.Position, &st.WaitSeconds, &st.RepeatCount, &st.RepeatIntervalSeconds, &isTerminal)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // no next step
		}
		return nil, err
	}
	st.IsTerminal = isTerminal != 0
	return &st, nil
}
