package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
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

// setupMaintenanceTest creates an org + user and returns the store.
func setupMaintenanceTest(t *testing.T) (*sqlite.SQLiteStore, *domain.Org, *domain.User) {
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

	org := &domain.Org{
		ID:        uuid.New().String(),
		Name:      "Org",
		Slug:      "o-" + uuid.New().String()[:8],
		Plan:      domain.PlanFree,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.Orgs().Create(ctx, org); err != nil {
		t.Fatal(err)
	}

	user := &domain.User{
		ID:           uuid.New().String(),
		OrgID:        org.ID,
		Email:        "u@x.com",
		PasswordHash: "h",
		Role:         domain.RoleAdmin,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.Users().Create(ctx, user); err != nil {
		t.Fatal(err)
	}

	return s, org, user
}

func requestWithClaims(_ *testing.T, method, path, body string, user *domain.User) (*httptest.ResponseRecorder, *chi.Context, context.Context) {
	rctx := chi.NewRouteContext()
	ctx := context.WithValue(context.Background(), chi.RouteCtxKey, rctx)
	ctx = middleware.SetClaims(ctx, &auth.Claims{UserID: user.ID, OrgID: user.OrgID, Role: "admin"})
	r := httptest.NewRecorder()
	_ = method
	_ = path
	_ = body
	return r, rctx, ctx
}

func TestMaintenanceHandler_Create_EmitsStartedIfActiveNow(t *testing.T) {
	s, _, user := setupMaintenanceTest(t)
	em := &captureEmitter{}
	h := handlers.NewMaintenanceHandlerWithEmitter(s, em)

	now := time.Now().UTC().Truncate(time.Second)
	payload := map[string]any{
		"name":            "Live",
		"start_at":        now.Add(-5 * time.Minute).Format(time.RFC3339),
		"end_at":          now.Add(1 * time.Hour).Format(time.RFC3339),
		"suppress_alerts": true,
	}
	body, _ := json.Marshal(payload)

	w, _, ctx := requestWithClaims(t, "POST", "/maintenance", string(body), user)
	r := httptest.NewRequest("POST", "/maintenance", bytes.NewReader(body)).WithContext(ctx)

	h.Create(w, r)

	if w.Code != 201 {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}

	captured := em.snapshot()
	if len(captured) != 1 {
		t.Fatalf("expected 1 emit, got %d: %+v", len(captured), captured)
	}
	if captured[0].Type != types.TypeMaintenanceStartedV1 {
		t.Errorf("type: %q", captured[0].Type)
	}
	d, ok := captured[0].Data.(types.MaintenanceStartedV1Data)
	if !ok {
		t.Fatalf("bad data type %T", captured[0].Data)
	}
	if d.WindowID == "" {
		t.Error("window_id empty")
	}
	// Empty MonitorID → org-wide → Monitors should be empty slice.
	if len(d.Monitors) != 0 {
		t.Errorf("expected empty monitors (org-wide), got %v", d.Monitors)
	}
	if d.StartedAt.IsZero() {
		t.Error("started_at zero")
	}
	if d.ScheduledEnd.IsZero() {
		t.Error("scheduled_end zero (one-shot window should populate it)")
	}
}

func TestMaintenanceHandler_Create_DoesNotEmitIfNotActive(t *testing.T) {
	s, _, user := setupMaintenanceTest(t)
	em := &captureEmitter{}
	h := handlers.NewMaintenanceHandlerWithEmitter(s, em)

	now := time.Now().UTC().Truncate(time.Second)
	payload := map[string]any{
		"name":            "Scheduled future",
		"start_at":        now.Add(1 * time.Hour).Format(time.RFC3339),
		"end_at":          now.Add(2 * time.Hour).Format(time.RFC3339),
		"suppress_alerts": true,
	}
	body, _ := json.Marshal(payload)

	w, _, ctx := requestWithClaims(t, "POST", "/maintenance", string(body), user)
	r := httptest.NewRequest("POST", "/maintenance", bytes.NewReader(body)).WithContext(ctx)

	h.Create(w, r)

	if w.Code != 201 {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if n := len(em.snapshot()); n != 0 {
		t.Fatalf("expected 0 emits for future window, got %d", n)
	}
}

func TestMaintenanceHandler_Create_SingleMonitorIncludesMonitorID(t *testing.T) {
	s, org, user := setupMaintenanceTest(t)
	em := &captureEmitter{}
	h := handlers.NewMaintenanceHandlerWithEmitter(s, em)

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	mon := &domain.Monitor{
		ID: uuid.New().String(), OrgID: org.ID, Name: "M", Type: domain.MonitorHTTP,
		Config: []byte(`{"url":"https://example.com"}`), IntervalSeconds: 60, TimeoutSeconds: 10,
		Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Monitors().Create(ctx, mon); err != nil {
		t.Fatal(err)
	}

	payload := map[string]any{
		"name":            "Live per monitor",
		"monitor_id":      mon.ID,
		"start_at":        now.Add(-5 * time.Minute).Format(time.RFC3339),
		"end_at":          now.Add(1 * time.Hour).Format(time.RFC3339),
		"suppress_alerts": true,
	}
	body, _ := json.Marshal(payload)

	w, _, reqCtx := requestWithClaims(t, "POST", "/maintenance", string(body), user)
	r := httptest.NewRequest("POST", "/maintenance", bytes.NewReader(body)).WithContext(reqCtx)

	h.Create(w, r)

	if w.Code != 201 {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	captured := em.snapshot()
	if len(captured) != 1 {
		t.Fatalf("expected 1 emit, got %d", len(captured))
	}
	d := captured[0].Data.(types.MaintenanceStartedV1Data)
	if len(d.Monitors) != 1 || d.Monitors[0] != mon.ID {
		t.Errorf("expected monitors=[%s], got %v", mon.ID, d.Monitors)
	}
}

func TestMaintenanceHandler_Update_EmitsOnTransition(t *testing.T) {
	s, org, user := setupMaintenanceTest(t)
	em := &captureEmitter{}
	h := handlers.NewMaintenanceHandlerWithEmitter(s, em)

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	// Seed a window in the future (not active).
	mw := &domain.MaintenanceWindow{
		ID:             uuid.New().String(),
		OrgID:          org.ID,
		Name:           "Scheduled",
		StartAt:        now.Add(1 * time.Hour),
		EndAt:          now.Add(2 * time.Hour),
		SuppressAlerts: true,
		RecurrenceType: "none",
		DaysOfWeek:     "[]",
		CreatedAt:      now,
		CreatedBy:      user.ID,
	}
	if err := s.MaintenanceWindows().Create(ctx, mw); err != nil {
		t.Fatal(err)
	}

	// Shift StartAt into the past and EndAt still in future → becomes active.
	newStart := now.Add(-5 * time.Minute).Format(time.RFC3339)
	newEnd := now.Add(1 * time.Hour).Format(time.RFC3339)
	payload := map[string]any{
		"start_at": newStart,
		"end_at":   newEnd,
	}
	body, _ := json.Marshal(payload)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", mw.ID)
	reqCtx := context.WithValue(context.Background(), chi.RouteCtxKey, rctx)
	reqCtx = middleware.SetClaims(reqCtx, &auth.Claims{UserID: user.ID, OrgID: org.ID, Role: "admin"})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("PUT", "/maintenance/"+mw.ID, bytes.NewReader(body)).WithContext(reqCtx)

	h.Update(w, r)

	if w.Code != 200 {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	captured := em.snapshot()
	if len(captured) != 1 {
		t.Fatalf("expected 1 emit, got %d: %+v", len(captured), captured)
	}
	if captured[0].Type != types.TypeMaintenanceStartedV1 {
		t.Errorf("type: %q", captured[0].Type)
	}
}

func TestMaintenanceHandler_Update_NoEmitWhenStillActive(t *testing.T) {
	s, org, user := setupMaintenanceTest(t)
	em := &captureEmitter{}
	h := handlers.NewMaintenanceHandlerWithEmitter(s, em)

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	// Active window.
	mw := &domain.MaintenanceWindow{
		ID:             uuid.New().String(),
		OrgID:          org.ID,
		Name:           "Active",
		StartAt:        now.Add(-30 * time.Minute),
		EndAt:          now.Add(1 * time.Hour),
		SuppressAlerts: true,
		RecurrenceType: "none",
		DaysOfWeek:     "[]",
		CreatedAt:      now,
		CreatedBy:      user.ID,
	}
	if err := s.MaintenanceWindows().Create(ctx, mw); err != nil {
		t.Fatal(err)
	}

	// Just update the name, stays active.
	payload := map[string]any{"name": "Active-renamed"}
	body, _ := json.Marshal(payload)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", mw.ID)
	reqCtx := context.WithValue(context.Background(), chi.RouteCtxKey, rctx)
	reqCtx = middleware.SetClaims(reqCtx, &auth.Claims{UserID: user.ID, OrgID: org.ID, Role: "admin"})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("PUT", "/maintenance/"+mw.ID, bytes.NewReader(body)).WithContext(reqCtx)
	h.Update(w, r)

	if w.Code != 200 {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if n := len(em.snapshot()); n != 0 {
		t.Fatalf("expected 0 emits (no state change), got %d", n)
	}
}

func TestMaintenanceHandler_Delete_EmitsEndedIfActive(t *testing.T) {
	s, org, user := setupMaintenanceTest(t)
	em := &captureEmitter{}
	h := handlers.NewMaintenanceHandlerWithEmitter(s, em)

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	mw := &domain.MaintenanceWindow{
		ID:             uuid.New().String(),
		OrgID:          org.ID,
		Name:           "Active",
		StartAt:        now.Add(-30 * time.Minute),
		EndAt:          now.Add(1 * time.Hour),
		SuppressAlerts: true,
		RecurrenceType: "none",
		DaysOfWeek:     "[]",
		CreatedAt:      now,
		CreatedBy:      user.ID,
	}
	if err := s.MaintenanceWindows().Create(ctx, mw); err != nil {
		t.Fatal(err)
	}

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", mw.ID)
	reqCtx := context.WithValue(context.Background(), chi.RouteCtxKey, rctx)
	reqCtx = middleware.SetClaims(reqCtx, &auth.Claims{UserID: user.ID, OrgID: org.ID, Role: "admin"})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("DELETE", "/maintenance/"+mw.ID, nil).WithContext(reqCtx)
	h.Delete(w, r)

	if w.Code != 204 {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	captured := em.snapshot()
	if len(captured) != 1 {
		t.Fatalf("expected 1 emit, got %d", len(captured))
	}
	if captured[0].Type != types.TypeMaintenanceEndedV1 {
		t.Errorf("type: %q", captured[0].Type)
	}
	d := captured[0].Data.(types.MaintenanceEndedV1Data)
	if d.WindowID != mw.ID {
		t.Errorf("window_id: %q want %q", d.WindowID, mw.ID)
	}
	if !d.EndedEarly {
		t.Error("ended_early expected true")
	}
	if d.EndedAt.IsZero() {
		t.Error("ended_at zero")
	}
}

func TestMaintenanceHandler_Delete_NoEmitIfNotActive(t *testing.T) {
	s, org, user := setupMaintenanceTest(t)
	em := &captureEmitter{}
	h := handlers.NewMaintenanceHandlerWithEmitter(s, em)

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	mw := &domain.MaintenanceWindow{
		ID:             uuid.New().String(),
		OrgID:          org.ID,
		Name:           "Future",
		StartAt:        now.Add(1 * time.Hour),
		EndAt:          now.Add(2 * time.Hour),
		SuppressAlerts: true,
		RecurrenceType: "none",
		DaysOfWeek:     "[]",
		CreatedAt:      now,
		CreatedBy:      user.ID,
	}
	if err := s.MaintenanceWindows().Create(ctx, mw); err != nil {
		t.Fatal(err)
	}

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", mw.ID)
	reqCtx := context.WithValue(context.Background(), chi.RouteCtxKey, rctx)
	reqCtx = middleware.SetClaims(reqCtx, &auth.Claims{UserID: user.ID, OrgID: org.ID, Role: "admin"})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("DELETE", "/maintenance/"+mw.ID, nil).WithContext(reqCtx)
	h.Delete(w, r)

	if w.Code != 204 {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if n := len(em.snapshot()); n != 0 {
		t.Fatalf("expected 0 emits for non-active deletion, got %d", n)
	}
}

func TestMaintenanceHandler_NilEmitter_DoesNotPanic(t *testing.T) {
	s, _, user := setupMaintenanceTest(t)
	h := handlers.NewMaintenanceHandler(s) // no emitter

	now := time.Now().UTC().Truncate(time.Second)
	payload := map[string]any{
		"name":            "Live",
		"start_at":        now.Add(-5 * time.Minute).Format(time.RFC3339),
		"end_at":          now.Add(1 * time.Hour).Format(time.RFC3339),
		"suppress_alerts": true,
	}
	body, _ := json.Marshal(payload)

	w, _, ctx := requestWithClaims(t, "POST", "/maintenance", string(body), user)
	r := httptest.NewRequest("POST", "/maintenance", bytes.NewReader(body)).WithContext(ctx)

	h.Create(w, r)
	if w.Code != 201 {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
}
