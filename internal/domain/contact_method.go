package domain

import "time"

// ContactMethodType names how a person can be reached when they are paged.
type ContactMethodType string

const (
	ContactEmail     ContactMethodType = "email"
	ContactSMS       ContactMethodType = "sms"
	ContactVoice     ContactMethodType = "voice"
	ContactNtfy      ContactMethodType = "ntfy"
	ContactPushover  ContactMethodType = "pushover"
	ContactPagerDuty ContactMethodType = "pagerduty"
	ContactWebhook   ContactMethodType = "webhook"
	ContactSlackDM   ContactMethodType = "slack_dm"
	ContactDiscordDM ContactMethodType = "discord_dm"
	ContactTelegram  ContactMethodType = "telegram"
)

// AllContactMethodTypes lists every contact method type in display order.
var AllContactMethodTypes = []ContactMethodType{
	ContactEmail, ContactSMS, ContactVoice, ContactNtfy, ContactPushover,
	ContactPagerDuty, ContactWebhook, ContactSlackDM, ContactDiscordDM, ContactTelegram,
}

// ContactMethod is one way to reach a user (their email address, phone, a
// push topic, a chat DM). Config holds the type-specific destination as JSON
// and is never serialized directly; handlers render a redacted view.
//
// A method takes part in paging only once VerifiedAt is set.
type ContactMethod struct {
	ID         string            `json:"id"`
	OrgID      string            `json:"org_id"`
	UserID     string            `json:"user_id"`
	Type       ContactMethodType `json:"type"`
	Label      string            `json:"label"`
	Config     string            `json:"-"`
	VerifiedAt *time.Time        `json:"verified_at,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`

	// Verification state. Never sent to clients.
	VerifyCodeHash  string     `json:"-"`
	VerifyExpiresAt *time.Time `json:"-"`
	VerifyAttempts  int        `json:"-"`
	LastSentAt      *time.Time `json:"-"`
}

// Verified reports whether the method may be used for paging.
func (m *ContactMethod) Verified() bool { return m != nil && m.VerifiedAt != nil }

// NotificationUrgency selects which alerts a notification rule applies to.
type NotificationUrgency string

const (
	UrgencyHigh NotificationUrgency = "high"
	UrgencyLow  NotificationUrgency = "low"
	UrgencyAny  NotificationUrgency = "any"
)

// UrgencyForSeverity maps an alert severity to the urgency a notification
// rule matches: critical alerts are high urgency, everything else is low.
func UrgencyForSeverity(sev Severity) NotificationUrgency {
	if sev == SeverityCritical {
		return UrgencyHigh
	}
	return UrgencyLow
}

// Matches reports whether a rule with urgency u applies to an alert of
// urgency alert.
func (u NotificationUrgency) Matches(alert NotificationUrgency) bool {
	return u == UrgencyAny || u == alert
}

// NotificationRule says "when I am paged, contact method X after N minutes"
// so a user can ask for, say, SMS right away and a voice call after 5.
type NotificationRule struct {
	ID              string              `json:"id"`
	OrgID           string              `json:"org_id"`
	UserID          string              `json:"user_id"`
	ContactMethodID string              `json:"contact_method_id"`
	DelayMinutes    int                 `json:"delay_minutes"`
	Urgency         NotificationUrgency `json:"urgency"`
	CreatedAt       time.Time           `json:"created_at"`
}

// PagingTarget pairs a notification rule with its verified contact method.
// It is what the escalation engine fans out to when it pages a person.
type PagingTarget struct {
	Rule   *NotificationRule
	Method *ContactMethod
}
