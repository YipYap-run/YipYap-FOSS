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
	// Delete all org-scoped data. Order matters for foreign key constraints:
	// children first, then parents.
	tables := []string{
		"monitor_checks", "monitor_rollups", "monitor_labels",
		"alert_events", "alert_escalation_state", "alerts",
		"step_targets", "escalation_steps", "escalation_policies",
		"notification_channels", "maintenance_windows",
		"schedule_overrides", "schedules", "team_members", "teams",
		"api_keys", "monitors", "users", "org_settings",
	}
	for _, table := range tables {
		if _, err := s.q.ExecContext(ctx,
			`DELETE FROM `+table+` WHERE org_id = ?`, id); err != nil {
			// Some tables may not have org_id (e.g., monitor_checks references
			// monitors, not orgs directly). Skip errors for those.
			continue
		}
	}
	// Delete child data that references monitors (not org_id directly).
	_, _ = s.q.ExecContext(ctx,
		`DELETE FROM monitor_checks WHERE monitor_id NOT IN (SELECT id FROM monitors)`)
	_, _ = s.q.ExecContext(ctx,
		`DELETE FROM monitor_rollups WHERE monitor_id NOT IN (SELECT id FROM monitors)`)
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
