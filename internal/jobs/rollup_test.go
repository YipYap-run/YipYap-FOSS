package jobs

import (
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func check(status domain.CheckStatus, latency int) *domain.MonitorCheck {
	return &domain.MonitorCheck{Status: status, LatencyMS: latency}
}

func TestComputeRollupEmpty(t *testing.T) {
	got := computeRollup("m1", "hourly", time.Unix(0, 0).UTC(), nil)
	if got != nil {
		t.Fatalf("expected nil for zero checks, got %+v", got)
	}
}

func TestComputeRollupAllUp(t *testing.T) {
	checks := []*domain.MonitorCheck{
		check(domain.StatusUp, 10),
		check(domain.StatusUp, 20),
		check(domain.StatusUp, 30),
		check(domain.StatusUp, 40),
	}
	start := time.Date(2026, 5, 24, 1, 0, 0, 0, time.UTC)
	r := computeRollup("m1", "hourly", start, checks)
	if r == nil {
		t.Fatal("expected non-nil rollup")
	}
	if r.MonitorID != "m1" || r.Period != "hourly" || !r.PeriodStart.Equal(start) {
		t.Fatalf("unexpected identity fields: %+v", r)
	}
	if r.CheckCount != 4 {
		t.Fatalf("check_count: want 4, got %d", r.CheckCount)
	}
	if r.FailureCount != 0 {
		t.Fatalf("failure_count: want 0, got %d", r.FailureCount)
	}
	if r.UptimePct != 100.0 {
		t.Fatalf("uptime_pct: want 100, got %v", r.UptimePct)
	}
	if r.AvgLatencyMS != 25.0 {
		t.Fatalf("avg_latency_ms: want 25, got %v", r.AvgLatencyMS)
	}
}

func TestComputeRollupSomeDownAndDegraded(t *testing.T) {
	// degraded counts as a failure for uptime (matches monitors.go: only
	// StatusUp is "up"). 2 up out of 4 = 50%.
	checks := []*domain.MonitorCheck{
		check(domain.StatusUp, 100),
		check(domain.StatusDown, 0),
		check(domain.StatusDegraded, 200),
		check(domain.StatusUp, 300),
	}
	start := time.Date(2026, 5, 24, 1, 0, 0, 0, time.UTC)
	r := computeRollup("m1", "hourly", start, checks)
	if r.CheckCount != 4 {
		t.Fatalf("check_count: want 4, got %d", r.CheckCount)
	}
	if r.FailureCount != 2 {
		t.Fatalf("failure_count: want 2 (down+degraded), got %d", r.FailureCount)
	}
	if r.UptimePct != 50.0 {
		t.Fatalf("uptime_pct: want 50, got %v", r.UptimePct)
	}
	// avg over all checks: (100+0+200+300)/4 = 150
	if r.AvgLatencyMS != 150.0 {
		t.Fatalf("avg_latency_ms: want 150, got %v", r.AvgLatencyMS)
	}
}

func TestComputeRollupPercentiles(t *testing.T) {
	// 100 checks, all up, latency 1..100ms (inserted out of order to prove
	// the implementation sorts).
	checks := make([]*domain.MonitorCheck, 0, 100)
	for i := 100; i >= 1; i-- {
		checks = append(checks, check(domain.StatusUp, i))
	}
	start := time.Date(2026, 5, 24, 1, 0, 0, 0, time.UTC)
	r := computeRollup("m1", "hourly", start, checks)

	if r.CheckCount != 100 || r.FailureCount != 0 {
		t.Fatalf("count/failure: %+v", r)
	}
	// avg of 1..100 = 50.5
	if r.AvgLatencyMS != 50.5 {
		t.Fatalf("avg_latency_ms: want 50.5, got %v", r.AvgLatencyMS)
	}
	// nearest-rank: p95 index = ceil(0.95*100)-1 = 94 -> value 95.
	if r.P95LatencyMS != 95.0 {
		t.Fatalf("p95_latency_ms: want 95, got %v", r.P95LatencyMS)
	}
	// p99 index = ceil(0.99*100)-1 = 98 -> value 99.
	if r.P99LatencyMS != 99.0 {
		t.Fatalf("p99_latency_ms: want 99, got %v", r.P99LatencyMS)
	}
}

func TestComputeRollupSingleCheckPercentile(t *testing.T) {
	// One check: every percentile is that single value.
	checks := []*domain.MonitorCheck{check(domain.StatusUp, 77)}
	r := computeRollup("m1", "hourly", time.Now().UTC(), checks)
	if r.P95LatencyMS != 77.0 || r.P99LatencyMS != 77.0 || r.AvgLatencyMS != 77.0 {
		t.Fatalf("single-check percentiles wrong: %+v", r)
	}
}
