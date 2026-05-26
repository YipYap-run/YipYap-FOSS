package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

type teamStore struct{ q queryable }

func (s *teamStore) Create(ctx context.Context, t *domain.Team) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO teams (id, org_id, name) VALUES (?, ?, ?)`,
		t.ID, t.OrgID, t.Name)
	return err
}

func (s *teamStore) GetByID(ctx context.Context, id string) (*domain.Team, error) {
	var t domain.Team
	err := s.q.QueryRowContext(ctx,
		`SELECT id, org_id, name FROM teams WHERE id = ?`, id).
		Scan(&t.ID, &t.OrgID, &t.Name)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("team not found")
		}
		return nil, err
	}
	return &t, nil
}

func (s *teamStore) ListByOrg(ctx context.Context, orgID string, params store.ListParams) ([]*domain.Team, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.q.QueryContext(ctx,
		`SELECT id, org_id, name FROM teams WHERE org_id = ? ORDER BY name LIMIT ? OFFSET ?`,
		orgID, limit, params.Offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.Team
	for rows.Next() {
		var t domain.Team
		if err := rows.Scan(&t.ID, &t.OrgID, &t.Name); err != nil {
			return nil, err
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

func (s *teamStore) Update(ctx context.Context, t *domain.Team) error {
	res, err := s.q.ExecContext(ctx, `UPDATE teams SET name = ? WHERE id = ?`, t.Name, t.ID)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

func (s *teamStore) Delete(ctx context.Context, id string) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM teams WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

func (s *teamStore) AddMember(ctx context.Context, m *domain.TeamMember) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO team_members (team_id, user_id, position) VALUES (?, ?, ?)`,
		m.TeamID, m.UserID, m.Position)
	return err
}

func (s *teamStore) RemoveMember(ctx context.Context, teamID, userID string) error {
	res, err := s.q.ExecContext(ctx,
		`DELETE FROM team_members WHERE team_id = ? AND user_id = ?`, teamID, userID)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

func (s *teamStore) UpdateMember(ctx context.Context, m *domain.TeamMember) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE team_members SET position = ? WHERE team_id = ? AND user_id = ?`,
		m.Position, m.TeamID, m.UserID)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

func (s *teamStore) ListMembers(ctx context.Context, teamID string) ([]*domain.TeamMember, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT team_id, user_id, position FROM team_members WHERE team_id = ? ORDER BY position`,
		teamID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.TeamMember
	for rows.Next() {
		var m domain.TeamMember
		if err := rows.Scan(&m.TeamID, &m.UserID, &m.Position); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}
