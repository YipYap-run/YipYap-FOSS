package domain

type EscalationPolicy struct {
	ID       string `json:"id"`
	OrgID    string `json:"org_id"`
	Name     string `json:"name"`
	Loop     bool   `json:"loop"`
	MaxLoops *int   `json:"max_loops,omitempty"`
}

type EscalationStep struct {
	ID                    string `json:"id"`
	PolicyID              string `json:"policy_id"`
	Position              int    `json:"position"`
	WaitSeconds           int    `json:"wait_seconds"`
	RepeatCount           int    `json:"repeat_count"`
	RepeatIntervalSeconds int    `json:"repeat_interval_seconds,omitempty"`
	IsTerminal            bool   `json:"is_terminal"`
}

type TargetType string

const (
	TargetOnCallPrimary   TargetType = "on_call_primary"
	TargetOnCallSecondary TargetType = "on_call_secondary"
	TargetUser            TargetType = "user"
	TargetTeam            TargetType = "team"
	TargetChannel         TargetType = "channel"
)

type StepTarget struct {
	ID           string     `json:"id"`
	StepID       string     `json:"step_id"`
	TargetType   TargetType `json:"target_type"`
	TargetID     string     `json:"target_id,omitempty"`
	ChannelID    string     `json:"channel_id"`
	Simultaneous bool       `json:"simultaneous"`
}
