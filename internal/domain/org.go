package domain

import "time"

type OrgPlan string

const PlanFree OrgPlan = "free"

// Free tier limits (shared, available in FOSS builds).
const FreeMaxMonitors = 5

// MinCheckIntervalSeconds returns the minimum check interval for the given plan.
func MinCheckIntervalSeconds(plan OrgPlan) int {
	switch plan {
	case "enterprise":
		return 10
	case "pro":
		return 30
	default:
		return 300
	}
}

type Org struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Slug           string    `json:"slug"`
	Plan           OrgPlan   `json:"plan"`
	OncallDisplay  string    `json:"oncall_display,omitempty"`
	MFARequired    bool      `json:"mfa_required"`
	MFAGraceDays   int       `json:"mfa_grace_days"`
	PromoExpiresAt string    `json:"promo_expires_at,omitempty"`
	PromoGraceDays int       `json:"promo_grace_days,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
