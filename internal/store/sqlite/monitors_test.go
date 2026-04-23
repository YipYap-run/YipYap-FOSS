package sqlite

import (
	"context"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func TestMonitorCreateAndGet(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	mon := createTestMonitor(t, s, org.ID)

	got, err := s.Monitors().GetByID(ctx, mon.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Test Monitor" || !got.Enabled {
		t.Fatalf("unexpected: %+v", got)
	}
	if len(got.Regions) != 1 || got.Regions[0] != "us-east-1" {
		t.Fatalf("regions mismatch: %v", got.Regions)
	}
}

func TestMonitorListByOrg(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	createTestMonitor(t, s, org.ID)
	createTestMonitor(t, s, org.ID)

	mons, err := s.Monitors().ListByOrg(ctx, org.ID, store.MonitorFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(mons) != 2 {
		t.Fatalf("expected 2 monitors, got %d", len(mons))
	}
}

func TestMonitorLabels(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	mon := createTestMonitor(t, s, org.ID)

	labels := map[string]string{"env": "prod", "tier": "frontend"}
	if err := s.Monitors().SetLabels(ctx, mon.ID, labels); err != nil {
		t.Fatal(err)
	}

	got, err := s.Monitors().GetLabels(ctx, mon.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got["env"] != "prod" || got["tier"] != "frontend" {
		t.Fatalf("labels mismatch: %v", got)
	}

	if err := s.Monitors().DeleteLabel(ctx, mon.ID, "tier"); err != nil {
		t.Fatal(err)
	}
	got, err = s.Monitors().GetLabels(ctx, mon.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 label after delete, got %d", len(got))
	}
}
