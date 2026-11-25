package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/google/uuid"
)

// An "All monitors" window (empty monitor_id) suppresses every monitor in
// its own org and nothing in another org (mon-7).
func TestListActiveByMonitorOrgWideWindow(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	orgA := createTestOrg(t, s)
	orgB := createTestOrg(t, s)
	monA1 := createTestMonitor(t, s, orgA.ID)
	monA2 := createTestMonitor(t, s, orgA.ID)
	monB := createTestMonitor(t, s, orgB.ID)
	now := time.Now().UTC().Truncate(time.Second)

	orgWide := crossOrgWindow(t, s, orgA.ID, "", now)

	for _, id := range []string{monA1.ID, monA2.ID} {
		active, err := s.MaintenanceWindows().ListActiveByMonitor(ctx, id, now)
		if err != nil {
			t.Fatal(err)
		}
		if len(active) != 1 || active[0].ID != orgWide.ID {
			t.Fatalf("org-wide window did not cover monitor %s: %+v", id, active)
		}
	}
	active, err := s.MaintenanceWindows().ListActiveByMonitor(ctx, monB.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("org A's window suppressed org B's monitor: %+v", active)
	}
}

// The window's timezone round-trips, and older inserts without one store UTC.
func TestMaintenanceWindowTimezoneRoundTrip(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	now := time.Now().UTC().Truncate(time.Second)

	mw := &domain.MaintenanceWindow{
		ID: uuid.New().String(), OrgID: org.ID, Name: "tz",
		StartAt: now, EndAt: now.Add(time.Hour), RecurrenceType: "daily",
		Timezone: "America/New_York", CreatedAt: now,
	}
	if err := s.MaintenanceWindows().Create(ctx, mw); err != nil {
		t.Fatal(err)
	}
	got, err := s.MaintenanceWindows().GetByID(ctx, mw.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Timezone != "America/New_York" {
		t.Fatalf("timezone = %q", got.Timezone)
	}
	got.Timezone = "Europe/Berlin"
	if err := s.MaintenanceWindows().Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	got, _ = s.MaintenanceWindows().GetByID(ctx, mw.ID)
	if got.Timezone != "Europe/Berlin" {
		t.Fatalf("updated timezone = %q", got.Timezone)
	}

	def := crossOrgWindow(t, s, org.ID, "", now)
	got, _ = s.MaintenanceWindows().GetByID(ctx, def.ID)
	if got.Timezone != "UTC" {
		t.Fatalf("default timezone = %q", got.Timezone)
	}
}
