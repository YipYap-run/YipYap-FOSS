package jobs

import (
	"context"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// rollupConcurrency bounds how many monitors are aggregated in parallel per run.
const rollupConcurrency = 8

// StartRollupAggregator runs a background goroutine that, once an hour,
// aggregates the most-recently-completed clock hour of monitor checks into
// hourly rollups (and, at the top of a UTC day, the previous day into a daily
// rollup). It is forward-only: each tick only writes the periods that just
// completed, never backfills.
func StartRollupAggregator(ctx context.Context, s store.Store) {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := runRollupsAsOf(ctx, s, time.Now().UTC()); err != nil {
					slog.Error("rollup aggregator: run failed", "error", err)
				}
			}
		}
	}()
	slog.Info("rollup aggregator started", "interval", "1h")
}

// runRollupsAsOf aggregates the period(s) that completed just before asOf.
// asOf is passed explicitly (not read from the clock) so the work is
// deterministic and testable. It does not call time.Now.
func runRollupsAsOf(ctx context.Context, s store.Store, asOf time.Time) error {
	asOf = asOf.UTC()
	monitors, err := s.Monitors().ListAllEnabled(ctx)
	if err != nil {
		return err
	}

	// The just-completed clock hour.
	hourStart := asOf.Truncate(time.Hour).Add(-time.Hour)
	hourEnd := hourStart.Add(time.Hour)

	// If asOf falls in the first hour of a UTC day, the hour we just closed
	// was the last hour of the previous day, so that day is now complete too.
	var dayStart, dayEnd time.Time
	doDaily := asOf.Hour() == 0
	if doDaily {
		dayStart = hourStart.Truncate(24 * time.Hour)
		dayEnd = dayStart.Add(24 * time.Hour)
	}

	sem := make(chan struct{}, rollupConcurrency)
	var wg sync.WaitGroup
	for _, m := range monitors {
		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		default:
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(monitorID string) {
			defer wg.Done()
			defer func() { <-sem }()

			aggregateWindow(ctx, s, monitorID, "hourly", hourStart, hourEnd)
			if doDaily {
				aggregateWindow(ctx, s, monitorID, "daily", dayStart, dayEnd)
			}
		}(m.ID)
	}
	wg.Wait()
	return nil
}

// aggregateWindow computes and upserts a single rollup for one monitor and
// window. Errors are logged but not propagated, so one monitor's failure does
// not abort the whole run.
func aggregateWindow(ctx context.Context, s store.Store, monitorID, period string, start, end time.Time) {
	checks, err := s.Checks().ListByMonitor(ctx, monitorID, store.CheckFilter{
		ListParams: store.ListParams{Limit: 100000},
		Since:      &start,
		Until:      &end,
	})
	if err != nil {
		slog.Error("rollup aggregator: list checks failed", "monitor", monitorID, "period", period, "error", err)
		return
	}
	r := computeRollup(monitorID, period, start, checks)
	if r == nil {
		return
	}
	if err := s.Checks().UpsertRollup(ctx, r); err != nil {
		slog.Error("rollup aggregator: upsert failed", "monitor", monitorID, "period", period, "error", err)
		return
	}
	slog.Debug("rollup aggregator: wrote rollup", "monitor", monitorID, "period", period,
		"period_start", start, "check_count", r.CheckCount, "failure_count", r.FailureCount)
}

// computeRollup reduces a window of checks into a single rollup. It returns nil
// when there are no checks, so an empty window produces no rollup row.
//
// Uptime/failure semantics match the API (internal/api/handlers/monitors.go):
// a check counts as "up" only when Status == StatusUp. Both "down" and
// "degraded" are failures.
func computeRollup(monitorID, period string, periodStart time.Time, checks []*domain.MonitorCheck) *domain.MonitorRollup {
	n := len(checks)
	if n == 0 {
		return nil
	}

	upCount := 0
	var latencySum float64
	latencies := make([]int, 0, n)
	for _, c := range checks {
		if c.Status == domain.StatusUp {
			upCount++
		}
		latencySum += float64(c.LatencyMS)
		latencies = append(latencies, c.LatencyMS)
	}
	failureCount := n - upCount

	sort.Ints(latencies)

	return &domain.MonitorRollup{
		MonitorID:    monitorID,
		Period:       period,
		PeriodStart:  periodStart,
		UptimePct:    float64(upCount) / float64(n) * 100,
		AvgLatencyMS: latencySum / float64(n),
		P95LatencyMS: percentile(latencies, 0.95),
		P99LatencyMS: percentile(latencies, 0.99),
		CheckCount:   n,
		FailureCount: failureCount,
	}
}

// percentile returns the nearest-rank percentile of an already-sorted slice.
// sorted must be non-empty. p is a fraction in (0, 1].
func percentile(sorted []int, p float64) float64 {
	rank := int(math.Ceil(p*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return float64(sorted[rank])
}
