package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

func createMaintenanceMonitor(t *testing.T, s *sqlite.SQLiteStore, orgID string) *domain.Monitor {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	m := &domain.Monitor{
		ID: uuid.New().String(), OrgID: orgID, Name: "api", Type: domain.MonitorHTTP,
		Config: json.RawMessage(`{"url":"https://example.com"}`), IntervalSeconds: 60,
		TimeoutSeconds: 10, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Monitors().Create(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	return m
}

func postMaintenance(t *testing.T, h *handlers.MaintenanceHandler, user *domain.User, body string) *httptest.ResponseRecorder {
	t.Helper()
	w, _, ctx := requestWithClaims(t, "POST", "/maintenance-windows", body, user)
	r := httptest.NewRequest("POST", "/maintenance-windows", bytes.NewReader([]byte(body))).WithContext(ctx)
	h.Create(w, r)
	return w
}

func patchMaintenance(t *testing.T, h *handlers.MaintenanceHandler, user *domain.User, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	w, rctx, ctx := requestWithClaims(t, "PATCH", "/maintenance-windows/"+id, body, user)
	rctx.URLParams.Add("id", id)
	r := httptest.NewRequest("PATCH", "/maintenance-windows/"+id, bytes.NewReader([]byte(body))).WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	h.Update(w, r)
	return w
}

// The exact body the old settings SPA sent for a one-time window, including
// recurrence_end_at as "", used to fail with 400 (ux-1).
func TestMaintenanceCreate_SPAOneTimeBody(t *testing.T) {
	s, org, user := setupMaintenanceTest(t)
	h := handlers.NewMaintenanceHandler(s)
	mon := createMaintenanceMonitor(t, s, org.ID)

	for _, recEnd := range []string{`""`, `null`} {
		body := `{"name":"DB upgrade","description":"","start_at":"2030-01-02T03:00:00.000Z",` +
			`"end_at":"2030-01-02T05:00:00.000Z","monitor_id":"` + mon.ID + `","public":false,` +
			`"suppress_alerts":true,"recurrence_type":"none","days_of_week":"[]","day_of_month":0,` +
			`"recurrence_end_at":` + recEnd + `}`
		w := postMaintenance(t, h, user, body)
		if w.Code != 201 {
			t.Fatalf("recurrence_end_at=%s: status %d body=%s", recEnd, w.Code, w.Body.String())
		}
		var got domain.MaintenanceWindow
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.RecurrenceEndAt != nil || got.MonitorID != mon.ID || !got.SuppressAlerts {
			t.Fatalf("unexpected window: %+v", got)
		}
	}

	// The current SPA omits recurrence_end_at and suppress_alerts defaults to true.
	w := postMaintenance(t, h, user, `{"name":"x","start_at":"2030-01-02T03:00:00Z","end_at":"2030-01-02T04:00:00Z"}`)
	if w.Code != 201 {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var got domain.MaintenanceWindow
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if !got.SuppressAlerts || got.RecurrenceType != "none" {
		t.Fatalf("defaults not applied: %+v", got)
	}
}

func TestMaintenanceCreate_RejectsBadInput(t *testing.T) {
	s, _, user := setupMaintenanceTest(t)
	h := handlers.NewMaintenanceHandler(s)
	cases := map[string]string{
		"end equals start": `{"name":"x","start_at":"2030-01-02T03:00:00Z","end_at":"2030-01-02T03:00:00Z"}`,
		"end before start": `{"name":"x","start_at":"2030-01-02T03:00:00Z","end_at":"2030-01-02T02:00:00Z"}`,
		"missing times":    `{"name":"x"}`,
		"blank name":       `{"name":"  ","start_at":"2030-01-02T03:00:00Z","end_at":"2030-01-02T04:00:00Z"}`,
		"bad recur end":    `{"name":"x","start_at":"2030-01-02T03:00:00Z","end_at":"2030-01-02T04:00:00Z","recurrence_type":"daily","recurrence_end_at":"soon"}`,
		"recur end early":  `{"name":"x","start_at":"2030-01-02T03:00:00Z","end_at":"2030-01-02T04:00:00Z","recurrence_type":"daily","recurrence_end_at":"2029-01-01T00:00:00Z"}`,
		"long name":        `{"name":"` + strings.Repeat("n", 201) + `","start_at":"2030-01-02T03:00:00Z","end_at":"2030-01-02T04:00:00Z"}`,
		"long description": `{"name":"x","description":"` + strings.Repeat("d", 2001) + `","start_at":"2030-01-02T03:00:00Z","end_at":"2030-01-02T04:00:00Z"}`,
	}
	for name, body := range cases {
		if w := postMaintenance(t, h, user, body); w.Code != 400 {
			t.Errorf("%s: status %d, want 400 (body=%s)", name, w.Code, w.Body.String())
		}
	}
}

// The settings form caps name at 200 and description at 2000 characters; the
// server enforces the same limits (counted in characters, not bytes).
func TestMaintenanceCreate_LengthLimits(t *testing.T) {
	s, _, user := setupMaintenanceTest(t)
	h := handlers.NewMaintenanceHandler(s)
	body := `{"name":"` + strings.Repeat("é", 200) + `","description":"` + strings.Repeat("é", 2000) +
		`","start_at":"2030-01-02T03:00:00Z","end_at":"2030-01-02T04:00:00Z"}`
	if w := postMaintenance(t, h, user, body); w.Code != 201 {
		t.Fatalf("at the limit: status %d body=%s", w.Code, w.Body.String())
	}
	w := postMaintenance(t, h, user, `{"name":"`+strings.Repeat("n", 201)+`","start_at":"2030-01-02T03:00:00Z","end_at":"2030-01-02T04:00:00Z"}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "name must be 200 characters or fewer") {
		t.Fatalf("long name: status %d body=%s", w.Code, w.Body.String())
	}
}

func TestMaintenanceUpdate_RejectsLongDescription(t *testing.T) {
	s, _, user := setupMaintenanceTest(t)
	h := handlers.NewMaintenanceHandler(s)
	w := postMaintenance(t, h, user, `{"name":"x","start_at":"2030-01-02T03:00:00Z","end_at":"2030-01-02T04:00:00Z"}`)
	var mw domain.MaintenanceWindow
	_ = json.Unmarshal(w.Body.Bytes(), &mw)
	w = patchMaintenance(t, h, user, mw.ID, `{"description":"`+strings.Repeat("d", 2001)+`"}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "description must be 2000 characters or fewer") {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
}

func TestMaintenanceUpdate_RejectsEndBeforeStart(t *testing.T) {
	s, _, user := setupMaintenanceTest(t)
	h := handlers.NewMaintenanceHandler(s)
	w := postMaintenance(t, h, user, `{"name":"x","start_at":"2030-01-02T03:00:00Z","end_at":"2030-01-02T04:00:00Z"}`)
	var mw domain.MaintenanceWindow
	_ = json.Unmarshal(w.Body.Bytes(), &mw)

	if w := patchMaintenance(t, h, user, mw.ID, `{"end_at":"2030-01-02T02:00:00Z"}`); w.Code != 400 {
		t.Fatalf("status %d, want 400", w.Code)
	}
	if w := patchMaintenance(t, h, user, mw.ID, `{"recurrence_end_at":""}`); w.Code != 200 {
		t.Fatalf("clearing recurrence_end_at: status %d body=%s", w.Code, w.Body.String())
	}
}

// Org B must not be able to attach a window to org A's monitor (mon-3).
func TestMaintenance_CrossTenantMonitor(t *testing.T) {
	s, orgA, _ := setupMaintenanceTest(t)
	h := handlers.NewMaintenanceHandler(s)
	monA := createMaintenanceMonitor(t, s, orgA.ID)

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	orgB := &domain.Org{ID: uuid.New().String(), Name: "B", Slug: "b-" + uuid.New().String()[:8], Plan: domain.PlanFree, CreatedAt: now, UpdatedAt: now}
	if err := s.Orgs().Create(ctx, orgB); err != nil {
		t.Fatal(err)
	}
	userB := &domain.User{ID: uuid.New().String(), OrgID: orgB.ID, Email: "b@x.com", PasswordHash: "h", Role: domain.RoleAdmin, CreatedAt: now, UpdatedAt: now}
	if err := s.Users().Create(ctx, userB); err != nil {
		t.Fatal(err)
	}

	start := now.Add(-time.Minute).Format(time.RFC3339)
	end := now.Add(time.Hour).Format(time.RFC3339)
	attack := `{"name":"x","start_at":"` + start + `","end_at":"` + end + `","monitor_id":"` + monA.ID + `","suppress_alerts":true}`
	if w := postMaintenance(t, h, userB, attack); w.Code != 404 {
		t.Fatalf("create on foreign monitor: status %d, want 404", w.Code)
	}
	if w := postMaintenance(t, h, userB, strings.ReplaceAll(attack, monA.ID, uuid.New().String())); w.Code != 404 {
		t.Fatalf("create on missing monitor: status %d, want 404", w.Code)
	}

	// An org-wide window in org B, then an attempt to retarget it at A's monitor.
	w := postMaintenance(t, h, userB, `{"name":"x","start_at":"`+start+`","end_at":"`+end+`"}`)
	if w.Code != 201 {
		t.Fatalf("status %d", w.Code)
	}
	var mw domain.MaintenanceWindow
	_ = json.Unmarshal(w.Body.Bytes(), &mw)
	if w := patchMaintenance(t, h, userB, mw.ID, `{"monitor_id":"`+monA.ID+`"}`); w.Code != 404 {
		t.Fatalf("update to foreign monitor: status %d, want 404", w.Code)
	}

	active, err := s.MaintenanceWindows().ListActiveByMonitor(ctx, monA.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("org A's monitor is suppressed by org B: %+v", active)
	}
}

// The settings Edit dialog sends the whole form as a PATCH. Turning a weekly
// window into a one-time, org-wide window sends recurrence_end_at "" to clear
// the saved end date and monitor_id "" for all monitors (ux-1).
func TestMaintenanceUpdate_SPAEditBody(t *testing.T) {
	s, org, user := setupMaintenanceTest(t)
	h := handlers.NewMaintenanceHandler(s)
	mon := createMaintenanceMonitor(t, s, org.ID)

	w := postMaintenance(t, h, user, `{"name":"Patch night","description":"","start_at":"2030-01-02T03:00:00.000Z",`+
		`"end_at":"2030-01-02T05:00:00.000Z","monitor_id":"`+mon.ID+`","public":true,"suppress_alerts":true,`+
		`"recurrence_type":"weekly","days_of_week":"[1,3]","day_of_month":0,"recurrence_end_at":"2030-06-01T00:00:00.000Z"}`)
	if w.Code != 201 {
		t.Fatalf("create: status %d body=%s", w.Code, w.Body.String())
	}
	var created domain.MaintenanceWindow
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.RecurrenceEndAt == nil || created.DaysOfWeek != "[1,3]" {
		t.Fatalf("unexpected created window: %+v", created)
	}

	edit := `{"name":"Patch night","description":"moved","start_at":"2030-01-03T03:00:00.000Z",` +
		`"end_at":"2030-01-03T04:00:00.000Z","monitor_id":"","public":false,"suppress_alerts":false,` +
		`"recurrence_type":"none","days_of_week":"[]","day_of_month":0,"recurrence_end_at":""}`
	w = patchMaintenance(t, h, user, created.ID, edit)
	if w.Code != 200 {
		t.Fatalf("edit: status %d body=%s", w.Code, w.Body.String())
	}
	got, err := s.MaintenanceWindows().GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RecurrenceEndAt != nil || got.MonitorID != "" || got.RecurrenceType != "none" ||
		got.SuppressAlerts || got.Public || got.Description != "moved" {
		t.Fatalf("edit not applied: %+v", got)
	}

	// The dialog maps this exact message to the monitor field.
	w = patchMaintenance(t, h, user, created.ID, `{"monitor_id":"`+uuid.New().String()+`"}`)
	if w.Code != 404 || !strings.Contains(w.Body.String(), "monitor not found") {
		t.Fatalf("unknown monitor: status %d body=%s", w.Code, w.Body.String())
	}
}
