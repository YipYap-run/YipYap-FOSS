package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

type orgStore struct{ q queryable }

func (s *orgStore) Create(ctx context.Context, org *domain.Org) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO orgs (id, name, slug, plan, oncall_display, mfa_required, mfa_grace_days, promo_expires_at, promo_grace_days, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		org.ID, org.Name, org.Slug, string(org.Plan), org.OncallDisplay,
		org.MFARequired, org.MFAGraceDays,
		nullString(org.PromoExpiresAt), org.PromoGraceDays,
		org.CreatedAt.UTC().Format(timeFormat),
		org.UpdatedAt.UTC().Format(timeFormat),
	)
	return err
}

func (s *orgStore) GetByID(ctx context.Context, id string) (*domain.Org, error) {
	return s.scanOrg(s.q.QueryRowContext(ctx,
		`SELECT id, name, slug, plan, oncall_display, mfa_required, mfa_grace_days, promo_expires_at, promo_grace_days, created_at, updated_at FROM orgs WHERE id = ?`, id))
}

func (s *orgStore) GetBySlug(ctx context.Context, slug string) (*domain.Org, error) {
	return s.scanOrg(s.q.QueryRowContext(ctx,
		`SELECT id, name, slug, plan, oncall_display, mfa_required, mfa_grace_days, promo_expires_at, promo_grace_days, created_at, updated_at FROM orgs WHERE slug = ?`, slug))
}

func (s *orgStore) Update(ctx context.Context, org *domain.Org) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE orgs SET name = ?, slug = ?, plan = ?, oncall_display = ?, mfa_required = ?, mfa_grace_days = ?, promo_expires_at = ?, promo_grace_days = ?, updated_at = ? WHERE id = ?`,
		org.Name, org.Slug, string(org.Plan), org.OncallDisplay,
		org.MFARequired, org.MFAGraceDays,
		nullString(org.PromoExpiresAt), org.PromoGraceDays,
		org.UpdatedAt.UTC().Format(timeFormat), org.ID,
	)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

func (s *orgStore) Delete(ctx context.Context, id string) error {
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
		"notification_channels", "maintenance_windows",
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
		"billing",
		"users",
		"org_settings",
	}
	for _, table := range tables {
		_, _ = s.q.ExecContext(ctx,
			`DELETE FROM `+table+` WHERE org_id = ?`, id)
	}
	_, err := s.q.ExecContext(ctx, `DELETE FROM orgs WHERE id = ?`, id)
	return err
}

func (s *orgStore) scanOrg(row *sql.Row) (*domain.Org, error) {
	var o domain.Org
	var plan, createdAt, updatedAt string
	var promoExpiresAt sql.NullString
	if err := row.Scan(&o.ID, &o.Name, &o.Slug, &plan, &o.OncallDisplay, &o.MFARequired, &o.MFAGraceDays, &promoExpiresAt, &o.PromoGraceDays, &createdAt, &updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("org not found")
		}
		return nil, err
	}
	o.Plan = domain.OrgPlan(plan)
	if promoExpiresAt.Valid {
		o.PromoExpiresAt = promoExpiresAt.String
	}
	o.CreatedAt = mustParseTime(createdAt)
	o.UpdatedAt = mustParseTime(updatedAt)
	return &o, nil
}
