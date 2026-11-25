package sqlx

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// PhoneVerificationStore implements store.PhoneVerificationStore.
type PhoneVerificationStore struct {
	q Queryable
	d Dialect
}

// NewPhoneVerificationStore returns a PhoneVerificationStore bound to q.
func NewPhoneVerificationStore(q Queryable, d Dialect) *PhoneVerificationStore {
	return &PhoneVerificationStore{q: q, d: d}
}

func (s *PhoneVerificationStore) Create(ctx context.Context, v *domain.PhoneVerification) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO phone_verifications
		   (id, org_id, subject_type, subject_id, phone, method, created_by, code_hash, attempts, expires_at, verified_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		v.ID, v.OrgID, v.SubjectType, v.SubjectID, v.Phone, v.Method, v.CreatedBy, v.CodeHash, v.Attempts,
		s.d.TimeArg(v.ExpiresAt), s.d.OptionalTimeArg(v.VerifiedAt), s.d.TimeArg(v.CreatedAt))
	return err
}

func (s *PhoneVerificationStore) Latest(ctx context.Context, subjectType, subjectID string) (*domain.PhoneVerification, error) {
	var v domain.PhoneVerification
	var expiresAt, verifiedAt, createdAt Time
	err := s.q.QueryRowContext(ctx,
		`SELECT id, org_id, subject_type, subject_id, phone, method, created_by, code_hash, attempts, expires_at, verified_at, created_at
		   FROM phone_verifications
		  WHERE subject_type = ? AND subject_id = ?
		  ORDER BY created_at DESC, id DESC
		  LIMIT 1`, subjectType, subjectID).
		Scan(&v.ID, &v.OrgID, &v.SubjectType, &v.SubjectID, &v.Phone, &v.Method, &v.CreatedBy, &v.CodeHash, &v.Attempts,
			&expiresAt, &verifiedAt, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v.ExpiresAt = expiresAt.T
	v.VerifiedAt = verifiedAt.TimePtr()
	v.CreatedAt = createdAt.T
	return &v, nil
}

func (s *PhoneVerificationStore) ConsumeAttempt(ctx context.Context, id string, maxAttempts int) (bool, error) {
	res, err := s.q.ExecContext(ctx,
		`UPDATE phone_verifications SET attempts = attempts + 1
		  WHERE id = ? AND attempts < ? AND verified_at IS NULL`, id, maxAttempts)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (s *PhoneVerificationStore) MarkVerified(ctx context.Context, id string, at time.Time) (bool, error) {
	res, err := s.q.ExecContext(ctx,
		`UPDATE phone_verifications SET verified_at = ? WHERE id = ? AND verified_at IS NULL`,
		s.d.TimeArg(at), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (s *PhoneVerificationStore) CountByOrgSince(ctx context.Context, orgID string, since time.Time) (int, error) {
	var n int
	err := s.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM phone_verifications WHERE org_id = ? AND created_at >= ?`,
		orgID, s.d.TimeArg(since)).Scan(&n)
	return n, err
}

func (s *PhoneVerificationStore) CountByActorSince(ctx context.Context, orgID, actorID string, since time.Time) (int, error) {
	var n int
	err := s.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM phone_verifications WHERE org_id = ? AND created_by = ? AND created_at >= ?`,
		orgID, actorID, s.d.TimeArg(since)).Scan(&n)
	return n, err
}

func (s *PhoneVerificationStore) CountByPhoneSince(ctx context.Context, phone string, since time.Time) (int, error) {
	var n int
	err := s.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM phone_verifications WHERE phone = ? AND created_at >= ?`,
		phone, s.d.TimeArg(since)).Scan(&n)
	return n, err
}

func (s *PhoneVerificationStore) Delete(ctx context.Context, id string) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM phone_verifications WHERE id = ?`, id)
	return err
}

func (s *PhoneVerificationStore) ReserveWindow(ctx context.Context, w store.BudgetWindow) (bool, error) {
	res, err := s.q.ExecContext(ctx,
		`INSERT INTO phone_verify_budget (bucket, count, expires_at)
		 VALUES (?, 1, ?)
		 ON CONFLICT (bucket) DO UPDATE SET count = phone_verify_budget.count + 1
		 WHERE phone_verify_budget.count < ?`,
		w.Key, s.d.TimeArg(w.WindowEnd), w.Limit)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (s *PhoneVerificationStore) ReleaseWindow(ctx context.Context, w store.BudgetWindow) error {
	_, err := s.q.ExecContext(ctx,
		`UPDATE phone_verify_budget SET count = count - 1 WHERE bucket = ? AND count > 0`,
		w.Key)
	return err
}

func (s *PhoneVerificationStore) ReserveCooldown(ctx context.Context, key string, now, until time.Time) (bool, error) {
	res, err := s.q.ExecContext(ctx,
		`INSERT INTO phone_verify_budget (bucket, count, expires_at)
		 VALUES (?, 1, ?)
		 ON CONFLICT (bucket) DO UPDATE SET expires_at = excluded.expires_at
		 WHERE phone_verify_budget.expires_at <= ?`,
		key, s.d.TimeArg(until), s.d.TimeArg(now))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (s *PhoneVerificationStore) ReleaseCooldown(ctx context.Context, key string) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM phone_verify_budget WHERE bucket = ?`, key)
	return err
}

func (s *PhoneVerificationStore) CleanupBudget(ctx context.Context, before time.Time) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM phone_verify_budget WHERE expires_at <= ?`, s.d.TimeArg(before))
	return err
}
