package sqlx

import (
	"context"
	"database/sql"
	"fmt"
)

// OrgSettingsStore implements store.OrgSettingsStore across SQL backends.
type OrgSettingsStore struct {
	q Queryable
	d Dialect
}

// NewOrgSettingsStore returns an OrgSettingsStore bound to q with the given
// dialect.
func NewOrgSettingsStore(q Queryable, d Dialect) *OrgSettingsStore {
	return &OrgSettingsStore{q: q, d: d}
}

func (s *OrgSettingsStore) Get(ctx context.Context, orgID, key string) (string, error) {
	var value string
	err := s.q.QueryRowContext(ctx,
		`SELECT value FROM org_settings WHERE org_id = ? AND "key" = ?`,
		orgID, key).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("org setting not found: %s/%s", orgID, key)
		}
		return "", err
	}
	return value, nil
}

func (s *OrgSettingsStore) Set(ctx context.Context, orgID, key, value string) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO org_settings (org_id, "key", value) VALUES (?, ?, ?)
		 ON CONFLICT (org_id, "key") DO UPDATE SET value = excluded.value`,
		orgID, key, value)
	return err
}

func (s *OrgSettingsStore) Delete(ctx context.Context, orgID, key string) error {
	res, err := s.q.ExecContext(ctx,
		`DELETE FROM org_settings WHERE org_id = ? AND "key" = ?`,
		orgID, key)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

func (s *OrgSettingsStore) GetAll(ctx context.Context, orgID string) (map[string]string, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT "key", value FROM org_settings WHERE org_id = ?`, orgID)
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

// ListOrgIDsWithKey returns the IDs of orgs that have a non-empty value for
// key, for example every org with OTEL export configured.
func (s *OrgSettingsStore) ListOrgIDsWithKey(ctx context.Context, key string) ([]string, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT org_id FROM org_settings WHERE "key" = ? AND value <> '' ORDER BY org_id`, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
