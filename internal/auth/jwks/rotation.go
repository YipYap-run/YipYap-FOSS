package jwks

import (
	"context"
	"log/slog"
	"time"
)

// StartRotationLoop runs Rotate periodically until ctx is cancelled. The
// first Rotate runs immediately on start so that a freshly-booted process
// with a stale key won't wait up to `interval` before rotating.
//
// A reasonable interval is 24h: Rotate is idempotent within an
// active-key-max-age window, so 24h gives us headroom on missed ticks
// without spamming the database. The loop exits cleanly when ctx is
// cancelled.
//
// onRotate, if non-nil, is called after each Rotate attempt with the
// outcome (err may be nil on success). Consumers can record metrics from
// there.
func StartRotationLoop(ctx context.Context, mgr *Manager, interval time.Duration, onRotate func(err error)) {
	if interval <= 0 {
		interval = 24 * time.Hour
	}

	run := func() {
		err := mgr.Rotate(ctx)
		if err != nil {
			slog.Warn("jwks: rotation tick failed", "err", err.Error())
		}
		if onRotate != nil {
			onRotate(err)
		}
	}

	run()

	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
