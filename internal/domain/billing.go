package domain

import "time"

// Billing holds billing state for a paying org.
type Billing struct {
	OrgID             string    `json:"org_id"`
	PaymentCustomerID string    `json:"payment_customer_id"`
	PaymentSubID      string    `json:"payment_subscription_id"`
	BillingEmail      string    `json:"billing_email"`
	SeatCount         int       `json:"seat_count"`
	HasEnterprise     bool      `json:"has_enterprise"`
	PeriodEnd         time.Time `json:"period_end"`
	Status            string    `json:"status"`
	SMSVoiceResetAt   time.Time `json:"sms_voice_reset_at"`
}

// CheckoutAttempt tracks the org's current Stripe Checkout Session so a
// repeated or parallel upgrade click reuses it instead of opening a second
// session (and possibly a second subscription). Attempt numbers the
// session for its idempotency key; a new attempt starts only after the
// previous session expired, failed or was replaced.
type CheckoutAttempt struct {
	OrgID     string
	Attempt   int
	SessionID string
	URL       string
	Seats     int
	TrialEnd  time.Time // zero when the session has no trial
	ExpiresAt time.Time
}
