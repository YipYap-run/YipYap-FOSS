package sqlx

import (
	"context"
	"database/sql"
	"fmt"
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
       created_at, updated_at, disabled_at`

func (s *UserStore) Create(ctx context.Context, u *domain.User) error {
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

func (s *UserStore) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	return scanUser(s.q.QueryRowContext(ctx,
		`SELECT `+userSelectCols+` FROM users WHERE email = ?`, email))
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

func (s *UserStore) Update(ctx context.Context, u *domain.User) error {
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

func (s *UserStore) Delete(ctx context.Context, id string) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
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
		`UPDATE users SET disabled_at = NULL, updated_at = ? WHERE id = ? AND disabled_at IS NOT NULL`,
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
		 FROM users WHERE disabled_at IS NOT NULL AND disabled_at < ?`,
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
	)
	if err := r.Scan(
		&u.ID, &u.OrgID, &u.Email, &u.Name, &u.PasswordHash, &role, &u.Phone,
		&forcePwChange, &mfaAppEnabled, &mfaEnforcedAt,
		&emailVerifiedAt, &verSentAt, &u.EmailVerificationResendCount, &verWindow,
		&createdAt, &updatedAt, &disabledAt,
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
	return &u, nil
}
