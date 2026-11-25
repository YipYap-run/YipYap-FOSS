package domain

import (
	"encoding/json"
	"time"
)

type NotificationChannel struct {
	ID      string `json:"id"`
	OrgID   string `json:"org_id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Config  string `json:"-"`
	Enabled bool   `json:"enabled"`

	// VerifiedAt is when the destination was confirmed by its owner. Only
	// channel types that need verification (SMS and voice in SaaS) read it;
	// for those a nil value means the channel is pending and is skipped at
	// delivery time.
	VerifiedAt *time.Time `json:"verified_at,omitempty"`

	// Delivery health, written by the notifier after each final outcome.
	// LastError holds a sanitized failure class (never a provider response
	// body, URL or credential).
	LastSuccessAt       *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt       *time.Time `json:"last_failure_at,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	// FailureNotifiedAt is when org owners were emailed about the current
	// failure streak. Nil when no email is outstanding.
	FailureNotifiedAt *time.Time `json:"failure_notified_at,omitempty"`
}

// Channel health states reported by the API.
const (
	ChannelHealthOK      = "ok"
	ChannelHealthFailing = "failing"
	ChannelHealthUnknown = "unknown"
)

// MaxChannelErrorLen caps the stored last_error text.
const MaxChannelErrorLen = 300

// ChannelHealth summarizes recent delivery outcomes for a channel.
type ChannelHealth struct {
	Status              string     `json:"status"`
	LastError           string     `json:"last_error,omitempty"`
	LastFailureAt       *time.Time `json:"last_failure_at,omitempty"`
	LastSuccessAt       *time.Time `json:"last_success_at,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	// OwnersNotifiedAt is when org owners were emailed about the current
	// failure streak, if they were.
	OwnersNotifiedAt *time.Time `json:"owners_notified_at,omitempty"`
}

// Health reports "failing" while the latest deliveries have failed, "ok"
// once a delivery has succeeded since the last failure, and "unknown" for
// a channel that has not delivered anything yet.
func (ch *NotificationChannel) Health() ChannelHealth {
	h := ChannelHealth{
		Status:              ChannelHealthUnknown,
		LastFailureAt:       ch.LastFailureAt,
		LastSuccessAt:       ch.LastSuccessAt,
		ConsecutiveFailures: ch.ConsecutiveFailures,
	}
	switch {
	case ch.ConsecutiveFailures > 0:
		h.Status = ChannelHealthFailing
		h.LastError = ch.LastError
		h.OwnersNotifiedAt = ch.FailureNotifiedAt
	case ch.LastSuccessAt != nil:
		h.Status = ChannelHealthOK
	}
	return h
}

type NotificationJob struct {
	ID           string `json:"id"`
	AlertID      string `json:"alert_id"`
	OrgID        string `json:"org_id"`
	MonitorID    string `json:"monitor_id,omitempty"`
	MonitorName  string `json:"monitor_name"`
	Status       string `json:"status,omitempty"` // alert status at send time: firing, acknowledged, resolved
	Severity     string `json:"severity"`
	Channel      string `json:"channel"`
	TargetConfig string `json:"target_config"`
	Message      string `json:"message"`
	AckURL       string `json:"ack_url"`
	DedupeKey    string `json:"dedupe_key"`

	// ChannelID and ChannelName identify the notification_channels row the
	// job was built from, so the notifier can record the delivery outcome
	// on the channel and the alert timeline. Empty for jobs that do not
	// come from a channel row (tests, support notifications).
	ChannelID   string `json:"channel_id,omitempty"`
	ChannelName string `json:"channel_name,omitempty"`

	RunbookURL       string          `json:"runbook_url,omitempty"`
	ServiceName      string          `json:"service_name,omitempty"`
	ServiceNotes     string          `json:"service_notes,omitempty"`
	ContextLinks     json.RawMessage `json:"context_links,omitempty"`
	DependencyStatus json.RawMessage `json:"dependency_status,omitempty"`
	// TargetUserID and ContactMethodID are set when the job pages a person
	// through one of their own contact methods rather than a shared
	// channel. Providers ignore them; they let delivery results, retries
	// and audit trails be tied back to the person and method.
	TargetUserID    string `json:"target_user_id,omitempty"`
	ContactMethodID string `json:"contact_method_id,omitempty"`
	EventAction     string `json:"event_action,omitempty"`
}

type AckEvent struct {
	AlertID string `json:"alert_id"`
	UserID  string `json:"user_id"`
	Channel string `json:"channel"`

	// Via says how the ack arrived when it was not a chat button or the
	// API. AckViaLink marks a signed acknowledge link from a notification;
	// ChannelID and ChannelName then name the channel that carried it.
	Via         string `json:"via,omitempty"`
	ChannelID   string `json:"channel_id,omitempty"`
	ChannelName string `json:"channel_name,omitempty"`
}

// AckViaLink is the AckEvent.Via value for a signed acknowledge link.
const AckViaLink = "link"

// AckViaVoice is the AckEvent.Via (and Channel) value for an ack made by
// pressing 1 on a voice call.
const AckViaVoice = "voice"

// AlertAckedMessage is broadcast on the "alert.acknowledged" bus subject
// after the escalation engine stores an ack, so live views can refresh.
type AlertAckedMessage struct {
	OrgID       string `json:"org_id"`
	AlertID     string `json:"alert_id"`
	MonitorID   string `json:"monitor_id"`
	MonitorName string `json:"monitor_name,omitempty"`
	Status      string `json:"status"`
}
