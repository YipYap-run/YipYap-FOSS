package sqlx

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// NotificationChannelStore implements store.NotificationChannelStore across
// SQL backends.
type NotificationChannelStore struct {
	q Queryable
	d Dialect
}

// NewNotificationChannelStore returns a NotificationChannelStore bound to q.
func NewNotificationChannelStore(q Queryable, d Dialect) *NotificationChannelStore {
	return &NotificationChannelStore{q: q, d: d}
}

func (s *NotificationChannelStore) Create(ctx context.Context, ch *domain.NotificationChannel) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO notification_channels (id, org_id, type, name, config, enabled)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		ch.ID, ch.OrgID, ch.Type, ch.Name, ch.Config, s.d.BoolArg(ch.Enabled))
	return err
}

func (s *NotificationChannelStore) GetByID(ctx context.Context, id string) (*domain.NotificationChannel, error) {
	var ch domain.NotificationChannel
	var enabled Bool
	err := s.q.QueryRowContext(ctx,
		`SELECT id, org_id, type, name, config, enabled FROM notification_channels WHERE id = ?`, id).
		Scan(&ch.ID, &ch.OrgID, &ch.Type, &ch.Name, &ch.Config, &enabled)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("notification channel not found")
		}
		return nil, err
	}
	ch.Enabled = bool(enabled)
	return &ch, nil
}

func (s *NotificationChannelStore) ListByOrg(ctx context.Context, orgID string, params store.ListParams) ([]*domain.NotificationChannel, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.q.QueryContext(ctx,
		`SELECT id, org_id, type, name, config, enabled FROM notification_channels
		 WHERE org_id = ? ORDER BY name LIMIT ? OFFSET ?`,
		orgID, limit, params.Offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.NotificationChannel
	for rows.Next() {
		var ch domain.NotificationChannel
		var enabled Bool
		if err := rows.Scan(&ch.ID, &ch.OrgID, &ch.Type, &ch.Name, &ch.Config, &enabled); err != nil {
			return nil, err
		}
		ch.Enabled = bool(enabled)
		out = append(out, &ch)
	}
	return out, rows.Err()
}

func (s *NotificationChannelStore) Update(ctx context.Context, ch *domain.NotificationChannel) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE notification_channels SET type = ?, name = ?, config = ?, enabled = ? WHERE id = ?`,
		ch.Type, ch.Name, ch.Config, s.d.BoolArg(ch.Enabled), ch.ID)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

func (s *NotificationChannelStore) Delete(ctx context.Context, id string) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM notification_channels WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}
