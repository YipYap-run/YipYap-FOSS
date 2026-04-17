package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

type userStore struct{ q queryable }

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
		`SELECT id, org_id, email, name, password_hash, role, phone, force_password_change, mfa_app_enabled, mfa_enforced_at, created_at, updated_at
		 FROM users WHERE id = ?`, id))
}

func (s *userStore) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	return s.scanUser(s.q.QueryRowContext(ctx,
		`SELECT id, org_id, email, name, password_hash, role, phone, force_password_change, mfa_app_enabled, mfa_enforced_at, created_at, updated_at
		 FROM users WHERE email = ?`, email))
}

func (s *userStore) ListByOrg(ctx context.Context, orgID string, params store.ListParams) ([]*domain.User, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.q.QueryContext(ctx,
		`SELECT id, org_id, email, name, password_hash, role, phone, force_password_change, mfa_app_enabled, mfa_enforced_at, created_at, updated_at
		 FROM users WHERE org_id = ? ORDER BY email LIMIT ? OFFSET ?`,
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
	var mfaEnforcedAt sql.NullString
	if err := row.Scan(&u.ID, &u.OrgID, &u.Email, &u.Name, &u.PasswordHash, &role, &u.Phone, &u.ForcePasswordChange, &u.MFAAppEnabled, &mfaEnforcedAt, &createdAt, &updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user not found")
		}
		return nil, err
	}
	u.Role = domain.UserRole(role)
	if mfaEnforcedAt.Valid {
		u.MFAEnforcedAt = mfaEnforcedAt.String
	}
	u.CreatedAt = mustParseTime(createdAt)
	u.UpdatedAt = mustParseTime(updatedAt)
	return &u, nil
}

func (s *userStore) scanUserFromRows(rows *sql.Rows) (*domain.User, error) {
	var u domain.User
	var role, createdAt, updatedAt string
	var mfaEnforcedAt sql.NullString
	if err := rows.Scan(&u.ID, &u.OrgID, &u.Email, &u.Name, &u.PasswordHash, &role, &u.Phone, &u.ForcePasswordChange, &u.MFAAppEnabled, &mfaEnforcedAt, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	u.Role = domain.UserRole(role)
	if mfaEnforcedAt.Valid {
		u.MFAEnforcedAt = mfaEnforcedAt.String
	}
	u.CreatedAt = mustParseTime(createdAt)
	u.UpdatedAt = mustParseTime(updatedAt)
	return &u, nil
}
