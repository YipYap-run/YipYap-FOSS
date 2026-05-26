package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func TestCheckCreateAndGetLatest(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	mon := createTestMonitor(t, s, org.ID)

	now := time.Now().UTC().Truncate(time.Second)
	check := &domain.MonitorCheck{
		ID:         uuid.New().String(),
		MonitorID:  mon.ID,
		Status:     domain.StatusUp,
		LatencyMS:  42,
		StatusCode: 200,
		CheckedAt:  now,
	}
	if err := s.Checks().Create(ctx, check); err != nil {
		t.Fatal(err)
	}

	got, err := s.Checks().GetLatest(ctx, mon.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.StatusUp || got.LatencyMS != 42 {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestCheckListByMonitor(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	mon := createTestMonitor(t, s, org.ID)

	now := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 3; i++ {
		c := &domain.MonitorCheck{
			ID:        uuid.New().String(),
			MonitorID: mon.ID,
			Status:    domain.StatusUp,
			LatencyMS: 10 + i,
			CheckedAt: now.Add(time.Duration(i) * time.Minute),
		}
		if err := s.Checks().Create(ctx, c); err != nil {
			t.Fatal(err)
		}
	}

	checks, err := s.Checks().ListByMonitor(ctx, mon.ID, store.CheckFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 3 {
		t.Fatalf("expected 3 checks, got %d", len(checks))
	}
}

func TestCheckPruneBefore(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	mon := createTestMonitor(t, s, org.ID)

	now := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 5; i++ {
		c := &domain.MonitorCheck{
			ID:        uuid.New().String(),
			MonitorID: mon.ID,
			Status:    domain.StatusUp,
			CheckedAt: now.Add(time.Duration(i-3) * time.Hour),
		}
		if err := s.Checks().Create(ctx, c); err != nil {
			t.Fatal(err)
		}
	}

	pruned, err := s.Checks().PruneBefore(ctx, mon.ID, now.Add(-1*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 2 {
		t.Fatalf("expected 2 pruned, got %d", pruned)
	}
}

func TestCheckRollup(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	mon := createTestMonitor(t, s, org.ID)

	now := time.Now().UTC().Truncate(time.Second)
	r := &domain.MonitorRollup{
		MonitorID:    mon.ID,
		Period:       "hourly",
		PeriodStart:  now,
		UptimePct:    99.5,
		AvgLatencyMS: 42.0,
		CheckCount:   60,
		FailureCount: 1,
	}
	if err := s.Checks().CreateRollup(ctx, r); err != nil {
		t.Fatal(err)
	}

	rollups, err := s.Checks().GetRollups(ctx, mon.ID, "hourly")
	if err != nil {
		t.Fatal(err)
	}
	if len(rollups) != 1 || rollups[0].UptimePct != 99.5 {
		t.Fatalf("unexpected rollups: %+v", rollups)
	}
}

func TestCheckUpsertRollup(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	mon := createTestMonitor(t, s, org.ID)

	now := time.Now().UTC().Truncate(time.Second)
	r := &domain.MonitorRollup{
		MonitorID:    mon.ID,
		Period:       "hourly",
		PeriodStart:  now,
		UptimePct:    90.0,
		AvgLatencyMS: 50.0,
		P95LatencyMS: 80.0,
		P99LatencyMS: 120.0,
		CheckCount:   10,
		FailureCount: 1,
	}
	if err := s.Checks().UpsertRollup(ctx, r); err != nil {
		t.Fatal(err)
	}

	// Upsert the same key with new metric values.
	r2 := &domain.MonitorRollup{
		MonitorID:    mon.ID,
		Period:       "hourly",
		PeriodStart:  now,
		UptimePct:    99.9,
		AvgLatencyMS: 60.0,
		P95LatencyMS: 90.0,
		P99LatencyMS: 130.0,
		CheckCount:   20,
		FailureCount: 0,
	}
	if err := s.Checks().UpsertRollup(ctx, r2); err != nil {
		t.Fatalf("upsert on existing key errored: %v", err)
	}

	rollups, err := s.Checks().GetRollups(ctx, mon.ID, "hourly")
	if err != nil {
		t.Fatal(err)
	}
	if len(rollups) != 1 {
		t.Fatalf("expected 1 rollup (updated, not duplicated), got %d", len(rollups))
	}
	got := rollups[0]
	if got.UptimePct != 99.9 || got.AvgLatencyMS != 60.0 || got.P95LatencyMS != 90.0 ||
		got.P99LatencyMS != 130.0 || got.CheckCount != 20 || got.FailureCount != 0 {
		t.Fatalf("row was not updated to new values: %+v", got)
	}
}
