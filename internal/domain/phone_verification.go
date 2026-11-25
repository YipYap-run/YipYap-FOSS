package domain

import "time"

// Subjects a phone verification can belong to. Channels are org-level SMS
// and voice notification channels; contact methods are per-user numbers.
const (
	PhoneSubjectChannel       = "channel"
	PhoneSubjectContactMethod = "contact_method"
)

// Delivery methods for a phone verification code.
const (
	PhoneVerifyMethodSMS   = "sms"
	PhoneVerifyMethodVoice = "voice"
)

// PhoneVerification is one one-time code sent to a phone number. Only the
// HMAC of the code is stored. A row is single use: VerifiedAt is set once
// and a new send creates a new row.
type PhoneVerification struct {
	ID          string
	OrgID       string
	SubjectType string
	SubjectID   string
	Phone       string // E.164
	Method      string
	// CreatedBy is the user who asked for the code, so sends are metered
	// per member as well as per org.
	CreatedBy   string
	CodeHash    string
	Attempts    int
	ExpiresAt   time.Time
	VerifiedAt  *time.Time
	CreatedAt   time.Time
}
