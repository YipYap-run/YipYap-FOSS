package sqlx

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// OrgStore implements store.OrgStore across SQL backends.
type OrgStore struct {
	q Queryable
	d Dialect
}

// NewOrgStore returns an OrgStore bound to q.
func NewOrgStore(q Queryable, d Dialect) *OrgStore {
	return &OrgStore{q: q, d: d}
}

func (s *OrgStore) Create(ctx context.Context, org *domain.Org) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO orgs (id, name, slug, plan, oncall_display, mfa_required, mfa_grace_days, sso_enforced, promo_expires_at, promo_grace_days, paid_grace_until, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		org.ID, org.Name, org.Slug, string(org.Plan), org.OncallDisplay,
		s.d.BoolArg(org.MFARequired), org.MFAGraceDays, s.d.BoolArg(org.SSOEnforced),
		NullString(org.PromoExpiresAt), org.PromoGraceDays,
		s.d.OptionalTimeArg(org.PaidGraceUntil),
		s.d.TimeArg(org.CreatedAt),
		s.d.TimeArg(org.UpdatedAt),
	)
	return err
}

func (s *OrgStore) GetByID(ctx context.Context, id string) (*domain.Org, error) {
	return s.scanOrg(s.q.QueryRowContext(ctx,
		`SELECT id, name, slug, plan, oncall_display, mfa_required, mfa_grace_days, sso_enforced, promo_expires_at, promo_grace_days, paid_grace_until, created_at, updated_at FROM orgs WHERE id = ?`, id))
}

func (s *OrgStore) GetBySlug(ctx context.Context, slug string) (*domain.Org, error) {
	return s.scanOrg(s.q.QueryRowContext(ctx,
		`SELECT id, name, slug, plan, oncall_display, mfa_required, mfa_grace_days, sso_enforced, promo_expires_at, promo_grace_days, paid_grace_until, created_at, updated_at FROM orgs WHERE slug = ?`, slug))
}

// Update writes org. paid_grace_until follows the plan change: an upgrade
// clears it, a drop to Free from any other plan sets it (to
// org.PaidGraceUntil when the caller set one, else now plus the org's
// paid grace days), and staying on Free keeps the stored value. The
// CASE reads the row's old plan, so every downgrade path gets the
// deadline without each caller computing it.
func (s *OrgStore) Update(ctx context.Context, org *domain.Org) error {
	graceUntil := org.PaidGraceUntil
	if graceUntil == nil {
		t := time.Now().UTC().Truncate(time.Second).Add(time.Duration(org.PaidGraceDays()) * 24 * time.Hour)
		graceUntil = &t
	}
	res, err := s.q.ExecContext(ctx,
		`UPDATE orgs SET name = ?, slug = ?, plan = ?, oncall_display = ?, mfa_required = ?, mfa_grace_days = ?, sso_enforced = ?, promo_expires_at = ?, promo_grace_days = ?,
		        paid_grace_until = CASE
		            WHEN CAST(? AS TEXT) <> 'free' THEN NULL
		            WHEN plan <> 'free' THEN ?
		            ELSE paid_grace_until
		        END,
		        updated_at = ? WHERE id = ?`,
		org.Name, org.Slug, string(org.Plan), org.OncallDisplay,
		s.d.BoolArg(org.MFARequired), org.MFAGraceDays, s.d.BoolArg(org.SSOEnforced),
		NullString(org.PromoExpiresAt), org.PromoGraceDays,
		string(org.Plan), s.d.OptionalTimeArg(graceUntil),
		s.d.TimeArg(org.UpdatedAt), org.ID,
	)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

func (s *OrgStore) Delete(ctx context.Context, id string) error {
	// Clean up rows that reference a parent by id but declare neither
	// org_id nor a foreign key, so ON DELETE CASCADE does not reach them.
	_, _ = s.q.ExecContext(ctx,
		`DELETE FROM alert_status_page_refs WHERE alert_id IN (SELECT id FROM alerts WHERE org_id = ?)`, id)
	_, _ = s.q.ExecContext(ctx,
		`DELETE FROM cloudevent_ingest_idempotency WHERE monitor_id IN (SELECT id FROM monitors WHERE org_id = ?)`, id)

	// Delete every org_id-bearing table. Their children are removed by
	// ON DELETE CASCADE (foreign_keys=ON). Ordering matters only for two
	// non-cascade references: alerts.monitor_id (so alerts before monitors)
	// and monitor_match_rules.state_id (so monitors before
	// org_monitor_states).
	tables := []string{
		"alert_remediations", "alert_routing_rules", "alert_suppressions",
		"cloudevent_reply_audit",
		"alerts",
		"escalation_policies",
		"notification_channels", "phone_verifications", "maintenance_windows",
		"teams",
		"api_keys",
		"monitors",
		"monitor_groups",
		"org_monitor_states",
		"services",
		"incidents",
		"status_pages",
		"support_tickets",
		"oidc_connections",
		"billing", "billing_checkout_attempts",
		"sms_voice_usage", "sms_voice_usage_warnings", "promo_expiry_warnings",
		"org_invites",
		"users",
		"org_settings",
		"org_paid_channel_notices",
	}
	for _, table := range tables {
		_, _ = s.q.ExecContext(ctx,
			`DELETE FROM `+table+` WHERE org_id = ?`, id)
	}
	_, err := s.q.ExecContext(ctx, `DELETE FROM orgs WHERE id = ?`, id)
	return err
}

func (s *OrgStore) scanOrg(row Scanner) (*domain.Org, error) {
	var o domain.Org
	var plan string
	var mfaRequired, ssoEnforced Bool
	var createdAt, updatedAt Time
	var promoExpiresAt sql.NullString
	var paidGraceUntil Time
	if err := row.Scan(&o.ID, &o.Name, &o.Slug, &plan, &o.OncallDisplay, &mfaRequired, &o.MFAGraceDays, &ssoEnforced, &promoExpiresAt, &o.PromoGraceDays, &paidGraceUntil, &createdAt, &updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("org not found")
		}
		return nil, err
	}
	o.MFARequired = bool(mfaRequired)
	o.SSOEnforced = bool(ssoEnforced)
	o.Plan = domain.OrgPlan(plan)
	if promoExpiresAt.Valid {
		o.PromoExpiresAt = promoExpiresAt.String
	}
	if paidGraceUntil.Valid {
		t := paidGraceUntil.T
		o.PaidGraceUntil = &t
	}
	o.CreatedAt = createdAt.T
	o.UpdatedAt = updatedAt.T
	return &o, nil
}
