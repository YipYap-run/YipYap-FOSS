package sqlite

import (
	"context"
	"testing"
)

func TestMigrationsApplyFromEmpty(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("fresh migrate: %v", err)
	}
	for _, tbl := range []string{"phone_verifications", "phone_verify_budget", "slack_installations", "discord_installations", "telegram_link_codes", "org_paid_channel_notices"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("table %s missing", tbl)
		}
	}
	var cols int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('orgs') WHERE name='paid_grace_until'`).Scan(&cols); err != nil {
		t.Fatal(err)
	}
	if cols != 1 {
		t.Error("orgs.paid_grace_until missing")
	}
	var versions int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	t.Logf("applied %d migrations", versions)
}
