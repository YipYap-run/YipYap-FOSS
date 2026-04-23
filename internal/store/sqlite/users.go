package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

type userStore struct{ q queryable }

const userSelectCols = `id, org_id, email, name, password_hash, role, phone,
       force_password_change, mfa_app_enabled, mfa_enforced_at,
       email_verified_at, email_verification_sent_at,
       email_verification_resend_count, email_verification_resend_window_started,
       created_at, updated_at, disabled_at`

func (s *userStore) Create(ctx context.Context, u *domain.User) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO users (id, org_id, email, name, password_hash, role, phone, force_password_change, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.OrgID, u.Email, u.Name, u.PasswordHash, string(u.Role), u.Phone, u.ForcePasswordChange,
		u.CreatedAt.UTC().Format(timeFormat),
		u.UpdatedAt.UTC().Format(timeFormat),
	)
	return err
}

func (s *userStore) GetByID(ctx context.Context, id string) (*domain.User, error) {
	return s.scanUser(s.q.QueryRowContext(ctx,
		`SELECT `+userSelectCols+` FROM users WHERE id = ?`, id))
}

func (s *userStore) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	return s.scanUser(s.q.QueryRowContext(ctx,
		`SELECT `+userSelectCols+` FROM users WHERE email = ?`, email))
}

func (s *userStore) ListByOrg(ctx context.Context, orgID string, params store.ListParams) ([]*domain.User, error) {
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
		u, err := s.scanUserFromRows(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *userStore) Update(ctx context.Context, u *domain.User) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE users SET email = ?, name = ?, password_hash = ?, role = ?, phone = ?, force_password_change = ?, updated_at = ?
		 WHERE id = ?`,
		u.Email, u.Name, u.PasswordHash, string(u.Role), u.Phone, u.ForcePasswordChange,
		u.UpdatedAt.UTC().Format(timeFormat), u.ID,
	)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

func (s *userStore) Delete(ctx context.Context, id string) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

func (s *userStore) scanUser(row *sql.Row) (*domain.User, error) {
	var u domain.User
	var role, createdAt, updatedAt string
	var mfaEnforcedAt, disabledAt, emailVerifiedAt, verSentAt, verWindow sql.NullString
	if err := row.Scan(
		&u.ID, &u.OrgID, &u.Email, &u.Name, &u.PasswordHash, &role, &u.Phone,
		&u.ForcePasswordChange, &u.MFAAppEnabled, &mfaEnforcedAt,
		&emailVerifiedAt, &verSentAt, &u.EmailVerificationResendCount, &verWindow,
		&createdAt, &updatedAt, &disabledAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user not found")
		}
		return nil, err
	}
	hydrateUser(&u, role, createdAt, updatedAt, mfaEnforcedAt, disabledAt, emailVerifiedAt, verSentAt, verWindow)
	return &u, nil
}

func (s *userStore) scanUserFromRows(rows *sql.Rows) (*domain.User, error) {
	var u domain.User
	var role, createdAt, updatedAt string
	var mfaEnforcedAt, disabledAt, emailVerifiedAt, verSentAt, verWindow sql.NullString
	if err := rows.Scan(
		&u.ID, &u.OrgID, &u.Email, &u.Name, &u.PasswordHash, &role, &u.Phone,
		&u.ForcePasswordChange, &u.MFAAppEnabled, &mfaEnforcedAt,
		&emailVerifiedAt, &verSentAt, &u.EmailVerificationResendCount, &verWindow,
		&createdAt, &updatedAt, &disabledAt,
	); err != nil {
		return nil, err
	}
	hydrateUser(&u, role, createdAt, updatedAt, mfaEnforcedAt, disabledAt, emailVerifiedAt, verSentAt, verWindow)
	return &u, nil
}

func hydrateUser(u *domain.User, role, createdAt, updatedAt string, mfaEnforcedAt, disabledAt, emailVerifiedAt, verSentAt, verWindow sql.NullString) {
	u.Role = domain.UserRole(role)
	if mfaEnforcedAt.Valid {
		u.MFAEnforcedAt = mfaEnforcedAt.String
	}
	u.CreatedAt = mustParseTime(createdAt)
	u.UpdatedAt = mustParseTime(updatedAt)
	if disabledAt.Valid {
		t := mustParseTime(disabledAt.String)
		u.DisabledAt = &t
	}
	if emailVerifiedAt.Valid {
		t := mustParseTime(emailVerifiedAt.String)
		u.EmailVerifiedAt = &t
	}
	if verSentAt.Valid {
		t := mustParseTime(verSentAt.String)
		u.EmailVerificationSentAt = &t
	}
	if verWindow.Valid {
		t := mustParseTime(verWindow.String)
		u.EmailVerificationResendWindowStarted = &t
	}
}

func (s *userStore) Disable(ctx context.Context, id string, at time.Time) error {
	now := at.UTC().Format(timeFormat)
	res, err := s.q.ExecContext(ctx,
		`UPDATE users SET disabled_at = ?, updated_at = ? WHERE id = ? AND disabled_at IS NULL`,
		now, now, id,
	)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

func (s *userStore) Enable(ctx context.Context, id string) error {
	now := time.Now().UTC().Format(timeFormat)
	res, err := s.q.ExecContext(ctx,
		`UPDATE users SET disabled_at = NULL, updated_at = ? WHERE id = ? AND disabled_at IS NOT NULL`,
		now, id,
	)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

func (s *userStore) ListDisabledBefore(ctx context.Context, before time.Time) ([]*domain.User, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+userSelectCols+`
		 FROM users WHERE disabled_at IS NOT NULL AND disabled_at < ?`,
		before.UTC().Format(timeFormat))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var users []*domain.User
	for rows.Next() {
		u, err := s.scanUserFromRows(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *userStore) MarkEmailVerified(ctx context.Context, id string, at time.Time) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE users SET email_verified_at = ?, updated_at = ? WHERE id = ?`,
		at.UTC().Format(timeFormat), at.UTC().Format(timeFormat), id,
	)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

// RecordVerificationSend updates sent_at and increments the hourly counter
// (resetting the window if expired). The new window/count values are
// computed in a single statement so this is safe under concurrent resend
// presses from the same user.
func (s *userStore) RecordVerificationSend(ctx context.Context, id string, at time.Time) error {
	windowCutoff := at.UTC().Add(-1 * time.Hour).Format(timeFormat)
	nowStr := at.UTC().Format(timeFormat)
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
		nowStr, windowCutoff, windowCutoff, nowStr, id,
	)
	return err
}

func (s *userStore) ListUnverifiedUnsent(ctx context.Context) ([]*domain.User, error) {
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
		u, err := s.scanUserFromRows(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}
