package sqlx

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// APIKeyStore implements store.APIKeyStore across SQL backends.
type APIKeyStore struct {
	q Queryable
	d Dialect
}

// NewAPIKeyStore returns an APIKeyStore bound to q with the given dialect.
func NewAPIKeyStore(q Queryable, d Dialect) *APIKeyStore {
	return &APIKeyStore{q: q, d: d}
}

func (s *APIKeyStore) Create(ctx context.Context, k *domain.APIKey) error {
	scopes, _ := json.Marshal(k.Scopes)
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO api_keys (id, org_id, name, key_hash, prefix, scopes, created_by, expires_at, created_at, last_used_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		k.ID, k.OrgID, k.Name, k.KeyHash, k.Prefix, string(scopes),
		k.CreatedBy, s.d.OptionalTimeArg(k.ExpiresAt),
		s.d.TimeArg(k.CreatedAt),
		s.d.OptionalTimeArg(k.LastUsedAt))
	return err
}

func (s *APIKeyStore) GetByID(ctx context.Context, id string) (*domain.APIKey, error) {
	return s.scanKey(s.q.QueryRowContext(ctx,
		`SELECT id, org_id, name, key_hash, prefix, scopes, created_by, expires_at, created_at, last_used_at
		 FROM api_keys WHERE id = ?`, id))
}

func (s *APIKeyStore) GetByHash(ctx context.Context, hash string) (*domain.APIKey, error) {
	return s.scanKey(s.q.QueryRowContext(ctx,
		`SELECT id, org_id, name, key_hash, prefix, scopes, created_by, expires_at, created_at, last_used_at
		 FROM api_keys WHERE key_hash = ?`, hash))
}

func (s *APIKeyStore) ListByOrg(ctx context.Context, orgID string, params store.ListParams) ([]*domain.APIKey, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	return s.list(ctx,
		`SELECT id, org_id, name, key_hash, prefix, scopes, created_by, expires_at, created_at, last_used_at
		 FROM api_keys WHERE org_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		orgID, limit, params.Offset)
}

func (s *APIKeyStore) ListByCreator(ctx context.Context, orgID, userID string) ([]*domain.APIKey, error) {
	return s.list(ctx,
		`SELECT id, org_id, name, key_hash, prefix, scopes, created_by, expires_at, created_at, last_used_at
		 FROM api_keys WHERE org_id = ? AND created_by = ? ORDER BY created_at DESC`,
		orgID, userID)
}

func (s *APIKeyStore) DeleteByCreator(ctx context.Context, orgID, userID string) (int64, error) {
	res, err := s.q.ExecContext(ctx,
		`DELETE FROM api_keys WHERE org_id = ? AND created_by = ?`, orgID, userID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *APIKeyStore) list(ctx context.Context, query string, args ...any) ([]*domain.APIKey, error) {
	rows, err := s.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.APIKey
	for rows.Next() {
		var k domain.APIKey
		var scopesStr string
		var expiresAt, createdAt, lastUsedAt Time
		if err := rows.Scan(&k.ID, &k.OrgID, &k.Name, &k.KeyHash, &k.Prefix, &scopesStr, &k.CreatedBy, &expiresAt, &createdAt, &lastUsedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(scopesStr), &k.Scopes)
		k.CreatedAt = createdAt.T
		k.ExpiresAt = expiresAt.TimePtr()
		k.LastUsedAt = lastUsedAt.TimePtr()
		out = append(out, &k)
	}
	return out, rows.Err()
}

func (s *APIKeyStore) Delete(ctx context.Context, id string) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM api_keys WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

func (s *APIKeyStore) UpdateLastUsed(ctx context.Context, id string, at time.Time) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE api_keys SET last_used_at = ? WHERE id = ?`,
		s.d.TimeArg(at), id)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

func (s *APIKeyStore) scanKey(row *sql.Row) (*domain.APIKey, error) {
	var k domain.APIKey
	var scopesStr string
	var expiresAt, createdAt, lastUsedAt Time
	if err := row.Scan(&k.ID, &k.OrgID, &k.Name, &k.KeyHash, &k.Prefix, &scopesStr, &k.CreatedBy, &expiresAt, &createdAt, &lastUsedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("api key not found")
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(scopesStr), &k.Scopes)
	k.CreatedAt = createdAt.T
	k.ExpiresAt = expiresAt.TimePtr()
	k.LastUsedAt = lastUsedAt.TimePtr()
	return &k, nil
}
