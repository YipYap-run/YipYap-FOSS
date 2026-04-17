package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

type monitorGroupStore struct{ q queryable }

func (s *monitorGroupStore) Create(ctx context.Context, g *domain.MonitorGroup) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO monitor_groups (id, org_id, name, description, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		g.ID, g.OrgID, g.Name, g.Description,
		g.CreatedAt.UTC().Format(timeFormat),
		g.UpdatedAt.UTC().Format(timeFormat))
	return err
}

func (s *monitorGroupStore) GetByID(ctx context.Context, id string) (*domain.MonitorGroup, error) {
	var g domain.MonitorGroup
	var createdAt, updatedAt string
	err := s.q.QueryRowContext(ctx,
		`SELECT id, org_id, name, description, created_at, updated_at
		 FROM monitor_groups WHERE id = ?`, id).
		Scan(&g.ID, &g.OrgID, &g.Name, &g.Description, &createdAt, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("monitor group not found")
		}
		return nil, err
	}
	g.CreatedAt = mustParseTime(createdAt)
	g.UpdatedAt = mustParseTime(updatedAt)
	return &g, nil
}

func (s *monitorGroupStore) ListByOrg(ctx context.Context, orgID string) ([]*domain.MonitorGroup, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT id, org_id, name, description, created_at, updated_at
		 FROM monitor_groups WHERE org_id = ? ORDER BY name`, orgID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.MonitorGroup
	for rows.Next() {
		var g domain.MonitorGroup
		var createdAt, updatedAt string
		if err := rows.Scan(&g.ID, &g.OrgID, &g.Name, &g.Description, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		g.CreatedAt = mustParseTime(createdAt)
		g.UpdatedAt = mustParseTime(updatedAt)
		out = append(out, &g)
	}
	return out, rows.Err()
}

func (s *monitorGroupStore) Update(ctx context.Context, g *domain.MonitorGroup) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE monitor_groups SET name = ?, description = ?, updated_at = ? WHERE id = ?`,
		g.Name, g.Description, g.UpdatedAt.UTC().Format(timeFormat), g.ID)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

func (s *monitorGroupStore) Delete(ctx context.Context, id string) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM monitor_groups WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

// MonitorGroups returns the monitor group sub-store.
func (s *SQLiteStore) MonitorGroups() store.MonitorGroupStore {
	return &monitorGroupStore{q: s.q}
}
