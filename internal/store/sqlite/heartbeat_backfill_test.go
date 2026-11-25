package sqlite

import (
	"context"
	"io/fs"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// Migration 041 gives tokenless heartbeat monitors (old bulk imports) a
// fresh token, leaves existing tokens and other monitor types alone, and is
// safe to run twice (mon-8).
func TestMigration041HeartbeatTokenBackfill(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)

	empty := createTestMonitor(t, s, org.ID)
	empty.Type = domain.MonitorHeartbeat
	keep := createTestMonitor(t, s, org.ID)
	keep.Type = domain.MonitorHeartbeat
	other := createTestMonitor(t, s, org.ID)
	for _, m := range []*domain.Monitor{empty, keep} {
		if err := s.Monitors().Update(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE monitors SET heartbeat_token = 'existing' WHERE id = ?`, keep.ID); err != nil {
		t.Fatal(err)
	}

	data, err := fs.ReadFile(migrationsFS, "migrations/059_heartbeat_token_backfill.sql")
	if err != nil {
		t.Fatal(err)
	}
	var first string
	for i := range 2 {
		for _, stmt := range store.SplitSQLStatements(string(data)) {
			if _, err := s.db.ExecContext(ctx, stmt); err != nil {
				t.Fatalf("exec migration: %v", err)
			}
		}
		got, err := s.Monitors().GetByID(ctx, empty.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.HeartbeatToken) != 32 {
			t.Fatalf("backfilled token = %q, want 32 hex chars", got.HeartbeatToken)
		}
		if i == 0 {
			first = got.HeartbeatToken
		} else if got.HeartbeatToken != first {
			t.Fatal("second run replaced a token it had already set")
		}
	}

	if got, _ := s.Monitors().GetByID(ctx, keep.ID); got.HeartbeatToken != "existing" {
		t.Fatalf("existing token changed to %q", got.HeartbeatToken)
	}
	if got, _ := s.Monitors().GetByID(ctx, other.ID); got.HeartbeatToken != "" {
		t.Fatalf("http monitor got a token %q", got.HeartbeatToken)
	}
}
