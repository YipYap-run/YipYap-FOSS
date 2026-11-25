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

// Org is a tenant. SSOEnforced, when set, means members other than the
// owner must sign in through the org's SSO connection.
type Org struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Slug           string    `json:"slug"`
	Plan           OrgPlan   `json:"plan"`
	OncallDisplay  string    `json:"oncall_display,omitempty"`
	MFARequired    bool      `json:"mfa_required"`
	MFAGraceDays   int       `json:"mfa_grace_days"`
	SSOEnforced    bool      `json:"sso_enforced"`
	PromoExpiresAt string    `json:"promo_expires_at,omitempty"`
	PromoGraceDays int       `json:"promo_grace_days,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`

	// PaidGraceUntil is set when the org drops to Free from a paid or
	// promo plan. Paid channel types keep delivering until then. The
	// store sets it on the plan change and clears it on an upgrade.
	PaidGraceUntil *time.Time `json:"paid_grace_until,omitempty"`
}

// DefaultPaidGraceDays is the grace period for paid channel types after a
// downgrade when the org has no promo grace setting of its own.
const DefaultPaidGraceDays = 30

// PaidGraceDays is how long paid channel types keep delivering after the
// org drops to Free: its promo grace days, or DefaultPaidGraceDays.
func (o *Org) PaidGraceDays() int {
	if o.PromoGraceDays > 0 {
		return o.PromoGraceDays
	}
	return DefaultPaidGraceDays
}
