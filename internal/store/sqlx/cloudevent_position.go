package sqlx

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type CloudEventPositionStore struct {
	q Queryable
	d Dialect
}

func NewCloudEventPositionStore(q Queryable, d Dialect) *CloudEventPositionStore {
	return &CloudEventPositionStore{q: q, d: d}
}

func (s *CloudEventPositionStore) Get(ctx context.Context, orgID, key string) (string, bool, error) {
	var cursor string
	err := s.q.QueryRowContext(ctx,
		`SELECT cursor FROM cloudevent_positions WHERE org_id = ? AND key_hash = ?`,
		orgID, key).Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return cursor, true, nil
}

func (s *CloudEventPositionStore) Set(ctx context.Context, orgID, key, cursor string, at time.Time) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO cloudevent_positions (org_id, key_hash, cursor, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (org_id, key_hash) DO UPDATE
		 SET cursor = excluded.cursor, updated_at = excluded.updated_at`,
		orgID, key, cursor, s.d.MillisTimeArg(at.UTC()))
	return err
}

func (s *CloudEventPositionStore) TrimOrg(ctx context.Context, orgID string, keep int) (int64, error) {
	if keep <= 0 {
		return 0, nil
	}
	res, err := s.q.ExecContext(ctx,
		`DELETE FROM cloudevent_positions
		 WHERE org_id = ? AND key_hash NOT IN (
		     SELECT key_hash FROM cloudevent_positions
		     WHERE org_id = ? ORDER BY updated_at DESC LIMIT ?
		 )`,
		orgID, orgID, keep)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *CloudEventPositionStore) PruneBefore(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.q.ExecContext(ctx,
		`DELETE FROM cloudevent_positions WHERE updated_at < ?`,
		s.d.MillisTimeArg(before.UTC()))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
