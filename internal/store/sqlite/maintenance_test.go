package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func TestMaintenanceWindowCreateAndGet(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	mon := createTestMonitor(t, s, org.ID)

	now := time.Now().UTC().Truncate(time.Second)
	mw := &domain.MaintenanceWindow{
		ID:             uuid.New().String(),
		OrgID:          org.ID,
		MonitorID:      mon.ID,
		Name:           "Deploy window",
		StartAt:        now,
		EndAt:          now.Add(2 * time.Hour),
		Public:         true,
		SuppressAlerts: true,
		CreatedAt:      now,
		CreatedBy:      "user-1",
	}
	if err := s.MaintenanceWindows().Create(ctx, mw); err != nil {
		t.Fatal(err)
	}

	got, err := s.MaintenanceWindows().GetByID(ctx, mw.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Deploy window" || !got.Public {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestMaintenanceWindowListActiveByMonitor(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	mon := createTestMonitor(t, s, org.ID)

	now := time.Now().UTC().Truncate(time.Second)
	mw := &domain.MaintenanceWindow{
		ID:             uuid.New().String(),
		OrgID:          org.ID,
		MonitorID:      mon.ID,
		Name:           "Active window",
		StartAt:        now.Add(-1 * time.Hour),
		EndAt:          now.Add(1 * time.Hour),
		SuppressAlerts: true,
		CreatedAt:      now,
	}
	if err := s.MaintenanceWindows().Create(ctx, mw); err != nil {
		t.Fatal(err)
	}

	active, err := s.MaintenanceWindows().ListActiveByMonitor(ctx, mon.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 {
		t.Fatalf("expected 1 active, got %d", len(active))
	}
}

func TestMaintenanceWindowListByOrg(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)

	now := time.Now().UTC().Truncate(time.Second)
	mw := &domain.MaintenanceWindow{
		ID:        uuid.New().String(),
		OrgID:     org.ID,
		Name:      "Org window",
		StartAt:   now,
		EndAt:     now.Add(time.Hour),
		CreatedAt: now,
	}
	if err := s.MaintenanceWindows().Create(ctx, mw); err != nil {
		t.Fatal(err)
	}

	windows, err := s.MaintenanceWindows().ListByOrg(ctx, org.ID, store.ListParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 1 {
		t.Fatalf("expected 1, got %d", len(windows))
	}
}
