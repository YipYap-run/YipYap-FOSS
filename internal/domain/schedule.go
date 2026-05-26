package domain

import "time"

type Team struct {
	ID    string `json:"id"`
	OrgID string `json:"org_id"`
	Name  string `json:"name"`
}

type TeamMember struct {
	TeamID   string `json:"team_id"`
	UserID   string `json:"user_id"`
	Position int    `json:"position"`
}

type RotationInterval string

const (
	RotationDaily  RotationInterval = "daily"
	RotationWeekly RotationInterval = "weekly"
	RotationCustom RotationInterval = "custom_hours"
)

type Schedule struct {
	ID                    string           `json:"id"`
	TeamID                string           `json:"team_id"`
	RotationInterval      RotationInterval `json:"rotation_interval"`
	RotationIntervalHours int              `json:"rotation_interval_hours,omitempty"`
	HandoffTime           string           `json:"handoff_time"`
	EffectiveFrom         time.Time        `json:"effective_from"`
	Timezone              string           `json:"timezone"`
}

type ScheduleOverride struct {
	ID         string    `json:"id"`
	ScheduleID string    `json:"schedule_id"`
	UserID     string    `json:"user_id"`
	StartAt    time.Time `json:"start_at"`
	EndAt      time.Time `json:"end_at"`
	Reason     string    `json:"reason,omitempty"`
}
