package maintenance

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// capturedEmit records a single emit from the fake emitter.
type capturedEmit struct {
	Type  string
	OrgID string
	Data  interface{}
}

// fakeEmitter satisfies cloudevents.Emitter but only cares about maintenance
// methods; all other methods are no-ops. It stores each captured call in a
// mutex-guarded slice for later assertion.
type fakeEmitter struct {
	mu       sync.Mutex
	captured []capturedEmit
}

func (c *fakeEmitter) EmitAlertFired(_ context.Context, _ string, _ types.AlertFiredV1Data) error {
	return nil
}
func (c *fakeEmitter) EmitAlertAcknowledged(_ context.Context, _ string, _ types.AlertAcknowledgedV1Data) error {
	return nil
}
func (c *fakeEmitter) EmitAlertResolved(_ context.Context, _ string, _ types.AlertResolvedV1Data) error {
	return nil
}
func (c *fakeEmitter) EmitAlertEscalated(_ context.Context, _ string, _ types.AlertEscalatedV1Data) error {
	return nil
}
func (c *fakeEmitter) EmitMonitorUp(_ context.Context, _, _ string, _ types.MonitorUpV1Data) error {
	return nil
}
func (c *fakeEmitter) EmitMonitorDown(_ context.Context, _, _ string, _ types.MonitorDownV1Data) error {
	return nil
}
func (c *fakeEmitter) EmitMonitorDegraded(_ context.Context, _, _ string, _ types.MonitorDegradedV1Data) error {
	return nil
}
func (c *fakeEmitter) EmitMonitorHeartbeatMissed(_ context.Context, _, _ string, _ types.MonitorHeartbeatMissedV1Data) error {
	return nil
}
func (c *fakeEmitter) EmitMaintenanceStarted(_ context.Context, orgID string, d types.MaintenanceStartedV1Data) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.captured = append(c.captured, capturedEmit{Type: types.TypeMaintenanceStartedV1, OrgID: orgID, Data: d})
	return nil
}
func (c *fakeEmitter) EmitMaintenanceEnded(_ context.Context, orgID string, d types.MaintenanceEndedV1Data) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.captured = append(c.captured, capturedEmit{Type: types.TypeMaintenanceEndedV1, OrgID: orgID, Data: d})
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
		ID: uuid.New().String(), OrgID: org.ID, Name: "now",
		StartAt: now.Add(-1 * time.Minute), EndAt: now.Add(1 * time.Hour),
		RecurrenceType: "none", DaysOfWeek: "[]", CreatedAt: now,
	}
	if err := s.MaintenanceWindows().Create(context.Background(), mw); err != nil {
		t.Fatal(err)
	}

	// First tick: window is active, should emit started.
	tk.poll(context.Background(), now)
	if got := em.snapshot(); len(got) != 1 || got[0].Type != types.TypeMaintenanceStartedV1 {
		t.Fatalf("expected 1 started emit, got %+v", got)
	}

	// Steady-state tick: nothing new.
	em.reset()
	tk.poll(context.Background(), now.Add(1*time.Second))
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

	// Currently active.
	tk.poll(context.Background(), now)
	if got := em.snapshot(); len(got) != 1 || got[0].Type != types.TypeMaintenanceStartedV1 {
		t.Fatalf("expected 1 started, got %+v", got)
	}
	em.reset()

	// Advance past EndAt.
	tk.poll(context.Background(), now.Add(10*time.Minute))
	got := em.snapshot()
	if len(got) != 1 || got[0].Type != types.TypeMaintenanceEndedV1 {
		t.Fatalf("expected 1 ended, got %+v", got)
	}
	d := got[0].Data.(types.MaintenanceEndedV1Data)
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

	tk.poll(context.Background(), now)
	em.reset()

	// Delete row out from under the ticker (simulate a direct-DB delete or a
	// deletion that happened without our handler having an emitter wired).
	if err := s.MaintenanceWindows().Delete(context.Background(), mw.ID); err != nil {
		t.Fatal(err)
	}

	tk.poll(context.Background(), now.Add(1*time.Second))
	got := em.snapshot()
	if len(got) != 1 || got[0].Type != types.TypeMaintenanceEndedV1 {
		t.Fatalf("expected 1 ended, got %+v", got)
	}
	d := got[0].Data.(types.MaintenanceEndedV1Data)
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

	// During today's active hour: started.
	tk.poll(context.Background(), base.Add(5*time.Minute))
	got := em.snapshot()
	if len(got) != 1 || got[0].Type != types.TypeMaintenanceStartedV1 {
		t.Fatalf("expected started on day-1 active, got %+v", got)
	}
	em.reset()

	// After today's active hour: ended.
	tk.poll(context.Background(), base.Add(2*time.Hour))
	got = em.snapshot()
	if len(got) != 1 || got[0].Type != types.TypeMaintenanceEndedV1 {
		t.Fatalf("expected ended after day-1 window, got %+v", got)
	}
	em.reset()

	// Next day's active hour: started again.
	nextDay := base.Add(24 * time.Hour)
	tk.poll(context.Background(), nextDay.Add(5*time.Minute))
	got = em.snapshot()
	if len(got) != 1 || got[0].Type != types.TypeMaintenanceStartedV1 {
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

	tk.poll(context.Background(), now)
	got := em.snapshot()
	if len(got) != 1 {
		t.Fatalf("expected 1 emit, got %+v", got)
	}
	d := got[0].Data.(types.MaintenanceStartedV1Data)
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
