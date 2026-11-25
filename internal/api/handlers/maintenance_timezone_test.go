package handlers_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// The SPA sends wall-clock values plus a timezone; the server resolves them
// in that zone and echoes them back so Edit round-trips every field
// (core-monitoring decision 4, mon-7).
func TestMaintenanceLocalTimesAndTimezoneRoundTrip(t *testing.T) {
	s, org, user := setupMaintenanceTest(t)
	h := handlers.NewMaintenanceHandler(s)
	mon := createMaintenanceMonitor(t, s, org.ID)

	create := `{"name":"Nightly backup","description":"db","monitor_id":"` + mon.ID + `",` +
		`"timezone":"America/New_York","start_local":"2030-07-04T23:00","end_local":"2030-07-05T01:30",` +
		`"public":true,"suppress_alerts":true,"recurrence_type":"weekly","days_of_week":"[4]",` +
		`"recurrence_end_local":"2030-12-31T00:00"}`
	w := postMaintenance(t, h, user, create)
	if w.Code != 201 {
		t.Fatalf("create: status %d body=%s", w.Code, w.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"name": "Nightly backup", "description": "db", "monitor_id": mon.ID,
		"timezone": "America/New_York", "start_local": "2030-07-04T23:00", "end_local": "2030-07-05T01:30",
		"recurrence_end_local": "2030-12-31T00:00", "public": true, "suppress_alerts": true,
		"recurrence_type": "weekly", "days_of_week": "[4]",
		// 23:00 EDT is 03:00 UTC the next day.
		"start_at": "2030-07-05T03:00:00Z", "end_at": "2030-07-05T05:30:00Z",
	}
	for k, v := range want {
		if created[k] != v {
			t.Errorf("create %s = %v, want %v", k, created[k], v)
		}
	}
	id := created["id"].(string)

	// Edit everything, as the SPA does from the returned local values.
	edit := `{"name":"Nightly backup 2","description":"","monitor_id":"","timezone":"Europe/Berlin",` +
		`"start_local":"2030-01-10T02:00","end_local":"2030-01-10T03:00","public":false,` +
		`"suppress_alerts":false,"recurrence_type":"daily","days_of_week":"[]","day_of_month":0,` +
		`"recurrence_end_local":""}`
	w = patchMaintenance(t, h, user, id, edit)
	if w.Code != 200 {
		t.Fatalf("edit: status %d body=%s", w.Code, w.Body.String())
	}
	got, err := s.MaintenanceWindows().GetByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Nightly backup 2" || got.Description != "" || got.MonitorID != "" ||
		got.Timezone != "Europe/Berlin" || got.Public || got.SuppressAlerts ||
		got.RecurrenceType != "daily" || got.RecurrenceEndAt != nil {
		t.Fatalf("edit not applied: %+v", got)
	}
	// 02:00 CET is 01:00 UTC.
	if !got.StartAt.Equal(time.Date(2030, 1, 10, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("start_at = %s", got.StartAt)
	}
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), `"start_local":"2030-01-10T02:00"`) {
		t.Fatalf("local start not echoed: %s", raw)
	}
}

func TestMaintenanceRejectsBadTimezoneAndLocal(t *testing.T) {
	s, _, user := setupMaintenanceTest(t)
	h := handlers.NewMaintenanceHandler(s)
	for name, body := range map[string]string{
		"unknown zone": `{"name":"x","timezone":"Mars/Base","start_local":"2030-01-01T00:00","end_local":"2030-01-01T01:00"}`,
		"local zone":   `{"name":"x","timezone":"Local","start_local":"2030-01-01T00:00","end_local":"2030-01-01T01:00"}`,
		"bad local":    `{"name":"x","timezone":"UTC","start_local":"tomorrow","end_local":"2030-01-01T01:00"}`,
		"end first":    `{"name":"x","timezone":"UTC","start_local":"2030-01-01T02:00","end_local":"2030-01-01T01:00"}`,
	} {
		if w := postMaintenance(t, h, user, body); w.Code != 400 {
			t.Errorf("%s: status %d body=%s", name, w.Code, w.Body.String())
		}
	}
}

// Requests without a timezone keep working and store UTC.
func TestMaintenanceDefaultTimezone(t *testing.T) {
	s, _, user := setupMaintenanceTest(t)
	h := handlers.NewMaintenanceHandler(s)
	w := postMaintenance(t, h, user, `{"name":"x","start_at":"2030-01-02T03:00:00Z","end_at":"2030-01-02T04:00:00Z"}`)
	if w.Code != 201 {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var got domain.MaintenanceWindow
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Timezone != "UTC" {
		t.Fatalf("timezone = %q", got.Timezone)
	}
}
