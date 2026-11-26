// Package maintenance contains runtime machinery for maintenance-window
// lifecycle events. The Ticker polls the store at a fixed cadence and emits
// domain events when windows transition between active and inactive, including
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
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/events"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// DefaultInterval is the recommended polling cadence for the ticker. Thirty
// seconds keeps sub-minute transition delay while remaining cheap, a single
// SELECT across maintenance_windows per tick.
const DefaultInterval = 30 * time.Second

// Ticker polls maintenance windows, computes active-set diffs, and emits
// events on transitions. It handles scheduled starts, scheduled ends,
// and recurring-window edges (daily/weekly/monthly) without requiring a
// human API action.
//
// Every web replica runs a Ticker. The last emitted state of each window
// lives in the store (maintenance_lifecycle_state), and a replica emits only
// after it wins a conditional write for that transition, so each event fires
// once across replicas.
type Ticker struct {
	store    store.Store
	emitter  events.Emitter
	interval time.Duration
}

// NewTicker returns a Ticker wired to s and e. Pass interval <= 0 to use
// DefaultInterval.
func NewTicker(s store.Store, e events.Emitter, interval time.Duration) *Ticker {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Ticker{
		store:    s,
		emitter:  e,
		interval: interval,
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
	// events for every currently-active window the first time the shared
	// state is populated.
	t.prime(ctx, time.Now().UTC())

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			t.poll(ctx, now.UTC())
		}
	}
}

// prime starts tracking every untracked window at its current state without
// emitting. Windows another replica already tracks are left alone, so a
// transition that happened while this replica was down is still emitted by
// the next poll.
func (t *Ticker) prime(ctx context.Context, now time.Time) {
	mws := t.store.MaintenanceWindows()
	windows, err := mws.ListAll(ctx)
	if err != nil {
		slog.Error("maintenance ticker: list all failed", "error", err)
		return
	}
	for _, mw := range windows {
		if _, err := mws.SeedLifecycleState(ctx, stateOf(mw, now), now); err != nil {
			slog.Error("maintenance ticker: seed state failed", "window_id", mw.ID, "error", err)
		}
	}
}

func stateOf(mw *domain.MaintenanceWindow, now time.Time) store.MaintenanceLifecycleState {
	return store.MaintenanceLifecycleState{
		WindowID: mw.ID, OrgID: mw.OrgID, MonitorID: mw.MonitorID, Active: mw.IsActiveAt(now),
	}
}

// poll performs one pass: list every window, compute IsActiveAt(now), diff
// against the shared state, and emit events for transitions this
// replica claims.
func (t *Ticker) poll(ctx context.Context, now time.Time) {
	mws := t.store.MaintenanceWindows()
	// Read the tracked states before the windows: a window created between
	// the two reads then shows up as new, never as deleted.
	states, err := mws.ListLifecycleStates(ctx)
	if err != nil {
		slog.Error("maintenance ticker: list states failed", "error", err)
		return
	}
	windows, err := mws.ListAll(ctx)
	if err != nil {
		slog.Error("maintenance ticker: list all failed", "error", err)
		return
	}
	known := make(map[string]store.MaintenanceLifecycleState, len(states))
	for _, st := range states {
		known[st.WindowID] = st
	}

	seen := make(map[string]struct{}, len(windows))
	for _, mw := range windows {
		seen[mw.ID] = struct{}{}
		isActive := mw.IsActiveAt(now)
		prev, ok := known[mw.ID]

		if !ok {
			// First time any replica observes this window. Emit only if it
			// is active right now; future-dated windows stay silent until
			// StartAt is crossed.
			won, err := mws.SeedLifecycleState(ctx, stateOf(mw, now), now)
			if err != nil {
				slog.Error("maintenance ticker: seed state failed", "window_id", mw.ID, "error", err)
				continue
			}
			if won && isActive {
				t.emitStarted(ctx, mw, now)
			}
			continue
		}
		if prev.Active == isActive {
			continue
		}
		won, err := mws.ClaimLifecycleTransition(ctx, mw.ID, prev.Active, now)
		if err != nil {
			slog.Error("maintenance ticker: claim transition failed", "window_id", mw.ID, "error", err)
			continue
		}
		if !won {
			continue // another replica emitted this transition
		}
		if isActive {
			t.emitStarted(ctx, mw, now)
		} else {
			t.emitEnded(ctx, mw.OrgID, mw.ID, now, false)
		}
	}

	// Anything tracked that no longer exists in the store was deleted
	// out-of-band. If it was active, emit an ended-early event so downstream
	// subscribers aren't left thinking it's still ongoing.
	for id, prev := range known {
		if _, ok := seen[id]; ok {
			continue
		}
		won, err := mws.ClaimLifecycleDeletion(ctx, id)
		if err != nil {
			slog.Error("maintenance ticker: claim deletion failed", "window_id", id, "error", err)
			continue
		}
		if won && prev.Active {
			t.emitEnded(ctx, prev.OrgID, id, now, true)
		}
	}
}

// emitStarted publishes a maintenance.started.v1 event for the given
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
	if err := events.EmitMaintenanceStarted(ctx, t.emitter, mw.OrgID, events.MaintenanceStarted{
		WindowID:     mw.ID,
		Monitors:     monitors,
		StartedAt:    now,
		ScheduledEnd: scheduledEnd,
	}); err != nil {
		slog.Error("maintenance ticker: emit started failed", "window_id", mw.ID, "error", err)
	}
}

// emitEnded publishes a maintenance.ended.v1 event. endedEarly is true
// when the window was cut short (deleted while active); false when it hit
// its scheduled EndAt naturally.
func (t *Ticker) emitEnded(ctx context.Context, orgID, windowID string, now time.Time, endedEarly bool) {
	if err := events.EmitMaintenanceEnded(ctx, t.emitter, orgID, events.MaintenanceEnded{
		WindowID:   windowID,
		EndedAt:    now,
		EndedEarly: endedEarly,
	}); err != nil {
		slog.Error("maintenance ticker: emit ended failed", "window_id", windowID, "error", err)
	}
}
