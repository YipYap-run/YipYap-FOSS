package domain

import "time"

// Billing holds billing state for a paying org.
type Billing struct {
	OrgID             string    `json:"org_id"`
	PaymentCustomerID string    `json:"payment_customer_id"`
	PaymentSubID      string    `json:"payment_subscription_id"`
	BillingEmail     string    `json:"billing_email"`
	SeatCount        int       `json:"seat_count"`
	HasEnterprise    bool      `json:"has_enterprise"`
	PeriodEnd        time.Time `json:"period_end"`
	Status           string    `json:"status"`
	SMSUsed          int       `json:"sms_used"`
	VoiceUsed        int       `json:"voice_used"`
	SMSVoiceResetAt  time.Time `json:"sms_voice_reset_at"`
}
