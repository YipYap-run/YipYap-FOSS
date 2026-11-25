package sqlx

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// SessionStore implements store.SessionStore across SQL backends.
type SessionStore struct {
	q Queryable
	d Dialect
}

// NewSessionStore returns a SessionStore bound to q with the given dialect.
func NewSessionStore(q Queryable, d Dialect) *SessionStore {
	return &SessionStore{q: q, d: d}
}

const sessionCols = `id, user_id, org_id, auth_method, created_at, last_seen_at, expires_at, revoked_at, user_agent, ip`

func scanSession(r Scanner) (*domain.Session, error) {
	var s domain.Session
	var createdAt, lastSeenAt, expiresAt, revokedAt Time
	var ua, ip sql.NullString
	if err := r.Scan(&s.ID, &s.UserID, &s.OrgID, &s.AuthMethod, &createdAt, &lastSeenAt, &expiresAt, &revokedAt, &ua, &ip); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrSessionNotFound
		}
		return nil, err
	}
	s.CreatedAt = createdAt.T
	s.LastSeenAt = lastSeenAt.T
	s.ExpiresAt = expiresAt.T
	s.RevokedAt = revokedAt.TimePtr()
	s.UserAgent = ua.String
	s.IP = ip.String
	return &s, nil
}

func (s *SessionStore) Create(ctx context.Context, sess *domain.Session) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO user_sessions (`+sessionCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.UserID, sess.OrgID, sess.AuthMethod,
		s.d.TimeArg(sess.CreatedAt), s.d.TimeArg(sess.LastSeenAt), s.d.TimeArg(sess.ExpiresAt),
		s.d.OptionalTimeArg(sess.RevokedAt), NullString(sess.UserAgent), NullString(sess.IP))
	return err
}

func (s *SessionStore) Get(ctx context.Context, id string) (*domain.Session, error) {
	return scanSession(s.q.QueryRowContext(ctx,
		`SELECT `+sessionCols+` FROM user_sessions WHERE id = ?`, id))
}

func (s *SessionStore) ListActiveByUser(ctx context.Context, userID string, now time.Time) ([]*domain.Session, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+sessionCols+` FROM user_sessions
		 WHERE user_id = ? AND revoked_at IS NULL AND expires_at > ?
		 ORDER BY last_seen_at DESC, created_at DESC`,
		userID, s.d.TimeArg(now))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*domain.Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

func (s *SessionStore) Touch(ctx context.Context, id string, at, notAfter time.Time) error {
	_, err := s.q.ExecContext(ctx,
		`UPDATE user_sessions SET last_seen_at = ? WHERE id = ? AND last_seen_at < ? AND revoked_at IS NULL`,
		s.d.TimeArg(at), id, s.d.TimeArg(notAfter))
	return err
}

func (s *SessionStore) Extend(ctx context.Context, id string, expiresAt time.Time) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE user_sessions SET expires_at = ? WHERE id = ? AND revoked_at IS NULL`,
		s.d.TimeArg(expiresAt), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrSessionNotFound
	}
	return nil
}

func (s *SessionStore) Revoke(ctx context.Context, userID, id string, at time.Time) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE user_sessions SET revoked_at = ? WHERE id = ? AND user_id = ? AND revoked_at IS NULL`,
		s.d.TimeArg(at), id, userID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrSessionNotFound
	}
	return nil
}

func (s *SessionStore) RevokeAllForUser(ctx context.Context, userID, exceptID string, at time.Time) ([]string, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT id FROM user_sessions WHERE user_id = ? AND revoked_at IS NULL AND id <> ?`,
		userID, exceptID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	// One UPDATE covers rows created between the SELECT and here as well,
	// so a racing login cannot keep a session the caller meant to revoke.
	_, err = s.q.ExecContext(ctx,
		`UPDATE user_sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL AND id <> ?`,
		s.d.TimeArg(at), userID, exceptID)
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *SessionStore) RevokeOrgSessions(ctx context.Context, orgID string, f store.OrgSessionFilter, at time.Time) ([]string, error) {
	where := `org_id = ? AND revoked_at IS NULL AND user_id IN (SELECT id FROM users WHERE org_id = ?`
	args := []any{orgID, orgID}
	if len(f.KeepRoles) > 0 {
		where += ` AND role NOT IN (` + placeholders(len(f.KeepRoles)) + `)`
		for _, r := range f.KeepRoles {
			args = append(args, r)
		}
	}
	where += `)`
	if len(f.KeepMethods) > 0 {
		where += ` AND auth_method NOT IN (` + placeholders(len(f.KeepMethods)) + `)`
		for _, m := range f.KeepMethods {
			args = append(args, m)
		}
	}

	rows, err := s.q.QueryContext(ctx, `SELECT id FROM user_sessions WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	// As in RevokeAllForUser, the UPDATE re-applies the filter so a session
	// created after the SELECT is revoked too.
	_, err = s.q.ExecContext(ctx,
		`UPDATE user_sessions SET revoked_at = ? WHERE `+where,
		append([]any{s.d.TimeArg(at)}, args...)...)
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?, ", n-1) + "?"
}

func (s *SessionStore) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.q.ExecContext(ctx,
		`DELETE FROM user_sessions WHERE expires_at < ?`, s.d.TimeArg(before))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
