package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

type orgSettingsStore struct{ q queryable }

func (s *orgSettingsStore) Get(ctx context.Context, orgID, key string) (string, error) {
	var value string
	err := s.q.QueryRowContext(ctx,
		`SELECT value FROM org_settings WHERE org_id = ? AND key = ?`,
		orgID, key).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("org setting not found: %s/%s", orgID, key)
		}
		return "", err
	}
	return value, nil
}

func (s *orgSettingsStore) Set(ctx context.Context, orgID, key, value string) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO org_settings (org_id, key, value) VALUES (?, ?, ?)
		 ON CONFLICT(org_id, key) DO UPDATE SET value = excluded.value`,
		orgID, key, value)
	return err
}

func (s *orgSettingsStore) Delete(ctx context.Context, orgID, key string) error {
	res, err := s.q.ExecContext(ctx,
		`DELETE FROM org_settings WHERE org_id = ? AND key = ?`,
		orgID, key)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

func (s *orgSettingsStore) GetAll(ctx context.Context, orgID string) (map[string]string, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT key, value FROM org_settings WHERE org_id = ?`, orgID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}
