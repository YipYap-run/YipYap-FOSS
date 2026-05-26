package handlers_test

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

type capturedEmit struct {
	Type  string
	OrgID string
	Data  interface{}
}

type captureEmitter struct {
	mu       sync.Mutex
	captured []capturedEmit
}

func (c *captureEmitter) EmitAlertFired(_ context.Context, orgID string, d types.AlertFiredV1Data) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.captured = append(c.captured, capturedEmit{Type: types.TypeAlertFiredV1, OrgID: orgID, Data: d})
	return nil
}
func (c *captureEmitter) EmitAlertAcknowledged(_ context.Context, orgID string, d types.AlertAcknowledgedV1Data) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.captured = append(c.captured, capturedEmit{Type: types.TypeAlertAcknowledgedV1, OrgID: orgID, Data: d})
	return nil
}
func (c *captureEmitter) EmitAlertResolved(_ context.Context, orgID string, d types.AlertResolvedV1Data) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.captured = append(c.captured, capturedEmit{Type: types.TypeAlertResolvedV1, OrgID: orgID, Data: d})
	return nil
}
func (c *captureEmitter) EmitAlertEscalated(_ context.Context, orgID string, d types.AlertEscalatedV1Data) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.captured = append(c.captured, capturedEmit{Type: types.TypeAlertEscalatedV1, OrgID: orgID, Data: d})
	return nil
}
func (c *captureEmitter) EmitMonitorUp(_ context.Context, orgID, _ string, d types.MonitorUpV1Data) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.captured = append(c.captured, capturedEmit{Type: types.TypeMonitorUpV1, OrgID: orgID, Data: d})
	return nil
}
func (c *captureEmitter) EmitMonitorDown(_ context.Context, orgID, _ string, d types.MonitorDownV1Data) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.captured = append(c.captured, capturedEmit{Type: types.TypeMonitorDownV1, OrgID: orgID, Data: d})
	return nil
}
func (c *captureEmitter) EmitMonitorDegraded(_ context.Context, orgID, _ string, d types.MonitorDegradedV1Data) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.captured = append(c.captured, capturedEmit{Type: types.TypeMonitorDegradedV1, OrgID: orgID, Data: d})
	return nil
}
func (c *captureEmitter) EmitMonitorHeartbeatMissed(_ context.Context, orgID, _ string, d types.MonitorHeartbeatMissedV1Data) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.captured = append(c.captured, capturedEmit{Type: types.TypeMonitorHeartbeatMissedV1, OrgID: orgID, Data: d})
	return nil
}
func (c *captureEmitter) EmitMaintenanceStarted(_ context.Context, orgID string, d types.MaintenanceStartedV1Data) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.captured = append(c.captured, capturedEmit{Type: types.TypeMaintenanceStartedV1, OrgID: orgID, Data: d})
	return nil
}
func (c *captureEmitter) EmitMaintenanceEnded(_ context.Context, orgID string, d types.MaintenanceEndedV1Data) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.captured = append(c.captured, capturedEmit{Type: types.TypeMaintenanceEndedV1, OrgID: orgID, Data: d})
	return nil
}

func (c *captureEmitter) snapshot() []capturedEmit {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]capturedEmit, len(c.captured))
	copy(out, c.captured)
	return out
}

// setupAlertHandlerTest creates a store with an org + monitor + firing alert
// and returns a request context whose claims belong to that org.
func setupAlertHandlerTest(t *testing.T) (*sqlite.SQLiteStore, *domain.Alert, *domain.User) {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	org := &domain.Org{ID: uuid.New().String(), Name: "Org", Slug: "o-" + uuid.New().String()[:8], Plan: domain.PlanFree, CreatedAt: now, UpdatedAt: now}
	if err := s.Orgs().Create(ctx, org); err != nil {
		t.Fatal(err)
	}

	user := &domain.User{ID: uuid.New().String(), OrgID: org.ID, Email: "u@x.com", PasswordHash: "h", Role: domain.RoleMember, CreatedAt: now, UpdatedAt: now}
	if err := s.Users().Create(ctx, user); err != nil {
		t.Fatal(err)
	}

	mon := &domain.Monitor{
		ID: uuid.New().String(), OrgID: org.ID, Name: "M", Type: domain.MonitorHTTP,
		Config: []byte(`{"url":"https://example.com"}`), IntervalSeconds: 60, TimeoutSeconds: 10,
		Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Monitors().Create(ctx, mon); err != nil {
		t.Fatal(err)
	}

	alert := &domain.Alert{
		ID: uuid.New().String(), MonitorID: mon.ID, OrgID: org.ID,
		Status: domain.AlertFiring, Severity: domain.SeverityCritical,
		StartedAt: now,
	}
	if err := s.Alerts().Create(ctx, alert); err != nil {
		t.Fatal(err)
	}

	return s, alert, user
}

func requestWithAlertID(t *testing.T, alertID, userID, orgID string) (*httptest.ResponseRecorder, *chi.Context, context.Context) {
	t.Helper()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", alertID)
	ctx := context.WithValue(context.Background(), chi.RouteCtxKey, rctx)
	ctx = middleware.SetClaims(ctx, &auth.Claims{UserID: userID, OrgID: orgID, Role: "member"})
	return httptest.NewRecorder(), rctx, ctx
}

func TestAlertHandler_Ack_EmitsAcknowledged(t *testing.T) {
	s, alert, user := setupAlertHandlerTest(t)
	em := &captureEmitter{}
	h := handlers.NewAlertHandlerWithEmitter(s, em)

	w, _, ctx := requestWithAlertID(t, alert.ID, user.ID, alert.OrgID)
	r := httptest.NewRequest("POST", "/alerts/"+alert.ID+"/ack", nil).WithContext(ctx)

	h.Ack(w, r)

	if w.Code != 200 {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}

	captured := em.snapshot()
	if len(captured) != 1 {
		t.Fatalf("expected 1 emit, got %d", len(captured))
	}
	got := captured[0]
	if got.Type != types.TypeAlertAcknowledgedV1 {
		t.Errorf("type: %q", got.Type)
	}
	if got.OrgID != alert.OrgID {
		t.Errorf("org: %q want %q", got.OrgID, alert.OrgID)
	}
	d, ok := got.Data.(types.AlertAcknowledgedV1Data)
	if !ok {
		t.Fatalf("bad data type %T", got.Data)
	}
	if d.AlertID != alert.ID {
		t.Errorf("alert_id: %q want %q", d.AlertID, alert.ID)
	}
	if d.MonitorID != alert.MonitorID {
		t.Errorf("monitor_id: %q want %q", d.MonitorID, alert.MonitorID)
	}
	if d.AckedBy != user.ID {
		t.Errorf("acked_by: %q want %q", d.AckedBy, user.ID)
	}
}

func TestAlertHandler_Resolve_EmitsResolved(t *testing.T) {
	s, alert, user := setupAlertHandlerTest(t)
	em := &captureEmitter{}
	h := handlers.NewAlertHandlerWithEmitter(s, em)

	w, _, ctx := requestWithAlertID(t, alert.ID, user.ID, alert.OrgID)
	r := httptest.NewRequest("POST", "/alerts/"+alert.ID+"/resolve", nil).WithContext(ctx)

	h.Resolve(w, r)

	if w.Code != 200 {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}

	captured := em.snapshot()
	if len(captured) != 1 {
		t.Fatalf("expected 1 emit, got %d", len(captured))
	}
	got := captured[0]
	if got.Type != types.TypeAlertResolvedV1 {
		t.Errorf("type: %q", got.Type)
	}
	d, ok := got.Data.(types.AlertResolvedV1Data)
	if !ok {
		t.Fatalf("bad data type %T", got.Data)
	}
	if d.AlertID != alert.ID {
		t.Errorf("alert_id: %q want %q", d.AlertID, alert.ID)
	}
	if d.ResolvedBy != user.ID {
		t.Errorf("resolved_by: %q want %q", d.ResolvedBy, user.ID)
	}
	if d.ResolvedAt.IsZero() {
		t.Error("resolved_at zero")
	}
}

func TestAlertHandler_NilEmitter_DoesNotPanic(t *testing.T) {
	s, alert, user := setupAlertHandlerTest(t)
	h := handlers.NewAlertHandler(s) // no emitter

	// Ack.
	{
		w, _, ctx := requestWithAlertID(t, alert.ID, user.ID, alert.OrgID)
		r := httptest.NewRequest("POST", "/alerts/"+alert.ID+"/ack", nil).WithContext(ctx)
		h.Ack(w, r)
		if w.Code != 200 {
			t.Fatalf("ack status %d body=%s", w.Code, w.Body.String())
		}
	}
	// Resolve.
	{
		w, _, ctx := requestWithAlertID(t, alert.ID, user.ID, alert.OrgID)
		r := httptest.NewRequest("POST", "/alerts/"+alert.ID+"/resolve", nil).WithContext(ctx)
		h.Resolve(w, r)
		if w.Code != 200 {
			t.Fatalf("resolve status %d body=%s", w.Code, w.Body.String())
		}
	}
}
