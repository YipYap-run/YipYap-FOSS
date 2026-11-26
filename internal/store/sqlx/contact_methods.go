package sqlx

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// DefaultEmailMethodLabel names the contact method seeded from a user's
// verified account email.
const DefaultEmailMethodLabel = "Account email"

// ContactMethodStore implements store.ContactMethodStore across SQL backends.
type ContactMethodStore struct {
	q Queryable
	d Dialect
}

// NewContactMethodStore returns a ContactMethodStore bound to q.
func NewContactMethodStore(q Queryable, d Dialect) *ContactMethodStore {
	return &ContactMethodStore{q: q, d: d}
}

const contactMethodCols = `id, org_id, user_id, type, label, config, verified_at,
       verify_code_hash, verify_expires_at, verify_attempts, last_sent_at,
       created_at, updated_at`

func scanContactMethod(sc interface{ Scan(...any) error }) (*domain.ContactMethod, error) {
	var (
		m                                               domain.ContactMethod
		typ                                             string
		codeHash                                        sql.NullString
		verifiedAt, expiresAt, sentAt, created, updated Time
	)
	if err := sc.Scan(&m.ID, &m.OrgID, &m.UserID, &typ, &m.Label, &m.Config, &verifiedAt,
		&codeHash, &expiresAt, &m.VerifyAttempts, &sentAt, &created, &updated); err != nil {
		return nil, err
	}
	m.Type = domain.ContactMethodType(typ)
	m.VerifyCodeHash = codeHash.String
	m.VerifiedAt = verifiedAt.TimePtr()
	m.VerifyExpiresAt = expiresAt.TimePtr()
	m.LastSentAt = sentAt.TimePtr()
	m.CreatedAt = created.T
	m.UpdatedAt = updated.T
	return &m, nil
}

func (s *ContactMethodStore) Create(ctx context.Context, m *domain.ContactMethod) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO user_contact_methods (id, org_id, user_id, type, label, config, verified_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.OrgID, m.UserID, string(m.Type), m.Label, m.Config,
		s.d.OptionalTimeArg(m.VerifiedAt), s.d.TimeArg(m.CreatedAt), s.d.TimeArg(m.UpdatedAt))
	return err
}

func (s *ContactMethodStore) Get(ctx context.Context, userID, id string) (*domain.ContactMethod, error) {
	m, err := scanContactMethod(s.q.QueryRowContext(ctx,
		`SELECT `+contactMethodCols+` FROM user_contact_methods WHERE id = ? AND user_id = ?`, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrContactMethodNotFound
	}
	return m, err
}

func (s *ContactMethodStore) ListByUser(ctx context.Context, userID string) ([]*domain.ContactMethod, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+contactMethodCols+` FROM user_contact_methods WHERE user_id = ? ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*domain.ContactMethod
	for rows.Next() {
		m, err := scanContactMethod(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListByType implements store.ContactMethodStore.
func (s *ContactMethodStore) ListByType(ctx context.Context, t domain.ContactMethodType) ([]*domain.ContactMethod, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+contactMethodCols+` FROM user_contact_methods WHERE type = ? ORDER BY id`, string(t))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*domain.ContactMethod
	for rows.Next() {
		m, err := scanContactMethod(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ReplaceConfig implements store.ContactMethodStore. It leaves updated_at
// alone: re-encrypting a secret is not a user-visible change.
func (s *ContactMethodStore) ReplaceConfig(ctx context.Context, id, oldCfg, newCfg string) (bool, error) {
	res, err := s.q.ExecContext(ctx,
		`UPDATE user_contact_methods SET config = ? WHERE id = ? AND config = ?`, newCfg, id, oldCfg)
	return affectedOne(res, err)
}

func (s *ContactMethodStore) CountByUser(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_contact_methods WHERE user_id = ?`, userID).Scan(&n)
	return n, err
}

func (s *ContactMethodStore) Update(ctx context.Context, m *domain.ContactMethod, resetVerification bool) error {
	now := time.Now().UTC()
	var res sql.Result
	var err error
	if resetVerification {
		res, err = s.q.ExecContext(ctx,
			`UPDATE user_contact_methods
			 SET label = ?, config = ?, verified_at = NULL, verify_code_hash = NULL,
			     verify_expires_at = NULL, verify_attempts = 0, updated_at = ?
			 WHERE id = ? AND user_id = ?`,
			m.Label, m.Config, s.d.TimeArg(now), m.ID, m.UserID)
		m.VerifiedAt = nil
	} else {
		res, err = s.q.ExecContext(ctx,
			`UPDATE user_contact_methods SET label = ?, config = ?, updated_at = ? WHERE id = ? AND user_id = ?`,
			m.Label, m.Config, s.d.TimeArg(now), m.ID, m.UserID)
	}
	if err != nil {
		return err
	}
	m.UpdatedAt = now
	return notFoundAs(ExpectOneRow(res), store.ErrContactMethodNotFound)
}

func (s *ContactMethodStore) Delete(ctx context.Context, userID, id string) error {
	// Rules go with the method (ON DELETE CASCADE); delete them explicitly
	// too so a backend without enforced foreign keys stays consistent.
	if _, err := s.q.ExecContext(ctx,
		`DELETE FROM user_notification_rules WHERE contact_method_id = ? AND user_id = ?`, id, userID); err != nil {
		return err
	}
	res, err := s.q.ExecContext(ctx, `DELETE FROM user_contact_methods WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	return notFoundAs(ExpectOneRow(res), store.ErrContactMethodNotFound)
}

func (s *ContactMethodStore) SetVerificationCode(ctx context.Context, userID, id, codeHash string, expiresAt, sentAt time.Time) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE user_contact_methods
		 SET verify_code_hash = ?, verify_expires_at = ?, verify_attempts = 0, last_sent_at = ?
		 WHERE id = ? AND user_id = ?`,
		codeHash, s.d.TimeArg(expiresAt), s.d.TimeArg(sentAt), id, userID)
	if err != nil {
		return err
	}
	return notFoundAs(ExpectOneRow(res), store.ErrContactMethodNotFound)
}

func (s *ContactMethodStore) RecordSend(ctx context.Context, userID, id string, at time.Time) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE user_contact_methods SET last_sent_at = ? WHERE id = ? AND user_id = ?`,
		s.d.TimeArg(at), id, userID)
	if err != nil {
		return err
	}
	return notFoundAs(ExpectOneRow(res), store.ErrContactMethodNotFound)
}

func (s *ContactMethodStore) IncrementVerifyAttempts(ctx context.Context, userID, id string) (int, error) {
	res, err := s.q.ExecContext(ctx,
		`UPDATE user_contact_methods SET verify_attempts = verify_attempts + 1 WHERE id = ? AND user_id = ?`,
		id, userID)
	if err != nil {
		return 0, err
	}
	if err := notFoundAs(ExpectOneRow(res), store.ErrContactMethodNotFound); err != nil {
		return 0, err
	}
	var n int
	err = s.q.QueryRowContext(ctx,
		`SELECT verify_attempts FROM user_contact_methods WHERE id = ? AND user_id = ?`, id, userID).Scan(&n)
	return n, err
}

// ReserveSendBudget implements store.ContactMethodStore. Every bucket is
// reserved or none is: a later refusal returns the units already taken.
func (s *ContactMethodStore) ReserveSendBudget(ctx context.Context, userID, orgID string, at time.Time, phone string, userDailyLimit, orgSMSDailyLimit, phoneDailyLimit int) (bool, error) {
	day := at.UTC().Format("2006-01-02")
	if _, err := s.q.ExecContext(ctx,
		`DELETE FROM contact_method_send_budgets WHERE day < ?`,
		at.UTC().AddDate(0, 0, -2).Format("2006-01-02")); err != nil {
		return false, err
	}
	type bucket struct {
		scope, owner string
		limit        int
	}
	buckets := []bucket{{"user", userID, userDailyLimit}}
	if phone != "" {
		// The phone bucket is keyed by number alone, so it caps codes to one
		// number across every org and user.
		buckets = append(buckets, bucket{"phone", phone, phoneDailyLimit}, bucket{"org_sms", orgID, orgSMSDailyLimit})
	}
	for i, b := range buckets {
		ok, err := s.reserveSend(ctx, b.scope, b.owner, day, b.limit)
		if err == nil && ok {
			continue
		}
		for _, taken := range buckets[:i] {
			_ = s.releaseSend(ctx, taken.scope, taken.owner, day)
		}
		return false, err
	}
	return true, nil
}

func (s *ContactMethodStore) releaseSend(ctx context.Context, scope, ownerID, day string) error {
	_, err := s.q.ExecContext(ctx,
		`UPDATE contact_method_send_budgets SET count = count - 1
		  WHERE scope = ? AND owner_id = ? AND day = ? AND count > 0`,
		scope, ownerID, day)
	return err
}

func (s *ContactMethodStore) reserveSend(ctx context.Context, scope, ownerID, day string, limit int) (bool, error) {
	res, err := s.q.ExecContext(ctx,
		`INSERT INTO contact_method_send_budgets (scope, owner_id, day, count)
		 VALUES (?, ?, ?, 1)
		 ON CONFLICT (scope, owner_id, day) DO UPDATE
		 SET count = contact_method_send_budgets.count + 1
		 WHERE contact_method_send_budgets.count < ?`,
		scope, ownerID, day, limit)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *ContactMethodStore) MarkVerified(ctx context.Context, userID, id string, at time.Time) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE user_contact_methods
		 SET verified_at = ?, verify_code_hash = NULL, verify_expires_at = NULL, verify_attempts = 0, updated_at = ?
		 WHERE id = ? AND user_id = ?`,
		s.d.TimeArg(at), s.d.TimeArg(at), id, userID)
	if err != nil {
		return err
	}
	return notFoundAs(ExpectOneRow(res), store.ErrContactMethodNotFound)
}

func (s *ContactMethodStore) SeedDefaultEmail(ctx context.Context, userID string, at time.Time) error {
	var orgID, email string
	var verified Time
	err := s.q.QueryRowContext(ctx,
		`SELECT org_id, email, email_verified_at FROM users WHERE id = ?`, userID).Scan(&orgID, &email, &verified)
	if err != nil {
		return err
	}
	if !verified.Valid || email == "" {
		return nil
	}
	methods, err := s.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	for _, m := range methods {
		if m.Type != domain.ContactEmail {
			continue
		}
		var cfg struct {
			Address string `json:"address"`
		}
		if json.Unmarshal([]byte(m.Config), &cfg) == nil && cfg.Address == email {
			return nil
		}
	}
	cfg, err := json.Marshal(map[string]string{"address": email})
	if err != nil {
		return err
	}
	at = at.UTC()
	m := &domain.ContactMethod{
		ID: uuid.New().String(), OrgID: orgID, UserID: userID,
		Type: domain.ContactEmail, Label: DefaultEmailMethodLabel, Config: string(cfg),
		VerifiedAt: &at, CreatedAt: at, UpdatedAt: at,
	}
	if err := s.Create(ctx, m); err != nil {
		return err
	}
	return NewNotificationRuleStore(s.q, s.d).Create(ctx, &domain.NotificationRule{
		ID: uuid.New().String(), OrgID: orgID, UserID: userID, ContactMethodID: m.ID,
		DelayMinutes: 0, Urgency: domain.UrgencyAny, CreatedAt: at,
	})
}

// NotificationRuleStore implements store.NotificationRuleStore across SQL
// backends.
type NotificationRuleStore struct {
	q Queryable
	d Dialect
}

// NewNotificationRuleStore returns a NotificationRuleStore bound to q.
func NewNotificationRuleStore(q Queryable, d Dialect) *NotificationRuleStore {
	return &NotificationRuleStore{q: q, d: d}
}

const notificationRuleCols = `id, org_id, user_id, contact_method_id, delay_minutes, urgency, created_at`

func scanNotificationRule(sc interface{ Scan(...any) error }) (*domain.NotificationRule, error) {
	var r domain.NotificationRule
	var urgency string
	var created Time
	if err := sc.Scan(&r.ID, &r.OrgID, &r.UserID, &r.ContactMethodID, &r.DelayMinutes, &urgency, &created); err != nil {
		return nil, err
	}
	r.Urgency = domain.NotificationUrgency(urgency)
	r.CreatedAt = created.T
	return &r, nil
}

// Create inserts the rule. The contact method must belong to the same user;
// the insert copies nothing from a method owned by someone else.
func (s *NotificationRuleStore) Create(ctx context.Context, r *domain.NotificationRule) error {
	res, err := s.q.ExecContext(ctx,
		`INSERT INTO user_notification_rules (id, org_id, user_id, contact_method_id, delay_minutes, urgency, created_at)
		 SELECT ?, ?, ?, m.id, ?, ?, ? FROM user_contact_methods m WHERE m.id = ? AND m.user_id = ?`,
		r.ID, r.OrgID, r.UserID, r.DelayMinutes, string(r.Urgency), s.d.TimeArg(r.CreatedAt),
		r.ContactMethodID, r.UserID)
	if err != nil {
		return err
	}
	return notFoundAs(ExpectOneRow(res), store.ErrContactMethodNotFound)
}

func (s *NotificationRuleStore) Get(ctx context.Context, userID, id string) (*domain.NotificationRule, error) {
	r, err := scanNotificationRule(s.q.QueryRowContext(ctx,
		`SELECT `+notificationRuleCols+` FROM user_notification_rules WHERE id = ? AND user_id = ?`, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotificationRuleNotFound
	}
	return r, err
}

func (s *NotificationRuleStore) ListByUser(ctx context.Context, userID string) ([]*domain.NotificationRule, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+notificationRuleCols+` FROM user_notification_rules WHERE user_id = ?
		 ORDER BY delay_minutes, created_at, id`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*domain.NotificationRule
	for rows.Next() {
		r, err := scanNotificationRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *NotificationRuleStore) CountByUser(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_notification_rules WHERE user_id = ?`, userID).Scan(&n)
	return n, err
}

// Update rewrites delay, urgency and method. The new method must belong to
// the rule's owner.
func (s *NotificationRuleStore) Update(ctx context.Context, r *domain.NotificationRule) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE user_notification_rules SET contact_method_id = ?, delay_minutes = ?, urgency = ?
		 WHERE id = ? AND user_id = ?
		   AND EXISTS (SELECT 1 FROM user_contact_methods m WHERE m.id = ? AND m.user_id = ?)`,
		r.ContactMethodID, r.DelayMinutes, string(r.Urgency), r.ID, r.UserID, r.ContactMethodID, r.UserID)
	if err != nil {
		return err
	}
	return notFoundAs(ExpectOneRow(res), store.ErrNotificationRuleNotFound)
}

func (s *NotificationRuleStore) Delete(ctx context.Context, userID, id string) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM user_notification_rules WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	return notFoundAs(ExpectOneRow(res), store.ErrNotificationRuleNotFound)
}

func (s *NotificationRuleStore) ListPagingTargets(ctx context.Context, userID string) ([]domain.PagingTarget, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT r.id, r.org_id, r.user_id, r.contact_method_id, r.delay_minutes, r.urgency, r.created_at,
		        m.id, m.org_id, m.user_id, m.type, m.label, m.config, m.verified_at,
		        m.verify_code_hash, m.verify_expires_at, m.verify_attempts, m.last_sent_at,
		        m.created_at, m.updated_at
		 FROM user_notification_rules r
		 JOIN user_contact_methods m ON m.id = r.contact_method_id AND m.user_id = r.user_id
		 WHERE r.user_id = ? AND m.verified_at IS NOT NULL
		 ORDER BY r.delay_minutes, r.created_at, r.id`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []domain.PagingTarget
	for rows.Next() {
		var (
			r                                               domain.NotificationRule
			urgency, typ                                    string
			rCreated                                        Time
			m                                               domain.ContactMethod
			codeHash                                        sql.NullString
			verifiedAt, expiresAt, sentAt, created, updated Time
		)
		if err := rows.Scan(&r.ID, &r.OrgID, &r.UserID, &r.ContactMethodID, &r.DelayMinutes, &urgency, &rCreated,
			&m.ID, &m.OrgID, &m.UserID, &typ, &m.Label, &m.Config, &verifiedAt,
			&codeHash, &expiresAt, &m.VerifyAttempts, &sentAt, &created, &updated); err != nil {
			return nil, err
		}
		r.Urgency = domain.NotificationUrgency(urgency)
		r.CreatedAt = rCreated.T
		m.Type = domain.ContactMethodType(typ)
		m.VerifyCodeHash = codeHash.String
		m.VerifiedAt = verifiedAt.TimePtr()
		m.VerifyExpiresAt = expiresAt.TimePtr()
		m.LastSentAt = sentAt.TimePtr()
		m.CreatedAt = created.T
		m.UpdatedAt = updated.T
		rr, mm := r, m
		out = append(out, domain.PagingTarget{Rule: &rr, Method: &mm})
	}
	return out, rows.Err()
}

// PagingTypesByOrg implements store.NotificationRuleStore.
func (s *NotificationRuleStore) PagingTypesByOrg(ctx context.Context, orgID string) (map[string][]domain.ContactMethodType, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT DISTINCT r.user_id, m.type
		 FROM user_notification_rules r
		 JOIN user_contact_methods m ON m.id = r.contact_method_id AND m.user_id = r.user_id
		 WHERE r.org_id = ? AND m.org_id = ? AND m.verified_at IS NOT NULL
		 ORDER BY r.user_id, m.type`, orgID, orgID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string][]domain.ContactMethodType{}
	for rows.Next() {
		var userID, typ string
		if err := rows.Scan(&userID, &typ); err != nil {
			return nil, err
		}
		out[userID] = append(out[userID], domain.ContactMethodType(typ))
	}
	return out, rows.Err()
}

// notFoundAs maps ExpectOneRow's generic "not found" to a typed error.
func notFoundAs(err, typed error) error {
	if err != nil && err.Error() == "not found" {
		return typed
	}
	return err
}
