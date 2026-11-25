package sqlx

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// InviteStore implements store.InviteStore across SQL backends.
type InviteStore struct {
	q Queryable
	d Dialect
}

// NewInviteStore returns an InviteStore bound to q with the given dialect.
func NewInviteStore(q Queryable, d Dialect) *InviteStore {
	return &InviteStore{q: q, d: d}
}

const inviteCols = `id, org_id, email, role, token_hash, invited_by, expires_at, accepted_at, revoked_at, created_at`

// invitePending is the SQL predicate for a pending invite.
const invitePending = `accepted_at IS NULL AND revoked_at IS NULL`

func (s *InviteStore) Create(ctx context.Context, inv *domain.OrgInvite) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO org_invites (`+inviteCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		inv.ID, inv.OrgID, inv.Email, string(inv.Role), inv.TokenHash, inv.InvitedBy,
		s.d.TimeArg(inv.ExpiresAt), s.d.OptionalTimeArg(inv.AcceptedAt),
		s.d.OptionalTimeArg(inv.RevokedAt), s.d.TimeArg(inv.CreatedAt))
	return err
}

func (s *InviteStore) GetByID(ctx context.Context, id string) (*domain.OrgInvite, error) {
	return s.scan(s.q.QueryRowContext(ctx,
		`SELECT `+inviteCols+` FROM org_invites WHERE id = ?`, id))
}

func (s *InviteStore) GetPendingByEmail(ctx context.Context, orgID, email string) (*domain.OrgInvite, error) {
	return s.scan(s.q.QueryRowContext(ctx,
		`SELECT `+inviteCols+` FROM org_invites
		 WHERE org_id = ? AND lower(email) = lower(?) AND `+invitePending, orgID, email))
}

func (s *InviteStore) ListPending(ctx context.Context, orgID string) ([]*domain.OrgInvite, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+inviteCols+` FROM org_invites
		 WHERE org_id = ? AND `+invitePending+` ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*domain.OrgInvite
	for rows.Next() {
		inv, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (s *InviteStore) CountActive(ctx context.Context, orgID string, now time.Time) (int, error) {
	var n int
	err := s.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM org_invites
		 WHERE org_id = ? AND `+invitePending+` AND expires_at > ?`,
		orgID, s.d.TimeArg(now)).Scan(&n)
	return n, err
}

func (s *InviteStore) Rotate(ctx context.Context, id, tokenHash string, expiresAt time.Time) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE org_invites SET token_hash = ?, expires_at = ? WHERE id = ? AND `+invitePending,
		tokenHash, s.d.TimeArg(expiresAt), id)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

func (s *InviteStore) Revoke(ctx context.Context, id string, at time.Time) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE org_invites SET revoked_at = ? WHERE id = ? AND `+invitePending,
		s.d.TimeArg(at), id)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

func (s *InviteStore) MarkAccepted(ctx context.Context, id, tokenHash string, at time.Time) (bool, error) {
	res, err := s.q.ExecContext(ctx,
		`UPDATE org_invites SET accepted_at = ?
		 WHERE id = ? AND token_hash = ? AND expires_at > ? AND `+invitePending,
		s.d.TimeArg(at), id, tokenHash, s.d.TimeArg(at))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func (s *InviteStore) scan(row Scanner) (*domain.OrgInvite, error) {
	var inv domain.OrgInvite
	var role string
	var expiresAt, acceptedAt, revokedAt, createdAt Time
	if err := row.Scan(&inv.ID, &inv.OrgID, &inv.Email, &role, &inv.TokenHash, &inv.InvitedBy,
		&expiresAt, &acceptedAt, &revokedAt, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("invite not found")
		}
		return nil, err
	}
	inv.Role = domain.UserRole(role)
	inv.ExpiresAt = expiresAt.T
	inv.AcceptedAt = acceptedAt.TimePtr()
	inv.RevokedAt = revokedAt.TimePtr()
	inv.CreatedAt = createdAt.T
	return &inv, nil
}
