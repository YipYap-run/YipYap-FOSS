package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

type oidcSigningKeyStore struct{ q queryable }

func (s *oidcSigningKeyStore) Create(ctx context.Context, k *store.OIDCSigningKey) error {
	_, err := s.q.ExecContext(ctx, `
        INSERT INTO oidc_signing_keys
            (kid, algorithm, encrypted_private, public_jwk, status, created_at, activated_at, retired_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)
    `,
		k.Kid,
		k.Algorithm,
		k.EncryptedPrivate,
		k.PublicJWK,
		k.Status,
		k.CreatedAt.UTC().Format(timeFormat),
		formatOptionalTime(k.ActivatedAt),
		formatOptionalTime(k.RetiredAt),
	)
	if err != nil {
		return fmt.Errorf("oidc_signing_keys insert: %w", err)
	}
	return nil
}

func (s *oidcSigningKeyStore) GetByKID(ctx context.Context, kid string) (*store.OIDCSigningKey, error) {
	row := s.q.QueryRowContext(ctx, `
        SELECT kid, algorithm, encrypted_private, public_jwk, status, created_at, activated_at, retired_at
        FROM oidc_signing_keys
        WHERE kid = ?
    `, kid)
	return scanOIDCSigningKey(row)
}

func (s *oidcSigningKeyStore) ListByStatus(ctx context.Context, statuses ...string) ([]*store.OIDCSigningKey, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if len(statuses) == 0 {
		rows, err = s.q.QueryContext(ctx, `
            SELECT kid, algorithm, encrypted_private, public_jwk, status, created_at, activated_at, retired_at
            FROM oidc_signing_keys
            ORDER BY created_at ASC
        `)
	} else {
		placeholders := strings.Repeat("?,", len(statuses))
		placeholders = placeholders[:len(placeholders)-1]
		args := make([]any, len(statuses))
		for i, v := range statuses {
			args[i] = v
		}
		rows, err = s.q.QueryContext(ctx, `
            SELECT kid, algorithm, encrypted_private, public_jwk, status, created_at, activated_at, retired_at
            FROM oidc_signing_keys
            WHERE status IN (`+placeholders+`)
            ORDER BY created_at ASC
        `, args...)
	}
	if err != nil {
		return nil, fmt.Errorf("oidc_signing_keys list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*store.OIDCSigningKey
	for rows.Next() {
		k, err := scanOIDCSigningKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *oidcSigningKeyStore) UpdateStatus(ctx context.Context, kid, status string, retiredAt *time.Time) error {
	res, err := s.q.ExecContext(ctx, `
        UPDATE oidc_signing_keys
        SET status = ?, retired_at = ?
        WHERE kid = ?
    `, status, formatOptionalTime(retiredAt), kid)
	if err != nil {
		return fmt.Errorf("oidc_signing_keys update status: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("oidc_signing_keys: kid %q not found", kid)
	}
	return nil
}

func (s *oidcSigningKeyStore) DeleteExpiredBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	cutoffStr := cutoff.UTC().Format(timeFormat)
	res, err := s.q.ExecContext(ctx, `
        DELETE FROM oidc_signing_keys
        WHERE status = 'expired' AND retired_at IS NOT NULL AND retired_at < ?
    `, cutoffStr)
	if err != nil {
		return 0, fmt.Errorf("oidc_signing_keys delete expired: %w", err)
	}
	return res.RowsAffected()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanOIDCSigningKey(r rowScanner) (*store.OIDCSigningKey, error) {
	var (
		k            store.OIDCSigningKey
		createdAt    string
		activatedAt  sql.NullString
		retiredAt    sql.NullString
	)
	err := r.Scan(
		&k.Kid,
		&k.Algorithm,
		&k.EncryptedPrivate,
		&k.PublicJWK,
		&k.Status,
		&createdAt,
		&activatedAt,
		&retiredAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	k.CreatedAt = mustParseTime(createdAt)
	if activatedAt.Valid {
		k.ActivatedAt = ptrTime(mustParseTime(activatedAt.String))
	}
	if retiredAt.Valid {
		k.RetiredAt = ptrTime(mustParseTime(retiredAt.String))
	}
	return &k, nil
}

func ptrTime(t time.Time) *time.Time { return &t }
