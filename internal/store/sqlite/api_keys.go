package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

type apiKeyStore struct{ q queryable }

func (s *apiKeyStore) Create(ctx context.Context, k *domain.APIKey) error {
	scopes, _ := json.Marshal(k.Scopes)
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO api_keys (id, org_id, name, key_hash, prefix, scopes, created_by, expires_at, created_at, last_used_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		k.ID, k.OrgID, k.Name, k.KeyHash, k.Prefix, string(scopes),
		k.CreatedBy, formatOptionalTime(k.ExpiresAt),
		k.CreatedAt.UTC().Format(timeFormat),
		formatOptionalTime(k.LastUsedAt))
	return err
}

func (s *apiKeyStore) GetByID(ctx context.Context, id string) (*domain.APIKey, error) {
	return s.scanKey(s.q.QueryRowContext(ctx,
		`SELECT id, org_id, name, key_hash, prefix, scopes, created_by, expires_at, created_at, last_used_at
		 FROM api_keys WHERE id = ?`, id))
}

func (s *apiKeyStore) GetByHash(ctx context.Context, hash string) (*domain.APIKey, error) {
	return s.scanKey(s.q.QueryRowContext(ctx,
		`SELECT id, org_id, name, key_hash, prefix, scopes, created_by, expires_at, created_at, last_used_at
		 FROM api_keys WHERE key_hash = ?`, hash))
}

func (s *apiKeyStore) ListByOrg(ctx context.Context, orgID string, params store.ListParams) ([]*domain.APIKey, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.q.QueryContext(ctx,
		`SELECT id, org_id, name, key_hash, prefix, scopes, created_by, expires_at, created_at, last_used_at
		 FROM api_keys WHERE org_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		orgID, limit, params.Offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.APIKey
	for rows.Next() {
		var k domain.APIKey
		var scopesStr string
		var expiresAt, lastUsedAt *string
		var createdAt string
		if err := rows.Scan(&k.ID, &k.OrgID, &k.Name, &k.KeyHash, &k.Prefix, &scopesStr, &k.CreatedBy, &expiresAt, &createdAt, &lastUsedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(scopesStr), &k.Scopes)
		k.CreatedAt = mustParseTime(createdAt)
		k.ExpiresAt = parseOptionalTime(expiresAt)
		k.LastUsedAt = parseOptionalTime(lastUsedAt)
		out = append(out, &k)
	}
	return out, rows.Err()
}

func (s *apiKeyStore) Delete(ctx context.Context, id string) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM api_keys WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

func (s *apiKeyStore) UpdateLastUsed(ctx context.Context, id string, at time.Time) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE api_keys SET last_used_at = ? WHERE id = ?`,
		at.UTC().Format(timeFormat), id)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

func (s *apiKeyStore) scanKey(row *sql.Row) (*domain.APIKey, error) {
	var k domain.APIKey
	var scopesStr string
	var expiresAt, lastUsedAt *string
	var createdAt string
	if err := row.Scan(&k.ID, &k.OrgID, &k.Name, &k.KeyHash, &k.Prefix, &scopesStr, &k.CreatedBy, &expiresAt, &createdAt, &lastUsedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("api key not found")
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(scopesStr), &k.Scopes)
	k.CreatedAt = mustParseTime(createdAt)
	k.ExpiresAt = parseOptionalTime(expiresAt)
	k.LastUsedAt = parseOptionalTime(lastUsedAt)
	return &k, nil
}
