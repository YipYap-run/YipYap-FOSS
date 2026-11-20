package sqlx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// OIDCSigningKeyStore implements store.OIDCSigningKeyStore across SQL backends.
type OIDCSigningKeyStore struct {
	q Queryable
	d Dialect
}

// NewOIDCSigningKeyStore returns an OIDCSigningKeyStore bound to q.
func NewOIDCSigningKeyStore(q Queryable, d Dialect) *OIDCSigningKeyStore {
	return &OIDCSigningKeyStore{q: q, d: d}
}

func (s *OIDCSigningKeyStore) Create(ctx context.Context, k *store.OIDCSigningKey) error {
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
		s.d.TimeArg(k.CreatedAt),
		s.d.OptionalTimeArg(k.ActivatedAt),
		s.d.OptionalTimeArg(k.RetiredAt),
	)
	if err != nil {
		return fmt.Errorf("oidc_signing_keys insert: %w", err)
	}
	return nil
}

func (s *OIDCSigningKeyStore) GetByKID(ctx context.Context, kid string) (*store.OIDCSigningKey, error) {
	row := s.q.QueryRowContext(ctx, `
        SELECT kid, algorithm, encrypted_private, public_jwk, status, created_at, activated_at, retired_at
        FROM oidc_signing_keys
        WHERE kid = ?
    `, kid)
	return scanOIDCSigningKey(row)
}

func (s *OIDCSigningKeyStore) ListByStatus(ctx context.Context, statuses ...string) ([]*store.OIDCSigningKey, error) {
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

func (s *OIDCSigningKeyStore) UpdateStatus(ctx context.Context, kid, status string, retiredAt *time.Time) error {
	res, err := s.q.ExecContext(ctx, `
        UPDATE oidc_signing_keys
        SET status = ?, retired_at = ?
        WHERE kid = ?
    `, status, s.d.OptionalTimeArg(retiredAt), kid)
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

func (s *OIDCSigningKeyStore) DeleteExpiredBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.q.ExecContext(ctx, `
        DELETE FROM oidc_signing_keys
        WHERE status = 'expired' AND retired_at IS NOT NULL AND retired_at < ?
    `, s.d.TimeArg(cutoff))
	if err != nil {
		return 0, fmt.Errorf("oidc_signing_keys delete expired: %w", err)
	}
	return res.RowsAffected()
}

func scanOIDCSigningKey(r Scanner) (*store.OIDCSigningKey, error) {
	var (
		k           store.OIDCSigningKey
		createdAt   Time
		activatedAt Time
		retiredAt   Time
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
	k.CreatedAt = createdAt.T
	k.ActivatedAt = activatedAt.TimePtr()
	k.RetiredAt = retiredAt.TimePtr()
	return &k, nil
}
