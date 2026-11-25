package sqlite

import (
	"context"
	"io/fs"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/google/uuid"
)

func crossOrgWindow(t *testing.T, s *SQLiteStore, orgID, monitorID string, now time.Time) *domain.MaintenanceWindow {
	t.Helper()
	mw := &domain.MaintenanceWindow{
		ID:             uuid.New().String(),
		OrgID:          orgID,
		MonitorID:      monitorID,
		Name:           "window",
		StartAt:        now.Add(-time.Hour),
		EndAt:          now.Add(time.Hour),
		SuppressAlerts: true,
		CreatedAt:      now,
	}
	if err := s.MaintenanceWindows().Create(context.Background(), mw); err != nil {
		t.Fatal(err)
	}
	return mw
}

// A window that org B created against org A's monitor (possible before the
// handler checked ownership) must not suppress org A's alerts (mon-3).
func TestListActiveByMonitorIgnoresOtherOrgWindows(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	orgA := createTestOrg(t, s)
	orgB := createTestOrg(t, s)
	monA := createTestMonitor(t, s, orgA.ID)
	now := time.Now().UTC().Truncate(time.Second)

	crossOrgWindow(t, s, orgB.ID, monA.ID, now)
	active, err := s.MaintenanceWindows().ListActiveByMonitor(ctx, monA.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("cross-org window suppressed org A's monitor: %+v", active)
	}

	own := crossOrgWindow(t, s, orgA.ID, monA.ID, now)
	active, err = s.MaintenanceWindows().ListActiveByMonitor(ctx, monA.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != own.ID {
		t.Fatalf("expected only org A's own window, got %+v", active)
	}
}

// Migration 039 removes windows whose monitor belongs to another org and
// keeps same-org, org-wide and orphaned windows. Running it twice is safe.
func TestMigration039MaintenanceCrossOrgCleanup(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	orgA := createTestOrg(t, s)
	orgB := createTestOrg(t, s)
	monA := createTestMonitor(t, s, orgA.ID)
	now := time.Now().UTC().Truncate(time.Second)

	foreign := crossOrgWindow(t, s, orgB.ID, monA.ID, now)
	own := crossOrgWindow(t, s, orgA.ID, monA.ID, now)
	orgWide := crossOrgWindow(t, s, orgB.ID, "", now)
	orphan := crossOrgWindow(t, s, orgB.ID, uuid.New().String(), now)

	data, err := fs.ReadFile(migrationsFS, "migrations/057_maintenance_cross_org_cleanup.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		for _, stmt := range store.SplitSQLStatements(string(data)) {
			if _, err := s.db.ExecContext(ctx, stmt); err != nil {
				t.Fatalf("exec migration: %v", err)
			}
		}
	}

	if _, err := s.MaintenanceWindows().GetByID(ctx, foreign.ID); err == nil {
		t.Fatal("cross-org window survived the migration")
	}
	for _, keep := range []*domain.MaintenanceWindow{own, orgWide, orphan} {
		if _, err := s.MaintenanceWindows().GetByID(ctx, keep.ID); err != nil {
			t.Fatalf("window %s was removed: %v", keep.ID, err)
		}
	}
}
