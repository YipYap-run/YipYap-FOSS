package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	// Embed the IANA time zone database. Release images are built FROM
	// scratch and carry no /usr/share/zoneinfo, so without this every zone
	// but UTC fails to load and per-window time zones would silently break.
	_ "time/tzdata"
)

// DefaultMaintenanceTimezone is the zone used for windows saved before
// windows carried their own zone, and when a request omits one.
const DefaultMaintenanceTimezone = "UTC"

// localLayout is the wall-clock format exchanged with the SPA
// (a datetime-local input value). Seconds are optional on input.
const localLayout = "2006-01-02T15:04"

type MaintenanceWindow struct {
	ID          string `json:"id"`
	OrgID       string `json:"org_id"`
	MonitorID   string `json:"monitor_id,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// StartAt and EndAt are absolute instants. For recurring windows they
	// define the first occurrence; later occurrences repeat the same wall
	// clock times in Timezone.
	StartAt         time.Time  `json:"start_at"`
	EndAt           time.Time  `json:"end_at"`
	Public          bool       `json:"public"`
	SuppressAlerts  bool       `json:"suppress_alerts"`
	RecurrenceType  string     `json:"recurrence_type"`
	RecurrenceEndAt *time.Time `json:"recurrence_end_at,omitempty"`
	DaysOfWeek      string     `json:"days_of_week,omitempty"`
	DayOfMonth      int        `json:"day_of_month,omitempty"`
	// Timezone is the IANA zone the window's times were entered in and in
	// which recurrence is evaluated, so a daily 02:00 window stays at 02:00
	// local time across DST changes.
	Timezone  string    `json:"timezone"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
}

// maintenanceNow is the clock MarshalJSON uses for active_now. Tests
// replace it.
var maintenanceNow = time.Now

// MarshalJSON adds the window's times as wall-clock values in its own
// zone (start_local, end_local, recurrence_end_local) so a client can fill
// its form without converting through the browser's zone. active_now says
// whether an occurrence covers the current time, so a client does not have
// to evaluate recurrence rules itself.
func (mw MaintenanceWindow) MarshalJSON() ([]byte, error) {
	type plain MaintenanceWindow
	out := struct {
		plain
		Timezone           string `json:"timezone"`
		StartLocal         string `json:"start_local"`
		EndLocal           string `json:"end_local"`
		RecurrenceEndLocal string `json:"recurrence_end_local,omitempty"`
		ActiveNow          bool   `json:"active_now"`
	}{plain: plain(mw)}
	out.ActiveNow = mw.IsActiveAt(maintenanceNow())
	loc := mw.Location()
	out.Timezone = loc.String()
	out.StartLocal = mw.StartAt.In(loc).Format(localLayout)
	out.EndLocal = mw.EndAt.In(loc).Format(localLayout)
	if mw.RecurrenceEndAt != nil {
		out.RecurrenceEndLocal = mw.RecurrenceEndAt.In(loc).Format(localLayout)
	}
	return json.Marshal(out)
}

// LoadMaintenanceLocation validates an IANA zone name. An empty name means
// UTC. "Local" is rejected because it depends on the server's settings.
func LoadMaintenanceLocation(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return time.UTC, nil
	}
	if name == "Local" {
		return nil, fmt.Errorf("timezone must be an IANA zone name such as Europe/Berlin")
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("timezone must be an IANA zone name such as Europe/Berlin")
	}
	return loc, nil
}

// ParseMaintenanceLocal parses a wall-clock value ("2006-01-02T15:04" or
// with ":05" seconds) in loc. A wall time skipped by a DST change resolves
// the way time.Date does (shifted forward by the gap).
func ParseMaintenanceLocal(v string, loc *time.Location) (time.Time, error) {
	v = strings.TrimSpace(v)
	for _, layout := range []string{localLayout, localLayout + ":05"} {
		if t, err := time.ParseInLocation(layout, v, loc); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("must be a local date and time like 2026-01-31T22:00")
}

// Location returns the window's zone, falling back to UTC for an empty or
// unknown name so evaluation never fails.
func (mw *MaintenanceWindow) Location() *time.Location {
	if mw.Timezone == "" {
		return time.UTC
	}
	loc, err := LoadMaintenanceLocation(mw.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// IsActiveAt returns whether the maintenance window is active at time t.
//
// Recurring windows repeat the first occurrence's wall-clock start and end
// in the window's zone. An occurrence that crosses midnight (end wall time
// before the start wall time, or an end date after the start date) belongs
// to the day it starts on, so a Friday 23:00 to 02:00 weekly window covers
// early Saturday too.
func (mw *MaintenanceWindow) IsActiveAt(t time.Time) bool {
	if mw.RecurrenceType == "" || mw.RecurrenceType == "none" {
		return !t.Before(mw.StartAt) && !t.After(mw.EndAt)
	}
	// Past recurrence end?
	if mw.RecurrenceEndAt != nil && t.After(*mw.RecurrenceEndAt) {
		return false
	}
	// Before first occurrence?
	if t.Before(mw.StartAt) {
		return false
	}

	loc := mw.Location()
	start := mw.StartAt.In(loc)
	end := mw.EndAt.In(loc)

	// Wall-clock shape of one occurrence: start time of day, end time of
	// day, and how many calendar days later the end falls.
	startDate := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	endDate := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC)
	dayOffset := int(endDate.Sub(startDate).Hours() / 24)
	startClock := clockSeconds(start)
	endClock := clockSeconds(end)
	if dayOffset <= 0 && endClock <= startClock {
		// Same date but the end wall time is not after the start: an
		// overnight window entered on one date.
		dayOffset = 1
	}
	if dayOffset < 0 {
		dayOffset = 0
	}

	var days []int
	if mw.RecurrenceType == "weekly" {
		_ = json.Unmarshal([]byte(mw.DaysOfWeek), &days)
	}

	local := t.In(loc)
	// An occurrence that started up to dayOffset days ago can still cover t.
	for back := 0; back <= dayOffset; back++ {
		day := time.Date(local.Year(), local.Month(), local.Day()-back, 0, 0, 0, 0, loc)
		dayUTC := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
		if dayUTC.Before(startDate) {
			break // before the first occurrence
		}
		if !mw.recursOn(day, days) {
			continue
		}
		occStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, startClock, 0, loc)
		occEnd := time.Date(day.Year(), day.Month(), day.Day()+dayOffset, 0, 0, endClock, 0, loc)
		if !t.Before(occStart) && !t.After(occEnd) {
			return true
		}
	}
	return false
}

// recursOn reports whether an occurrence starts on day (a date in the
// window's zone).
func (mw *MaintenanceWindow) recursOn(day time.Time, weekdays []int) bool {
	switch mw.RecurrenceType {
	case "daily":
		return true
	case "weekly":
		wd := int(day.Weekday()) // 0=Sun ... 6=Sat
		for _, d := range weekdays {
			if d == wd {
				return true
			}
		}
		return false
	case "monthly":
		return day.Day() == mw.DayOfMonth
	}
	return false
}

func clockSeconds(t time.Time) int {
	return t.Hour()*3600 + t.Minute()*60 + t.Second()
}
