package domain

import "time"

// MonitorState defines a custom state beyond the built-in up/down/degraded.
type MonitorState struct {
	ID          string    `json:"id"`
	OrgID       string    `json:"org_id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	HealthClass string    `json:"health_class"` // "healthy", "degraded", "unhealthy"
	Severity    string    `json:"severity"`     // "critical", "warning", "info"
	Color       string    `json:"color"`        // hex color
	Position    int       `json:"position"`
	IsBuiltin   bool      `json:"is_builtin"`
	CreatedAt   time.Time `json:"created_at"`
}

// MonitorMatchRule maps a check result condition to a state.
type MonitorMatchRule struct {
	ID            string `json:"id"`
	MonitorID     string `json:"monitor_id"`
	Position      int    `json:"position"`
	StatusCode    *int   `json:"status_code,omitempty"`
	StatusCodeMin *int   `json:"status_code_min,omitempty"`
	StatusCodeMax *int   `json:"status_code_max,omitempty"`
	BodyMatch     string `json:"body_match,omitempty"`
	BodyMatchMode string `json:"body_match_mode,omitempty"` // "contains", "not_contains", "regex"
	HeaderMatch   string `json:"header_match,omitempty"`
	HeaderValue   string `json:"header_value,omitempty"`
	StateID       string `json:"state_id"`
	StateName     string `json:"state_name,omitempty"`       // populated by joins
	StateColor    string `json:"state_color,omitempty"`      // populated by joins
	HealthClass   string `json:"health_class,omitempty"`     // populated by joins: "healthy", "degraded", "unhealthy"
}
