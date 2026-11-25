package sqlite

import (
	"context"
	"testing"
)

// The channel-permissions seed must fill in defaults for existing orgs,
// keep any value an org already set, and be safe to run twice.
func TestChannelPermissionsMigrationSeedsDefaults(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	seeded := createTestOrg(t, s)
	custom := createTestOrg(t, s)
	if err := s.OrgSettings().Set(ctx, custom.ID, "channel_manage_roles", "owner,admin,member"); err != nil {
		t.Fatal(err)
	}

	sqlText, err := migrationsFS.ReadFile("migrations/065_channel_permissions.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := s.db.ExecContext(ctx, string(sqlText)); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}

	check := func(orgID, key, want string) {
		t.Helper()
		got, err := s.OrgSettings().Get(ctx, orgID, key)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	check(seeded.ID, "channel_manage_roles", "owner,admin")
	check(seeded.ID, "channel_test_roles", "owner,admin,member")
	check(custom.ID, "channel_manage_roles", "owner,admin,member")
	check(custom.ID, "channel_test_roles", "owner,admin,member")
}
