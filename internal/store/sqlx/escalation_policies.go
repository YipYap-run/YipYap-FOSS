package sqlx

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// EscalationPolicyStore implements store.EscalationPolicyStore across SQL
// backends.
type EscalationPolicyStore struct {
	q Queryable
	d Dialect
}

// NewEscalationPolicyStore returns an EscalationPolicyStore bound to q.
func NewEscalationPolicyStore(q Queryable, d Dialect) *EscalationPolicyStore {
	return &EscalationPolicyStore{q: q, d: d}
}

func (s *EscalationPolicyStore) Create(ctx context.Context, p *domain.EscalationPolicy) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO escalation_policies (id, org_id, name, description, loop, max_loops)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		p.ID, p.OrgID, p.Name, p.Description, s.d.BoolArg(p.Loop), p.MaxLoops)
	return err
}

func (s *EscalationPolicyStore) GetByID(ctx context.Context, id string) (*domain.EscalationPolicy, error) {
	var p domain.EscalationPolicy
	var loop Bool
	err := s.q.QueryRowContext(ctx,
		`SELECT id, org_id, name, description, loop, max_loops FROM escalation_policies WHERE id = ?`, id).
		Scan(&p.ID, &p.OrgID, &p.Name, &p.Description, &loop, &p.MaxLoops)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("escalation policy not found")
		}
		return nil, err
	}
	p.Loop = bool(loop)
	return &p, nil
}

func (s *EscalationPolicyStore) ListByOrg(ctx context.Context, orgID string, params store.ListParams) ([]*domain.EscalationPolicy, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.q.QueryContext(ctx,
		`SELECT id, org_id, name, description, loop, max_loops FROM escalation_policies
		 WHERE org_id = ? ORDER BY name LIMIT ? OFFSET ?`,
		orgID, limit, params.Offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.EscalationPolicy
	for rows.Next() {
		var p domain.EscalationPolicy
		var loop Bool
		if err := rows.Scan(&p.ID, &p.OrgID, &p.Name, &p.Description, &loop, &p.MaxLoops); err != nil {
			return nil, err
		}
		p.Loop = bool(loop)
		out = append(out, &p)
	}
	return out, rows.Err()
}

func (s *EscalationPolicyStore) Update(ctx context.Context, p *domain.EscalationPolicy) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE escalation_policies SET name = ?, description = ?, loop = ?, max_loops = ? WHERE id = ?`,
		p.Name, p.Description, s.d.BoolArg(p.Loop), p.MaxLoops, p.ID)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

func (s *EscalationPolicyStore) Delete(ctx context.Context, id string) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM escalation_policies WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

// ReplaceSteps swaps the policy's steps and targets in one transaction, so a
// failed insert leaves the previous steps in place instead of an empty or
// half-written policy.
func (s *EscalationPolicyStore) ReplaceSteps(ctx context.Context, policyID string, steps []domain.EscalationStep, targets map[string][]domain.StepTarget) error {
	return InTx(ctx, s.q, func(q Queryable) error {
		if _, err := q.ExecContext(ctx, `DELETE FROM escalation_steps WHERE policy_id = ?`, policyID); err != nil {
			return err
		}
		for _, step := range steps {
			if _, err := q.ExecContext(ctx,
				`INSERT INTO escalation_steps (id, policy_id, position, wait_seconds, repeat_count, repeat_interval_seconds, is_terminal)
				 VALUES (?, ?, ?, ?, ?, ?, ?)`,
				step.ID, policyID, step.Position, step.WaitSeconds, step.RepeatCount, step.RepeatIntervalSeconds, s.d.BoolArg(step.IsTerminal)); err != nil {
				return err
			}
			for _, t := range targets[step.ID] {
				if _, err := q.ExecContext(ctx,
					`INSERT INTO step_targets (id, step_id, target_type, target_id, channel_id, simultaneous)
					 VALUES (?, ?, ?, ?, ?, ?)`,
					t.ID, step.ID, string(t.TargetType), t.TargetID, t.ChannelID, s.d.BoolArg(t.Simultaneous)); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// CountOpenAlerts returns how many unresolved alerts belong to monitors that
// use the policy. The SPA warns before a step save when this is non-zero.
func (s *EscalationPolicyStore) CountOpenAlerts(ctx context.Context, policyID string) (int, error) {
	var n int
	err := s.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM alerts a
		 JOIN monitors m ON m.id = a.monitor_id
		 WHERE m.escalation_policy_id = ? AND a.status <> ?`,
		policyID, string(domain.AlertResolved)).Scan(&n)
	return n, err
}

func (s *EscalationPolicyStore) GetSteps(ctx context.Context, policyID string) ([]domain.EscalationStep, error) {
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
		var isTerminal Bool
		if err := rows.Scan(&st.ID, &st.PolicyID, &st.Position, &st.WaitSeconds, &st.RepeatCount, &st.RepeatIntervalSeconds, &isTerminal); err != nil {
			return nil, err
		}
		st.IsTerminal = bool(isTerminal)
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *EscalationPolicyStore) GetTargets(ctx context.Context, stepID string) ([]domain.StepTarget, error) {
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
		var simultaneous Bool
		if err := rows.Scan(&t.ID, &t.StepID, &targetType, &t.TargetID, &t.ChannelID, &simultaneous); err != nil {
			return nil, err
		}
		t.TargetType = domain.TargetType(targetType)
		t.Simultaneous = bool(simultaneous)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *EscalationPolicyStore) GetNextStep(ctx context.Context, policyID string, currentPosition int) (*domain.EscalationStep, error) {
	var st domain.EscalationStep
	var isTerminal Bool
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
	st.IsTerminal = bool(isTerminal)
	return &st, nil
}
