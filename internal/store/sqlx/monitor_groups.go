package sqlx

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// MonitorGroupStore implements store.MonitorGroupStore across SQL backends.
type MonitorGroupStore struct {
	q Queryable
	d Dialect
}

// NewMonitorGroupStore returns a MonitorGroupStore bound to q with the given
// dialect.
func NewMonitorGroupStore(q Queryable, d Dialect) *MonitorGroupStore {
	return &MonitorGroupStore{q: q, d: d}
}

func (s *MonitorGroupStore) Create(ctx context.Context, g *domain.MonitorGroup) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO monitor_groups (id, org_id, name, description, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		g.ID, g.OrgID, g.Name, g.Description,
		s.d.TimeArg(g.CreatedAt), s.d.TimeArg(g.UpdatedAt))
	return err
}

func (s *MonitorGroupStore) GetByID(ctx context.Context, id string) (*domain.MonitorGroup, error) {
	g, err := scanMonitorGroup(s.q.QueryRowContext(ctx,
		`SELECT id, org_id, name, description, created_at, updated_at
		 FROM monitor_groups WHERE id = ?`, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("monitor group not found")
		}
		return nil, err
	}
	return g, nil
}

func (s *MonitorGroupStore) ListByOrg(ctx context.Context, orgID string) ([]*domain.MonitorGroup, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT id, org_id, name, description, created_at, updated_at
		 FROM monitor_groups WHERE org_id = ? ORDER BY name`, orgID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.MonitorGroup
	for rows.Next() {
		g, err := scanMonitorGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *MonitorGroupStore) Update(ctx context.Context, g *domain.MonitorGroup) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE monitor_groups SET name = ?, description = ?, updated_at = ? WHERE id = ?`,
		g.Name, g.Description, s.d.TimeArg(g.UpdatedAt), g.ID)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

func (s *MonitorGroupStore) Delete(ctx context.Context, id string) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM monitor_groups WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

func scanMonitorGroup(r Scanner) (*domain.MonitorGroup, error) {
	var g domain.MonitorGroup
	var createdAt, updatedAt Time
	if err := r.Scan(&g.ID, &g.OrgID, &g.Name, &g.Description, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	g.CreatedAt = createdAt.T
	g.UpdatedAt = updatedAt.T
	return &g, nil
}
