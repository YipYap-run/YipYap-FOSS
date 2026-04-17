package domain

import (
	"encoding/json"
	"time"
)

type MaintenanceWindow struct {
	ID              string     `json:"id"`
	OrgID           string     `json:"org_id"`
	MonitorID       string     `json:"monitor_id,omitempty"`
	Name            string     `json:"name"`
	Description     string     `json:"description,omitempty"`
	StartAt         time.Time  `json:"start_at"`
	EndAt           time.Time  `json:"end_at"`
	Public          bool       `json:"public"`
	SuppressAlerts  bool       `json:"suppress_alerts"`
	RecurrenceType  string     `json:"recurrence_type"`
	RecurrenceEndAt *time.Time `json:"recurrence_end_at,omitempty"`
	DaysOfWeek      string     `json:"days_of_week,omitempty"`
	DayOfMonth      int        `json:"day_of_month,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	CreatedBy       string     `json:"created_by"`
}

// IsActiveAt returns whether the maintenance window is active at time t.
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

	duration := mw.EndAt.Sub(mw.StartAt)
	startTime := mw.StartAt // time-of-day reference

	switch mw.RecurrenceType {
	case "daily":
		todayStart := time.Date(t.Year(), t.Month(), t.Day(), startTime.Hour(), startTime.Minute(), startTime.Second(), 0, t.Location())
		todayEnd := todayStart.Add(duration)
		return !t.Before(todayStart) && !t.After(todayEnd)

	case "weekly":
		var days []int
		_ = json.Unmarshal([]byte(mw.DaysOfWeek), &days)
		todayWeekday := int(t.Weekday()) // 0=Sun ... 6=Sat
		found := false
		for _, d := range days {
			if d == todayWeekday {
				found = true
				break
			}
		}
		if !found {
			return false
		}
		todayStart := time.Date(t.Year(), t.Month(), t.Day(), startTime.Hour(), startTime.Minute(), startTime.Second(), 0, t.Location())
		todayEnd := todayStart.Add(duration)
		return !t.Before(todayStart) && !t.After(todayEnd)

	case "monthly":
		if t.Day() != mw.DayOfMonth {
			return false
		}
		todayStart := time.Date(t.Year(), t.Month(), t.Day(), startTime.Hour(), startTime.Minute(), startTime.Second(), 0, t.Location())
		todayEnd := todayStart.Add(duration)
		return !t.Before(todayStart) && !t.After(todayEnd)
	}

	return false
}
