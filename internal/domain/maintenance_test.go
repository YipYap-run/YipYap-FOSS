package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestIsActiveAtOneShot(t *testing.T) {
	start := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	mw := &MaintenanceWindow{StartAt: start, EndAt: start.Add(2 * time.Hour)}
	cases := map[time.Duration]bool{-time.Minute: false, 0: true, time.Hour: true, 2 * time.Hour: true, 2*time.Hour + time.Second: false}
	for off, want := range cases {
		if got := mw.IsActiveAt(start.Add(off)); got != want {
			t.Errorf("offset %v: got %v, want %v", off, got, want)
		}
	}
}

// A daily 23:00 to 02:00 window must cover the hours after midnight, both
// when the end is entered on the next date and on the same date.
func TestIsActiveAtOvernightDaily(t *testing.T) {
	for name, end := range map[string]time.Time{
		"end next day": time.Date(2026, 3, 2, 2, 0, 0, 0, time.UTC),
		"end same day": time.Date(2026, 3, 1, 2, 0, 0, 0, time.UTC),
	} {
		t.Run(name, func(t *testing.T) {
			mw := &MaintenanceWindow{
				StartAt:        time.Date(2026, 3, 1, 23, 0, 0, 0, time.UTC),
				EndAt:          end,
				RecurrenceType: "daily",
				Timezone:       "UTC",
			}
			if name == "end same day" {
				// Same-date entry is invalid for the API (end before start),
				// but evaluation still treats it as overnight.
				mw.EndAt = time.Date(2026, 3, 1, 2, 0, 0, 0, time.UTC)
			}
			check := func(ts time.Time, want bool) {
				t.Helper()
				if got := mw.IsActiveAt(ts); got != want {
					t.Errorf("%s: got %v, want %v", ts, got, want)
				}
			}
			check(time.Date(2026, 3, 1, 23, 30, 0, 0, time.UTC), true)
			check(time.Date(2026, 3, 5, 23, 30, 0, 0, time.UTC), true)
			check(time.Date(2026, 3, 6, 1, 30, 0, 0, time.UTC), true) // after midnight
			check(time.Date(2026, 3, 6, 2, 0, 0, 0, time.UTC), true)
			check(time.Date(2026, 3, 6, 2, 1, 0, 0, time.UTC), false)
			check(time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC), false)
			// 01:00 on the first date is before the first occurrence.
			check(time.Date(2026, 3, 1, 1, 0, 0, 0, time.UTC), false)
		})
	}
}

// A weekly Friday 23:00 to Saturday 02:00 window covers early Saturday, but
// not early Friday (that belongs to Thursday, which is not selected).
func TestIsActiveAtOvernightWeekly(t *testing.T) {
	// 2026-03-06 is a Friday.
	mw := &MaintenanceWindow{
		StartAt:        time.Date(2026, 3, 6, 23, 0, 0, 0, time.UTC),
		EndAt:          time.Date(2026, 3, 7, 2, 0, 0, 0, time.UTC),
		RecurrenceType: "weekly",
		DaysOfWeek:     "[5]",
	}
	if !mw.IsActiveAt(time.Date(2026, 3, 14, 1, 0, 0, 0, time.UTC)) {
		t.Error("Saturday 01:00 should be covered by Friday's occurrence")
	}
	if mw.IsActiveAt(time.Date(2026, 3, 13, 1, 0, 0, 0, time.UTC)) {
		t.Error("Friday 01:00 should not be covered")
	}
	if !mw.IsActiveAt(time.Date(2026, 3, 13, 23, 15, 0, 0, time.UTC)) {
		t.Error("Friday 23:15 should be covered")
	}
}

// A daily 02:00 to 04:00 window in America/New_York stays at 02:00 local
// wall time across the November DST change, so its UTC instants shift by an
// hour. Evaluating in UTC would leave it an hour off after the change.
func TestIsActiveAtDSTDailyNewYork(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	mw := &MaintenanceWindow{
		StartAt:        time.Date(2026, 10, 20, 2, 0, 0, 0, ny), // EDT, 06:00 UTC
		EndAt:          time.Date(2026, 10, 20, 4, 0, 0, 0, ny),
		RecurrenceType: "daily",
		Timezone:       "America/New_York",
	}
	// Before the change (EDT, UTC-4): 02:30 local = 06:30 UTC.
	if !mw.IsActiveAt(time.Date(2026, 10, 25, 6, 30, 0, 0, time.UTC)) {
		t.Error("02:30 EDT should be active")
	}
	// After the change (EST, UTC-5): 02:30 local = 07:30 UTC.
	if !mw.IsActiveAt(time.Date(2026, 11, 10, 7, 30, 0, 0, time.UTC)) {
		t.Error("02:30 EST should be active")
	}
	// 06:30 UTC after the change is 01:30 EST, outside the window.
	if mw.IsActiveAt(time.Date(2026, 11, 10, 6, 30, 0, 0, time.UTC)) {
		t.Error("01:30 EST should not be active")
	}
	// 04:30 EST = 09:30 UTC is after the window.
	if mw.IsActiveAt(time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)) {
		t.Error("04:30 EST should not be active")
	}
}

// A monthly window evaluates the day of month in the window's zone.
func TestIsActiveAtMonthlyZone(t *testing.T) {
	tokyo := mustLoc(t, "Asia/Tokyo")
	mw := &MaintenanceWindow{
		StartAt:        time.Date(2026, 1, 1, 8, 0, 0, 0, tokyo),
		EndAt:          time.Date(2026, 1, 1, 9, 0, 0, 0, tokyo),
		RecurrenceType: "monthly",
		DayOfMonth:     1,
		Timezone:       "Asia/Tokyo",
	}
	// 2026-02-01 08:30 JST is 2026-01-31 23:30 UTC.
	if !mw.IsActiveAt(time.Date(2026, 1, 31, 23, 30, 0, 0, time.UTC)) {
		t.Error("the 1st in Tokyo should be active even though it is the 31st in UTC")
	}
}

func TestIsActiveAtRecurrenceEnd(t *testing.T) {
	end := time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)
	mw := &MaintenanceWindow{
		StartAt:         time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC),
		EndAt:           time.Date(2026, 3, 1, 11, 0, 0, 0, time.UTC),
		RecurrenceType:  "daily",
		RecurrenceEndAt: &end,
	}
	if !mw.IsActiveAt(time.Date(2026, 3, 2, 10, 30, 0, 0, time.UTC)) {
		t.Error("expected active before recurrence end")
	}
	if mw.IsActiveAt(time.Date(2026, 3, 3, 10, 30, 0, 0, time.UTC)) {
		t.Error("expected inactive after recurrence end")
	}
}

func TestParseMaintenanceLocal(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	got, err := ParseMaintenanceLocal("2026-07-04T22:00", ny)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 7, 5, 2, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("got %s, want %s", got, want)
	}
	if _, err := ParseMaintenanceLocal("2026-07-04T22:00:30", ny); err != nil {
		t.Errorf("seconds should be accepted: %v", err)
	}
	if _, err := ParseMaintenanceLocal("2026-07-04T22:00:00Z", ny); err == nil {
		t.Error("an RFC3339 value is not a local time")
	}
}

func TestLoadMaintenanceLocation(t *testing.T) {
	if loc, err := LoadMaintenanceLocation(""); err != nil || loc != time.UTC {
		t.Errorf("empty should be UTC, got %v %v", loc, err)
	}
	for _, bad := range []string{"Local", "Mars/Olympus", "../../etc/passwd"} {
		if _, err := LoadMaintenanceLocation(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	if _, err := LoadMaintenanceLocation("Europe/Berlin"); err != nil {
		t.Error(err)
	}
}

func TestMaintenanceWindowJSONLocalFields(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	rec := time.Date(2026, 12, 31, 23, 0, 0, 0, ny)
	mw := MaintenanceWindow{
		ID:              "w1",
		StartAt:         time.Date(2026, 11, 1, 1, 30, 0, 0, ny),
		EndAt:           time.Date(2026, 11, 1, 3, 0, 0, 0, ny),
		RecurrenceType:  "daily",
		RecurrenceEndAt: &rec,
		Timezone:        "America/New_York",
	}
	raw, err := json.Marshal(&mw)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["timezone"] != "America/New_York" || got["start_local"] != "2026-11-01T01:30" ||
		got["end_local"] != "2026-11-01T03:00" || got["recurrence_end_local"] != "2026-12-31T23:00" {
		t.Errorf("unexpected JSON: %s", raw)
	}
	if got["id"] != "w1" || got["recurrence_type"] != "daily" {
		t.Errorf("base fields missing: %s", raw)
	}

	// Older rows without a zone report UTC.
	mw.Timezone = ""
	raw, _ = json.Marshal(mw)
	_ = json.Unmarshal(raw, &got)
	if got["timezone"] != "UTC" {
		t.Errorf("empty zone should marshal as UTC: %s", raw)
	}
}

// active_now lets the SPA mark a repeating window "In progress" during an
// occurrence (including the after-midnight half of an overnight one) without
// evaluating recurrence in the browser.
func TestMaintenanceWindowJSONActiveNow(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	mw := MaintenanceWindow{
		ID:             "w1",
		StartAt:        time.Date(2026, 11, 2, 23, 0, 0, 0, ny),
		EndAt:          time.Date(2026, 11, 3, 2, 0, 0, 0, ny),
		RecurrenceType: "daily",
		Timezone:       "America/New_York",
	}
	orig := maintenanceNow
	t.Cleanup(func() { maintenanceNow = orig })

	cases := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"after midnight in a later occurrence", time.Date(2026, 11, 20, 1, 30, 0, 0, ny), true},
		{"midday between occurrences", time.Date(2026, 11, 20, 12, 0, 0, 0, ny), false},
		{"before the first occurrence", time.Date(2026, 11, 1, 23, 30, 0, 0, ny), false},
	}
	for _, tc := range cases {
		maintenanceNow = func() time.Time { return tc.at }
		raw, err := json.Marshal(mw)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got["active_now"] != tc.want {
			t.Errorf("%s: active_now = %v, want %v", tc.name, got["active_now"], tc.want)
		}
	}
}
