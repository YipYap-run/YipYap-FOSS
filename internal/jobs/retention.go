package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// StartRetentionPruner runs a background goroutine that periodically prunes
// check data older than 30 days (FOSS fixed retention).
func StartRetentionPruner(ctx context.Context, s store.Store) {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pruneAllFOSS(ctx, s)
			}
		}
	}()
	slog.Info("retention pruner started (foss)", "interval", "1h", "retention_days", 30)
}

func pruneAllFOSS(ctx context.Context, s store.Store) {
	monitors, err := s.Monitors().ListAllEnabled(ctx)
	if err != nil {
		return
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -30)
	for _, m := range monitors {
		_, _ = s.Checks().PruneBefore(ctx, m.ID, cutoff)
	}
}
