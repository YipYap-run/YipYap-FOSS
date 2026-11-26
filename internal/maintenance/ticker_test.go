package maintenance

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/events"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// capturedEmit records a single emit from the fake emitter.
type capturedEmit struct {
	Type  string
	OrgID string
	Data  interface{}
}

// fakeEmitter satisfies events.Emitter but only cares about maintenance
// methods; all other methods are no-ops. It stores each captured call in a
// mutex-guarded slice for later assertion.
type fakeEmitter struct {
	mu       sync.Mutex
	captured []capturedEmit
}

func (c *fakeEmitter) EmitAlertFired(_ context.Context, _ string, _ events.AlertFired) error {
	return nil
}
func (c *fakeEmitter) EmitAlertAcknowledged(_ context.Context, _ string, _ events.AlertAcknowledged) error {
	return nil
}
func (c *fakeEmitter) EmitAlertResolved(_ context.Context, _ string, _ events.AlertResolved) error {
	return nil
}
func (c *fakeEmitter) EmitAlertEscalated(_ context.Context, _ string, _ events.AlertEscalated) error {
	return nil
}
func (c *fakeEmitter) EmitMonitorUp(_ context.Context, _, _ string, _ events.MonitorUp) error {
	return nil
}
func (c *fakeEmitter) EmitMonitorDown(_ context.Context, _, _ string, _ events.MonitorDown) error {
	return nil
}
func (c *fakeEmitter) EmitMonitorDegraded(_ context.Context, _, _ string, _ events.MonitorDegraded) error {
	return nil
}
func (c *fakeEmitter) EmitMonitorHeartbeatMissed(_ context.Context, _, _ string, _ events.MonitorHeartbeatMissed) error {
	return nil
}
func (c *fakeEmitter) EmitMaintenanceStarted(_ context.Context, orgID string, d events.MaintenanceStarted) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.captured = append(c.captured, capturedEmit{Type: "maintenance.started", OrgID: orgID, Data: d})
	return nil
}
func (c *fakeEmitter) EmitMaintenanceEnded(_ context.Context, orgID string, d events.MaintenanceEnded) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.captured = append(c.captured, capturedEmit{Type: "maintenance.ended", OrgID: orgID, Data: d})
	return nil
}
func (c *fakeEmitter) snapshot() []capturedEmit {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]capturedEmit, len(c.captured))
	copy(out, c.captured)
	return out
}
func (c *fakeEmitter) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.captured = nil
}

// newTestStore builds an in-memory sqlite store with migrations applied and a
// single seed org.
func newTestStore(t *testing.T) (*sqlite.SQLiteStore, *domain.Org) {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	now := time.Now().UTC().Truncate(time.Second)
	org := &domain.Org{
		ID: uuid.New().String(), Name: "Org", Slug: "o-" + uuid.New().String()[:8],
		Plan: domain.PlanFree, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Orgs().Create(context.Background(), org); err != nil {
		t.Fatal(err)
	}
	return s, org
}

func TestTicker_EmitsStartedOnSchedule(t *testing.T) {
	s, org := newTestStore(t)
	em := &fakeEmitter{}
	tk := NewTicker(s, em, time.Second)

	now := time.Now().UTC().Truncate(time.Second)
	mw := &domain.MaintenanceWindow{
		ID: uuid.New().String(), OrgID: org.ID, Name: "soon",
		StartAt: now.Add(1 * time.Minute), EndAt: now.Add(1 * time.Hour),
		RecurrenceType: "none", DaysOfWeek: "[]", CreatedAt: now,
	}
	if err := s.MaintenanceWindows().Create(context.Background(), mw); err != nil {
		t.Fatal(err)
	}

	tk.prime(context.Background(), now)
	if got := em.snapshot(); len(got) != 0 {
		t.Fatalf("prime should not emit, got %+v", got)
	}

	tk.poll(context.Background(), now.Add(2*time.Minute))
	got := em.snapshot()
	if len(got) != 1 || got[0].Type != "maintenance.started" {
		t.Fatalf("expected 1 started on schedule, got %+v", got)
	}

	em.reset()
	tk.poll(context.Background(), now.Add(3*time.Minute))
	if got := em.snapshot(); len(got) != 0 {
		t.Fatalf("expected 0 emits on steady-state tick, got %+v", got)
	}
}

func TestTicker_EmitsEndedOnSchedule(t *testing.T) {
	s, org := newTestStore(t)
	em := &fakeEmitter{}
	tk := NewTicker(s, em, time.Second)

	now := time.Now().UTC().Truncate(time.Second)
	mw := &domain.MaintenanceWindow{
		ID: uuid.New().String(), OrgID: org.ID, Name: "one-shot",
		StartAt: now.Add(-10 * time.Minute), EndAt: now.Add(5 * time.Minute),
		RecurrenceType: "none", DaysOfWeek: "[]", CreatedAt: now,
	}
	if err := s.MaintenanceWindows().Create(context.Background(), mw); err != nil {
		t.Fatal(err)
	}

	tk.prime(context.Background(), now)
	if got := em.snapshot(); len(got) != 0 {
		t.Fatalf("prime should not emit, got %+v", got)
	}

	// Advance past EndAt.
	tk.poll(context.Background(), now.Add(10*time.Minute))
	got := em.snapshot()
	if len(got) != 1 || got[0].Type != "maintenance.ended" {
		t.Fatalf("expected 1 ended, got %+v", got)
	}
	d := got[0].Data.(events.MaintenanceEnded)
	if d.WindowID != mw.ID {
		t.Errorf("window_id: %q want %q", d.WindowID, mw.ID)
	}
	if d.EndedEarly {
		t.Error("scheduled end should not be ended_early")
	}
}

func TestTicker_EmitsEndedEarly_WhenDeleted(t *testing.T) {
	s, org := newTestStore(t)
	em := &fakeEmitter{}
	tk := NewTicker(s, em, time.Second)

	now := time.Now().UTC().Truncate(time.Second)
	mw := &domain.MaintenanceWindow{
		ID: uuid.New().String(), OrgID: org.ID, Name: "delete-me",
		StartAt: now.Add(-10 * time.Minute), EndAt: now.Add(1 * time.Hour),
		RecurrenceType: "none", DaysOfWeek: "[]", CreatedAt: now,
	}
	if err := s.MaintenanceWindows().Create(context.Background(), mw); err != nil {
		t.Fatal(err)
	}

	tk.prime(context.Background(), now)
	em.reset()

	// Delete row out from under the ticker (simulate a direct-DB delete or a
	// deletion that happened without our handler having an emitter wired).
	if err := s.MaintenanceWindows().Delete(context.Background(), mw.ID); err != nil {
		t.Fatal(err)
	}

	tk.poll(context.Background(), now.Add(1*time.Second))
	got := em.snapshot()
	if len(got) != 1 || got[0].Type != "maintenance.ended" {
		t.Fatalf("expected 1 ended, got %+v", got)
	}
	d := got[0].Data.(events.MaintenanceEnded)
	if !d.EndedEarly {
		t.Error("deletion of active window should emit ended_early=true")
	}
}

func TestTicker_Recurring(t *testing.T) {
	s, org := newTestStore(t)
	em := &fakeEmitter{}
	tk := NewTicker(s, em, time.Second)

	// Daily window, active from 09:00 to 10:00 UTC.
	// Use a synthetic "now" that we control so the test isn't wall-clock
	// dependent.
	base := time.Date(2026, 4, 23, 9, 0, 0, 0, time.UTC)

	mw := &domain.MaintenanceWindow{
		ID: uuid.New().String(), OrgID: org.ID, Name: "daily",
		StartAt: base, EndAt: base.Add(1 * time.Hour),
		RecurrenceType: "daily", DaysOfWeek: "[]", CreatedAt: base,
	}
	if err := s.MaintenanceWindows().Create(context.Background(), mw); err != nil {
		t.Fatal(err)
	}

	tk.prime(context.Background(), base.Add(-1*time.Minute))
	if got := em.snapshot(); len(got) != 0 {
		t.Fatalf("prime should not emit, got %+v", got)
	}

	// During today's active hour: started.
	tk.poll(context.Background(), base.Add(5*time.Minute))
	got := em.snapshot()
	if len(got) != 1 || got[0].Type != "maintenance.started" {
		t.Fatalf("expected started on day-1 active, got %+v", got)
	}
	em.reset()

	// After today's active hour: ended.
	tk.poll(context.Background(), base.Add(2*time.Hour))
	got = em.snapshot()
	if len(got) != 1 || got[0].Type != "maintenance.ended" {
		t.Fatalf("expected ended after day-1 window, got %+v", got)
	}
	em.reset()

	// Next day's active hour: started again.
	nextDay := base.Add(24 * time.Hour)
	tk.poll(context.Background(), nextDay.Add(5*time.Minute))
	got = em.snapshot()
	if len(got) != 1 || got[0].Type != "maintenance.started" {
		t.Fatalf("expected started on day-2 active, got %+v", got)
	}
}

func TestTicker_StartedPayloadShape(t *testing.T) {
	s, org := newTestStore(t)
	em := &fakeEmitter{}
	tk := NewTicker(s, em, time.Second)

	now := time.Now().UTC().Truncate(time.Second)
	monID := uuid.New().String()

	mw := &domain.MaintenanceWindow{
		ID: uuid.New().String(), OrgID: org.ID, MonitorID: monID, Name: "per-monitor",
		StartAt: now.Add(-1 * time.Minute), EndAt: now.Add(1 * time.Hour),
		RecurrenceType: "none", DaysOfWeek: "[]", CreatedAt: now,
	}
	if err := s.MaintenanceWindows().Create(context.Background(), mw); err != nil {
		t.Fatal(err)
	}

	tk.prime(context.Background(), now.Add(-2*time.Minute))
	tk.poll(context.Background(), now)
	got := em.snapshot()
	if len(got) != 1 {
		t.Fatalf("expected 1 emit, got %+v", got)
	}
	d := got[0].Data.(events.MaintenanceStarted)
	if len(d.Monitors) != 1 || d.Monitors[0] != monID {
		t.Errorf("monitors: got %v want [%s]", d.Monitors, monID)
	}
	if d.WindowID != mw.ID {
		t.Errorf("window_id: %q want %q", d.WindowID, mw.ID)
	}
	if d.ScheduledEnd.IsZero() {
		t.Error("scheduled_end should be populated for one-shot window")
	}

	// Ensure the payload marshals cleanly (sanity check for JSON shape).
	if _, err := json.Marshal(d); err != nil {
		t.Fatalf("marshal: %v", err)
	}
}

func TestTicker_RestartDoesNotDuplicateStarted(t *testing.T) {
	s, org := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	mw := &domain.MaintenanceWindow{
		ID: uuid.New().String(), OrgID: org.ID, Name: "ongoing",
		StartAt: now.Add(-10 * time.Minute), EndAt: now.Add(1 * time.Hour),
		RecurrenceType: "none", DaysOfWeek: "[]", CreatedAt: now,
	}
	if err := s.MaintenanceWindows().Create(context.Background(), mw); err != nil {
		t.Fatal(err)
	}

	first := &fakeEmitter{}
	NewTicker(s, first, time.Second).prime(context.Background(), now)
	if got := first.snapshot(); len(got) != 0 {
		t.Fatalf("first boot emitted %+v", got)
	}

	second := &fakeEmitter{}
	restarted := NewTicker(s, second, time.Second)
	restarted.prime(context.Background(), now.Add(time.Minute))
	restarted.poll(context.Background(), now.Add(2*time.Minute))
	if got := second.snapshot(); len(got) != 0 {
		t.Fatalf("restart emitted %+v", got)
	}

	restarted.poll(context.Background(), now.Add(2*time.Hour))
	got := second.snapshot()
	if len(got) != 1 || got[0].Type != "maintenance.ended" {
		t.Fatalf("expected 1 ended after restart, got %+v", got)
	}
}

// Two web replicas share one store: each transition is emitted once in total.
func TestTicker_ReplicasEmitOnce(t *testing.T) {
	s, org := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	mw := &domain.MaintenanceWindow{
		ID: uuid.New().String(), OrgID: org.ID, Name: "shared",
		StartAt: now.Add(time.Minute), EndAt: now.Add(time.Hour),
		RecurrenceType: "none", DaysOfWeek: "[]", CreatedAt: now,
	}
	if err := s.MaintenanceWindows().Create(ctx, mw); err != nil {
		t.Fatal(err)
	}
	a, b := &fakeEmitter{}, &fakeEmitter{}
	ta, tb := NewTicker(s, a, time.Second), NewTicker(s, b, time.Second)
	ta.prime(ctx, now)
	tb.prime(ctx, now)

	count := func(typ string) int {
		n := 0
		for _, e := range append(a.snapshot(), b.snapshot()...) {
			if e.Type == typ {
				n++
			}
		}
		return n
	}

	ta.poll(ctx, now.Add(2*time.Minute))
	tb.poll(ctx, now.Add(2*time.Minute+3*time.Second))
	if n := count("maintenance.started"); n != 1 {
		t.Fatalf("started emitted %d times across replicas, want 1", n)
	}

	tb.poll(ctx, now.Add(2*time.Hour))
	ta.poll(ctx, now.Add(2*time.Hour+time.Second))
	if n := count("maintenance.ended"); n != 1 {
		t.Fatalf("ended emitted %d times across replicas, want 1", n)
	}

	// A brand new active window seen by both replicas, then deleted.
	live := &domain.MaintenanceWindow{
		ID: uuid.New().String(), OrgID: org.ID, Name: "live",
		StartAt: now.Add(-time.Minute), EndAt: now.Add(3 * time.Hour),
		RecurrenceType: "none", DaysOfWeek: "[]", CreatedAt: now,
	}
	if err := s.MaintenanceWindows().Create(ctx, live); err != nil {
		t.Fatal(err)
	}
	a.reset()
	b.reset()
	ta.poll(ctx, now.Add(2*time.Hour+time.Minute))
	tb.poll(ctx, now.Add(2*time.Hour+time.Minute))
	if n := count("maintenance.started"); n != 1 {
		t.Fatalf("new window started emitted %d times, want 1", n)
	}
	if err := s.MaintenanceWindows().Delete(ctx, live.ID); err != nil {
		t.Fatal(err)
	}
	tb.poll(ctx, now.Add(2*time.Hour+2*time.Minute))
	ta.poll(ctx, now.Add(2*time.Hour+2*time.Minute))
	if n := count("maintenance.ended"); n != 1 {
		t.Fatalf("deleted window ended emitted %d times, want 1", n)
	}
}
