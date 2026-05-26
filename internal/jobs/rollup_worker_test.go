package jobs

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

func newRollupTestStore(t *testing.T) *sqlite.SQLiteStore {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func seedRollupOrgMonitor(t *testing.T, s store.Store) string {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	org := &domain.Org{
		ID:        uuid.New().String(),
		Name:      "Rollup Org",
		Slug:      "rollup-" + uuid.New().String()[:8],
		Plan:      domain.PlanFree,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.Orgs().Create(ctx, org); err != nil {
		t.Fatal(err)
	}
	m := &domain.Monitor{
		ID:              uuid.New().String(),
		OrgID:           org.ID,
		Name:            "Rollup Monitor",
		Type:            domain.MonitorHTTP,
		Config:          json.RawMessage(`{"url":"https://example.com"}`),
		IntervalSeconds: 60,
		TimeoutSeconds:  10,
		Regions:         []string{"us-east-1"},
		Enabled:         true,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.Monitors().Create(ctx, m); err != nil {
		t.Fatal(err)
	}
	return m.ID
}

func TestRunRollupsAsOfHourly(t *testing.T) {
	s := newRollupTestStore(t)
	ctx := context.Background()
	monitorID := seedRollupOrgMonitor(t, s)

	// asOf at 02:00 UTC -> just-completed hour is [01:00, 02:00).
	asOf := time.Date(2026, 5, 24, 2, 0, 0, 0, time.UTC)
	hourStart := time.Date(2026, 5, 24, 1, 0, 0, 0, time.UTC)

	// 4 checks inside the window: 3 up, 1 down -> 75% uptime.
	statuses := []domain.CheckStatus{domain.StatusUp, domain.StatusUp, domain.StatusUp, domain.StatusDown}
	latencies := []int{10, 20, 30, 40}
	for i, st := range statuses {
		c := &domain.MonitorCheck{
			ID:        uuid.New().String(),
			MonitorID: monitorID,
			Status:    st,
			LatencyMS: latencies[i],
			CheckedAt: hourStart.Add(time.Duration(i+1) * time.Minute),
		}
		if err := s.Checks().Create(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	// One check outside the window (in the current, not-yet-closed hour) must
	// be ignored.
	if err := s.Checks().Create(ctx, &domain.MonitorCheck{
		ID:        uuid.New().String(),
		MonitorID: monitorID,
		Status:    domain.StatusDown,
		LatencyMS: 999,
		CheckedAt: asOf.Add(5 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	if err := runRollupsAsOf(ctx, s, asOf); err != nil {
		t.Fatalf("runRollupsAsOf: %v", err)
	}

	rollups, err := s.Checks().GetRollups(ctx, monitorID, "hourly")
	if err != nil {
		t.Fatal(err)
	}
	if len(rollups) != 1 {
		t.Fatalf("expected 1 hourly rollup, got %d", len(rollups))
	}
	r := rollups[0]
	if !r.PeriodStart.Equal(hourStart) {
		t.Fatalf("period_start: want %v, got %v", hourStart, r.PeriodStart)
	}
	if r.CheckCount != 4 {
		t.Fatalf("check_count: want 4 (window only), got %d", r.CheckCount)
	}
	if r.FailureCount != 1 {
		t.Fatalf("failure_count: want 1, got %d", r.FailureCount)
	}
	if r.UptimePct != 75.0 {
		t.Fatalf("uptime_pct: want 75, got %v", r.UptimePct)
	}
	if r.AvgLatencyMS != 25.0 {
		t.Fatalf("avg_latency_ms: want 25, got %v", r.AvgLatencyMS)
	}

	// No daily rollup should exist (asOf is not at the top of a day).
	daily, err := s.Checks().GetRollups(ctx, monitorID, "daily")
	if err != nil {
		t.Fatal(err)
	}
	if len(daily) != 0 {
		t.Fatalf("expected no daily rollup at 02:00, got %d", len(daily))
	}

	// A second run must upsert (no error, no duplicate).
	if err := runRollupsAsOf(ctx, s, asOf); err != nil {
		t.Fatalf("second run errored: %v", err)
	}
	rollups2, err := s.Checks().GetRollups(ctx, monitorID, "hourly")
	if err != nil {
		t.Fatal(err)
	}
	if len(rollups2) != 1 {
		t.Fatalf("second run duplicated rollup: got %d rows", len(rollups2))
	}
}

func TestRunRollupsAsOfDaily(t *testing.T) {
	s := newRollupTestStore(t)
	ctx := context.Background()
	monitorID := seedRollupOrgMonitor(t, s)

	// asOf at 00:00 UTC -> just-completed hour is the previous day's 23:00,
	// and the previous full day is now complete.
	asOf := time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)
	dayStart := time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC)
	lastHourStart := time.Date(2026, 5, 23, 23, 0, 0, 0, time.UTC)

	// Check in the last hour of the previous day (counts for both hourly and daily).
	if err := s.Checks().Create(ctx, &domain.MonitorCheck{
		ID:        uuid.New().String(),
		MonitorID: monitorID,
		Status:    domain.StatusUp,
		LatencyMS: 50,
		CheckedAt: lastHourStart.Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	// Earlier check in the day (counts only for daily).
	if err := s.Checks().Create(ctx, &domain.MonitorCheck{
		ID:        uuid.New().String(),
		MonitorID: monitorID,
		Status:    domain.StatusDown,
		LatencyMS: 150,
		CheckedAt: dayStart.Add(2 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	if err := runRollupsAsOf(ctx, s, asOf); err != nil {
		t.Fatalf("runRollupsAsOf: %v", err)
	}

	hourly, err := s.Checks().GetRollups(ctx, monitorID, "hourly")
	if err != nil {
		t.Fatal(err)
	}
	if len(hourly) != 1 || hourly[0].CheckCount != 1 || hourly[0].UptimePct != 100.0 {
		t.Fatalf("unexpected hourly rollup: %+v", hourly)
	}
	if !hourly[0].PeriodStart.Equal(lastHourStart) {
		t.Fatalf("hourly period_start: want %v, got %v", lastHourStart, hourly[0].PeriodStart)
	}

	daily, err := s.Checks().GetRollups(ctx, monitorID, "daily")
	if err != nil {
		t.Fatal(err)
	}
	if len(daily) != 1 {
		t.Fatalf("expected 1 daily rollup, got %d", len(daily))
	}
	d := daily[0]
	if !d.PeriodStart.Equal(dayStart) {
		t.Fatalf("daily period_start: want %v, got %v", dayStart, d.PeriodStart)
	}
	if d.CheckCount != 2 {
		t.Fatalf("daily check_count: want 2, got %d", d.CheckCount)
	}
	if d.FailureCount != 1 || d.UptimePct != 50.0 {
		t.Fatalf("daily uptime: want 50%% / 1 failure, got %+v", d)
	}
}
