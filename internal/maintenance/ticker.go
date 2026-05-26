// Package maintenance contains runtime machinery for maintenance-window
// lifecycle events. The Ticker polls the store at a fixed cadence and emits
// CloudEvents when windows transition between active and inactive, including
// scheduled starts, scheduled ends, recurring-window edges, and deletions
// that happened out-of-band (for example, via tooling or an API path that
// wasn't wired with its own emitter).
//
// The synchronous API path in internal/api/handlers/maintenance.go emits
// lifecycle events for Create / Update / Delete; the Ticker is the
// belt-and-braces back-stop that catches everything else.
package maintenance

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// DefaultInterval is the recommended polling cadence for the ticker. Thirty
// seconds keeps sub-minute transition delay while remaining cheap, a single
// SELECT across maintenance_windows per tick.
const DefaultInterval = 30 * time.Second

// Ticker polls maintenance windows, computes active-set diffs, and emits
// CloudEvents on transitions. It handles scheduled starts, scheduled ends,
// and recurring-window edges (daily/weekly/monthly) without requiring a
// human API action.
type Ticker struct {
	store    store.Store
	emitter  cloudevents.Emitter
	interval time.Duration

	mu     sync.Mutex
	active map[string]windowState // window_id → last observed state
}

// windowState caches the fields we need when a window later vanishes (for
// example, because it was deleted out-of-band). We keep org_id and monitor
// info so we can emit a proper ended-early event even after the row is gone.
type windowState struct {
	active    bool
	orgID     string
	monitorID string
}

// NewTicker returns a Ticker wired to s and e. Pass interval <= 0 to use
// DefaultInterval.
func NewTicker(s store.Store, e cloudevents.Emitter, interval time.Duration) *Ticker {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Ticker{
		store:    s,
		emitter:  e,
		interval: interval,
		active:   make(map[string]windowState),
	}
}

// Run loops until ctx is done. Intended to be started in a goroutine from
// the long-lived web service. A nil emitter is tolerated, the ticker still
// tracks state transitions, it just does not publish anything (useful for
// FOSS builds where the emitter is not wired up).
func (t *Ticker) Run(ctx context.Context) {
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()

	// Prime the state on startup so we don't emit an avalanche of "started"
	// events for every currently-active window the moment we boot.
	t.poll(ctx, time.Now().UTC())

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			t.poll(ctx, now.UTC())
		}
	}
}

// poll performs one pass: list every window, compute IsActiveAt(now), diff
// against the remembered state, and emit CloudEvents for transitions.
func (t *Ticker) poll(ctx context.Context, now time.Time) {
	windows, err := t.store.MaintenanceWindows().ListAll(ctx)
	if err != nil {
		slog.Error("maintenance ticker: list all failed", "error", err)
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	seen := make(map[string]struct{}, len(windows))
	for _, mw := range windows {
		seen[mw.ID] = struct{}{}
		isActive := mw.IsActiveAt(now)
		prev, known := t.active[mw.ID]

		if !known {
			// First time we're observing this window. Record state but only
			// emit if it's becoming active right now (i.e. we just noticed
			// a transition from "did not exist" to "exists and is active").
			// Future-dated windows stay silent until StartAt is crossed.
			if isActive {
				t.emitStarted(ctx, mw, now)
			}
			t.active[mw.ID] = windowState{active: isActive, orgID: mw.OrgID, monitorID: mw.MonitorID}
			continue
		}

		switch {
		case !prev.active && isActive:
			t.emitStarted(ctx, mw, now)
		case prev.active && !isActive:
			t.emitEnded(ctx, mw.OrgID, mw.ID, now, false)
		}
		t.active[mw.ID] = windowState{active: isActive, orgID: mw.OrgID, monitorID: mw.MonitorID}
	}

	// Anything we remembered that no longer exists in the store was deleted
	// out-of-band. If it was active, emit an ended-early event so downstream
	// subscribers aren't left thinking it's still ongoing.
	for id, prev := range t.active {
		if _, ok := seen[id]; ok {
			continue
		}
		if prev.active {
			t.emitEnded(ctx, prev.orgID, id, now, true)
		}
		delete(t.active, id)
	}
}

// emitStarted publishes a maintenance.started.v1 CloudEvent for the given
// window using the Started-payload convention (empty Monitors means org-wide).
func (t *Ticker) emitStarted(ctx context.Context, mw *domain.MaintenanceWindow, now time.Time) {
	monitors := []string{}
	if mw.MonitorID != "" {
		monitors = []string{mw.MonitorID}
	}
	var scheduledEnd time.Time
	if mw.RecurrenceType == "" || mw.RecurrenceType == "none" {
		scheduledEnd = mw.EndAt
	}
	if err := cloudevents.EmitMaintenanceStarted(ctx, t.emitter, mw.OrgID, types.MaintenanceStartedV1Data{
		WindowID:     mw.ID,
		Monitors:     monitors,
		StartedAt:    now,
		ScheduledEnd: scheduledEnd,
	}); err != nil {
		slog.Error("maintenance ticker: emit started failed", "window_id", mw.ID, "error", err)
	}
}

// emitEnded publishes a maintenance.ended.v1 CloudEvent. endedEarly is true
// when the window was cut short (deleted while active); false when it hit
// its scheduled EndAt naturally.
func (t *Ticker) emitEnded(ctx context.Context, orgID, windowID string, now time.Time, endedEarly bool) {
	if err := cloudevents.EmitMaintenanceEnded(ctx, t.emitter, orgID, types.MaintenanceEndedV1Data{
		WindowID:   windowID,
		EndedAt:    now,
		EndedEarly: endedEarly,
	}); err != nil {
		slog.Error("maintenance ticker: emit ended failed", "window_id", windowID, "error", err)
	}
}
