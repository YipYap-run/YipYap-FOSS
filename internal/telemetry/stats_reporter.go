package telemetry

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// StartStatsReporter periodically queries platform stats and records them as
// OTEL gauge metrics. It returns immediately; the reporter runs in a goroutine
// until ctx is cancelled. Pass nil for stats to disable (e.g. FOSS builds).
func StartStatsReporter(ctx context.Context, stats store.StatsStore, m *Metrics, interval time.Duration) {
	if stats == nil {
		return
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()

		// Record once immediately, then on tick.
		recordStats(ctx, stats, m)

		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				recordStats(ctx, stats, m)
			}
		}
	}()
}

func recordStats(ctx context.Context, stats store.StatsStore, m *Metrics) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	s, err := stats.GetPlatformStats(ctx)
	if err != nil {
		slog.Warn("stats reporter: query failed", "error", err)
		return
	}

	m.TotalUsers.Record(ctx, s.TotalUsers)
	m.TotalMonitors.Record(ctx, s.TotalMonitors)

	for typ, count := range s.MonitorsByType {
		m.MonitorsByType.Record(ctx, count,
			metric.WithAttributeSet(attribute.NewSet(attribute.String("type", typ))))
	}

	for plan, count := range s.CustomersByPlan {
		m.CustomersByPlan.Record(ctx, count,
			metric.WithAttributeSet(attribute.NewSet(attribute.String("plan", plan))))
	}
}
