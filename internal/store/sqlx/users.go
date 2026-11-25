package sqlx

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// UserStore implements store.UserStore across SQL backends.
type UserStore struct {
	q Queryable
	d Dialect
}

// NewUserStore returns a UserStore bound to q.
func NewUserStore(q Queryable, d Dialect) *UserStore {
	return &UserStore{q: q, d: d}
}

const userSelectCols = `id, org_id, email, name, password_hash, role, phone,
       force_password_change, mfa_app_enabled, mfa_enforced_at,
       email_verified_at, email_verification_sent_at,
       email_verification_resend_count, email_verification_resend_window_started,
       created_at, updated_at, disabled_at,
       pending_email, pending_email_nonce, pending_email_requested_at,
       deleted_at`

func (s *UserStore) Create(ctx context.Context, u *domain.User) error {
	u.Email = domain.NormalizeEmail(u.Email)
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO users (id, org_id, email, name, password_hash, role, phone, force_password_change, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.OrgID, u.Email, u.Name, u.PasswordHash, string(u.Role), u.Phone, s.d.BoolArg(u.ForcePasswordChange),
		s.d.TimeArg(u.CreatedAt),
		s.d.TimeArg(u.UpdatedAt),
	)
	return err
}

func (s *UserStore) GetByID(ctx context.Context, id string) (*domain.User, error) {
	return scanUser(s.q.QueryRowContext(ctx,
		`SELECT `+userSelectCols+` FROM users WHERE id = ?`, id))
}

// GetByEmail matches without regard to case or surrounding space. The
// lower(email) expression matches idx_users_email_lower.
func (s *UserStore) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	return scanUser(s.q.QueryRowContext(ctx,
		`SELECT `+userSelectCols+` FROM users WHERE lower(email) = ?`, domain.NormalizeEmail(email)))
}

func (s *UserStore) ListByOrg(ctx context.Context, orgID string, params store.ListParams) ([]*domain.User, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+userSelectCols+`
		 FROM users WHERE org_id = ? AND disabled_at IS NULL ORDER BY email LIMIT ? OFFSET ?`,
		orgID, limit, params.Offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var users []*domain.User
	for rows.Next() {
		u, err := scanUserFromRows(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// escapeLike escapes the LIKE wildcards in a user-supplied search term so
// they match literally. Queries pair it with ESCAPE '!', which reads the same
// on SQLite and Postgres (a backslash escape depends on string settings).
func escapeLike(q string) string {
	return strings.NewReplacer(`!`, `!!`, `%`, `!%`, `_`, `!_`).Replace(q)
}

func (s *UserStore) SearchByOrg(ctx context.Context, orgID string, f store.UserFilter) ([]*domain.User, int, error) {
	where := []string{"org_id = ?", "disabled_at IS NULL"}
	args := []any{orgID}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + strings.ToLower(escapeLike(q)) + "%"
		where = append(where, `(LOWER(email) LIKE ? ESCAPE '!' OR LOWER(COALESCE(name, '')) LIKE ? ESCAPE '!')`)
		args = append(args, like, like)
	}
	if f.Role != "" {
		where = append(where, "role = ?")
		args = append(args, f.Role)
	}
	if f.IDs != nil {
		if len(f.IDs) == 0 {
			return []*domain.User{}, 0, nil
		}
		where = append(where, "id IN (?"+strings.Repeat(", ?", len(f.IDs)-1)+")")
		for _, id := range f.IDs {
			args = append(args, id)
		}
	}
	clause := " WHERE " + strings.Join(where, " AND ")

	var total int
	if err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+userSelectCols+` FROM users`+clause+` ORDER BY email, id LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	users := []*domain.User{}
	for rows.Next() {
		u, err := scanUserFromRows(rows)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}
	return users, total, rows.Err()
}

func (s *UserStore) CountByOrg(ctx context.Context, orgID string) (int, error) {
	var n int
	err := s.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE org_id = ? AND disabled_at IS NULL`, orgID).Scan(&n)
	return n, err
}

func (s *UserStore) Update(ctx context.Context, u *domain.User) error {
	u.Email = domain.NormalizeEmail(u.Email)
	res, err := s.q.ExecContext(ctx,
		`UPDATE users SET email = ?, name = ?, password_hash = ?, role = ?, phone = ?, force_password_change = ?, updated_at = ?
		 WHERE id = ?`,
		u.Email, u.Name, u.PasswordHash, string(u.Role), u.Phone, s.d.BoolArg(u.ForcePasswordChange),
		s.d.TimeArg(u.UpdatedAt), u.ID,
	)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

// SetMFAEnforcedAt stores the grace deadline only if none is set yet.
func (s *UserStore) SetMFAEnforcedAt(ctx context.Context, id string, deadline time.Time) error {
	_, err := s.q.ExecContext(ctx,
		`UPDATE users SET mfa_enforced_at = ? WHERE id = ? AND mfa_enforced_at IS NULL`,
		s.d.TimeArg(deadline.UTC()), id,
	)
	return err
}

// ExpireMFAGrace overwrites the grace deadline with at.
func (s *UserStore) ExpireMFAGrace(ctx context.Context, id string, at time.Time) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE users SET mfa_enforced_at = ? WHERE id = ?`,
		s.d.TimeArg(at.UTC()), id,
	)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

// ClearMFAEnforcedAtByOrg removes all stored grace deadlines in an org.
func (s *UserStore) ClearMFAEnforcedAtByOrg(ctx context.Context, orgID string) error {
	_, err := s.q.ExecContext(ctx,
		`UPDATE users SET mfa_enforced_at = NULL WHERE org_id = ? AND mfa_enforced_at IS NOT NULL`,
		orgID,
	)
	return err
}

// SetPendingEmail records a requested email change, replacing any earlier
// request and so voiding its links.
func (s *UserStore) SetPendingEmail(ctx context.Context, id, email, nonce string, at time.Time) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE users SET pending_email = ?, pending_email_nonce = ?, pending_email_requested_at = ? WHERE id = ?`,
		domain.NormalizeEmail(email), nonce, s.d.TimeArg(at.UTC()), id,
	)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

// ClearPendingEmail drops a pending change. With a non-empty nonce it only
// clears the request that nonce belongs to.
func (s *UserStore) ClearPendingEmail(ctx context.Context, id, nonce string) error {
	query := `UPDATE users SET pending_email = NULL, pending_email_nonce = NULL, pending_email_requested_at = NULL
		 WHERE id = ? AND pending_email IS NOT NULL`
	args := []any{id}
	if nonce != "" {
		query += ` AND pending_email_nonce = ?`
		args = append(args, nonce)
	}
	res, err := s.q.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

// ConfirmPendingEmail swaps in the pending address if it still matches the
// confirmed request, and marks it verified. The swap is one conditional
// UPDATE so a replayed or superseded link changes nothing.
func (s *UserStore) ConfirmPendingEmail(ctx context.Context, id, email, nonce string, at time.Time) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE users SET email = pending_email, email_verified_at = ?, updated_at = ?,
		        pending_email = NULL, pending_email_nonce = NULL, pending_email_requested_at = NULL
		  WHERE id = ? AND pending_email = ? AND pending_email_nonce = ?`,
		s.d.TimeArg(at.UTC()), s.d.TimeArg(at.UTC()), id, domain.NormalizeEmail(email), nonce,
	)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

func (s *UserStore) Delete(ctx context.Context, id string) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

// Anonymize turns a user row into a deleted-user tombstone and removes what
// only that user owned. The row is kept so support tickets, incidents,
// incident updates and past schedule overrides keep a valid author. Run it
// inside Store.Tx so a failure leaves the account untouched. Sessions are
// revoked separately through the session package.
func (s *UserStore) Anonymize(ctx context.Context, id string, at time.Time) error {
	return s.anonymize(ctx, id, at, false)
}

func (s *UserStore) AnonymizeKeepAPIKeys(ctx context.Context, id string, at time.Time) error {
	return s.anonymize(ctx, id, at, true)
}

func (s *UserStore) anonymize(ctx context.Context, id string, at time.Time, keepAPIKeys bool) error {
	at = at.UTC()
	// Clear every personal or credential column. disabled_at stays set (or
	// is set now) so every sign-in path keeps refusing the row.
	res, err := s.q.ExecContext(ctx,
		`UPDATE users SET email = ?, name = ?, password_hash = '', phone = '', role = ?,
		        force_password_change = ?,
		        mfa_enforced_at = NULL,
		        email_verified_at = NULL, email_verification_sent_at = NULL,
		        email_verification_resend_count = 0, email_verification_resend_window_started = NULL,
		        pending_email = NULL, pending_email_nonce = NULL, pending_email_requested_at = NULL,
		        disabled_at = COALESCE(disabled_at, ?), deleted_at = ?, updated_at = ?
		  WHERE id = ? AND deleted_at IS NULL`,
		domain.DeletedUserEmail(id), domain.DeletedUserName, string(domain.RoleViewer),
		s.d.BoolArg(false),
		s.d.TimeArg(at), s.d.TimeArg(at), s.d.TimeArg(at), id,
	)
	if err != nil {
		return fmt.Errorf("anonymize user row: %w", err)
	}
	if err := ExpectOneRow(res); err != nil {
		return err
	}
	if err := s.clearCredentialRows(ctx, id); err != nil {
		return err
	}

	steps := []struct {
		name  string
		query string
		args  []any
	}{
		{"oidc links", `DELETE FROM user_oidc_links WHERE user_id = ?`, []any{id}},
		// Team membership drives on-call rotations; a deleted user must not
		// be scheduled.
		{"team memberships", `DELETE FROM team_members WHERE user_id = ?`, []any{id}},
		// Overrides that have not ended would route pages to an account
		// nobody can sign in to. Past overrides stay as history.
		{"future schedule overrides", `DELETE FROM schedule_overrides WHERE user_id = ? AND end_at > ?`, []any{id, s.d.TimeArg(at)}},
		// API keys authenticate as their creator. Revoke them rather than
		// leave working credentials owned by a deleted account.
		{"api keys", `DELETE FROM api_keys WHERE created_by = ?`, []any{id}},
		// Support messages copy the sender's address. Point it at the
		// placeholder so the message reads as from a deleted user.
		{"support message sender", `UPDATE support_messages SET sender_email = ? WHERE sender_id = ? AND sender_type = ?`, []any{domain.DeletedUserEmail(id), id, domain.SupportSenderUser}},
	}
	if keepAPIKeys {
		kept := steps[:0]
		for _, st := range steps {
			if st.name == "api keys" {
				continue
			}
			kept = append(kept, st)
		}
		steps = kept
	}
	for _, st := range steps {
		if _, err := s.q.ExecContext(ctx, st.query, st.args...); err != nil {
			return fmt.Errorf("anonymize %s: %w", st.name, err)
		}
	}
	return nil
}

func (s *UserStore) Disable(ctx context.Context, id string, at time.Time) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE users SET disabled_at = ?, updated_at = ? WHERE id = ? AND disabled_at IS NULL`,
		s.d.TimeArg(at), s.d.TimeArg(at), id,
	)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

func (s *UserStore) Enable(ctx context.Context, id string) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE users SET disabled_at = NULL, updated_at = ? WHERE id = ? AND disabled_at IS NOT NULL AND deleted_at IS NULL`,
		s.d.TimeArg(time.Now()), id,
	)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

func (s *UserStore) ListDisabledBefore(ctx context.Context, before time.Time) ([]*domain.User, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+userSelectCols+`
		 FROM users WHERE disabled_at IS NOT NULL AND disabled_at < ? AND deleted_at IS NULL`,
		s.d.TimeArg(before))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var users []*domain.User
	for rows.Next() {
		u, err := scanUserFromRows(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *UserStore) MarkEmailVerified(ctx context.Context, id string, at time.Time) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE users SET email_verified_at = ?, updated_at = ? WHERE id = ?`,
		s.d.TimeArg(at), s.d.TimeArg(at), id,
	)
	if err != nil {
		return err
	}
	if err := ExpectOneRow(res); err != nil {
		return err
	}
	// A verified account email becomes the user's first contact method so
	// they can be paged before they open their notification settings.
	if err := NewContactMethodStore(s.q, s.d).SeedDefaultEmail(ctx, id, at); err != nil {
		slog.Warn("users: seed default email contact method", "user_id", id, "error", err)
	}
	return nil
}

func (s *UserStore) ClearEmailVerified(ctx context.Context, id string, at time.Time) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE users
		    SET email_verified_at = NULL,
		        email_verification_sent_at = NULL,
		        email_verification_resend_count = 0,
		        email_verification_resend_window_started = NULL,
		        updated_at = ?
		  WHERE id = ?`,
		s.d.TimeArg(at), id,
	)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

func (s *UserStore) RecordVerificationSend(ctx context.Context, id string, at time.Time) error {
	now := at.UTC()
	windowCutoff := now.Add(-time.Hour)
	_, err := s.q.ExecContext(ctx,
		`UPDATE users
		    SET email_verification_sent_at = ?,
		        email_verification_resend_count = CASE
		          WHEN email_verification_resend_window_started IS NULL
		            OR email_verification_resend_window_started < ?
		          THEN 1
		          ELSE email_verification_resend_count + 1
		        END,
		        email_verification_resend_window_started = CASE
		          WHEN email_verification_resend_window_started IS NULL
		            OR email_verification_resend_window_started < ?
		          THEN ?
		          ELSE email_verification_resend_window_started
		        END
		  WHERE id = ?`,
		s.d.TimeArg(now), s.d.TimeArg(windowCutoff), s.d.TimeArg(windowCutoff), s.d.TimeArg(now), id,
	)
	return err
}

func (s *UserStore) ListUnverifiedUnsent(ctx context.Context) ([]*domain.User, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+userSelectCols+`
		   FROM users
		  WHERE email_verified_at IS NULL
		    AND email_verification_sent_at IS NULL
		    AND disabled_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var users []*domain.User
	for rows.Next() {
		u, err := scanUserFromRows(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func scanUser(r Scanner) (*domain.User, error) {
	u, err := scanUserInto(r)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user not found")
		}
		return nil, err
	}
	return u, nil
}

func scanUserFromRows(r Scanner) (*domain.User, error) {
	return scanUserInto(r)
}

func scanUserInto(r Scanner) (*domain.User, error) {
	var (
		u               domain.User
		role            string
		forcePwChange   Bool
		mfaAppEnabled   Bool
		mfaEnforcedAt   Time
		emailVerifiedAt Time
		verSentAt       Time
		verWindow       Time
		createdAt       Time
		updatedAt       Time
		disabledAt      Time
		pendingEmail    sql.NullString
		pendingNonce    sql.NullString
		pendingAt       Time
		deletedAt       Time
	)
	if err := r.Scan(
		&u.ID, &u.OrgID, &u.Email, &u.Name, &u.PasswordHash, &role, &u.Phone,
		&forcePwChange, &mfaAppEnabled, &mfaEnforcedAt,
		&emailVerifiedAt, &verSentAt, &u.EmailVerificationResendCount, &verWindow,
		&createdAt, &updatedAt, &disabledAt,
		&pendingEmail, &pendingNonce, &pendingAt,
		&deletedAt,
	); err != nil {
		return nil, err
	}
	u.Role = domain.UserRole(role)
	u.ForcePasswordChange = bool(forcePwChange)
	u.MFAAppEnabled = bool(mfaAppEnabled)
	if mfaEnforcedAt.Valid {
		u.MFAEnforcedAt = mfaEnforcedAt.T.UTC().Format(time.RFC3339)
	}
	u.EmailVerifiedAt = emailVerifiedAt.TimePtr()
	u.EmailVerificationSentAt = verSentAt.TimePtr()
	u.EmailVerificationResendWindowStarted = verWindow.TimePtr()
	u.CreatedAt = createdAt.T
	u.UpdatedAt = updatedAt.T
	u.DisabledAt = disabledAt.TimePtr()
	u.PendingEmail = pendingEmail.String
	u.PendingEmailNonce = pendingNonce.String
	u.PendingEmailRequestedAt = pendingAt.TimePtr()
	u.DeletedAt = deletedAt.TimePtr()
	return &u, nil
}
