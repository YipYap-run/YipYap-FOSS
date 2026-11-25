package domain

import "time"

// SupportTicket represents a customer support ticket.
type SupportTicket struct {
	ID                string     `json:"id"`
	OrgID             string     `json:"org_id"`
	UserID            string     `json:"user_id"`
	Subject           string     `json:"subject"`
	Status            string     `json:"status"`   // open, pending, closed
	Priority          string     `json:"priority"` // low, normal, high, urgent
	ContextJSON       string     `json:"context_json,omitempty"`
	AssignedTo        string     `json:"assigned_to,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	ClosedAt          *time.Time `json:"closed_at,omitempty"`
	LastStaffReplyAt  *time.Time `json:"last_staff_reply_at,omitempty"`
	FirstStaffReplyAt *time.Time `json:"first_staff_reply_at,omitempty"`
	EscalatedAt       *time.Time `json:"escalated_at,omitempty"`
	CSATRating        *int       `json:"csat_rating,omitempty"`
	CSATComment       string     `json:"csat_comment,omitempty"`
	CSATSubmittedAt   *time.Time `json:"csat_submitted_at,omitempty"`
	SurveySentAt      *time.Time `json:"survey_sent_at,omitempty"`
	// Impact is what the customer picked when opening the ticket (for example
	// alerts_not_delivered). Priority is derived from it on the server.
	Impact string `json:"impact,omitempty"`
	// AwaitingStaffSince is the time of the oldest customer message with no
	// staff reply after it. Nil when staff spoke last. Escalation times out
	// on it.
	AwaitingStaffSince *time.Time `json:"awaiting_staff_since,omitempty"`
	// RelatedTicketID links a follow-up ticket to the closed ticket it
	// continues, once that ticket can no longer be reopened by reply.
	RelatedTicketID string `json:"related_ticket_id,omitempty"`

	// Populated by joins/enrichment, not stored:
	UserEmail string `json:"user_email,omitempty"`
	// UserDeleted is true when the ticket owner's account was deleted.
	// UserEmail is then empty and UserName is DeletedUserName.
	UserDeleted  bool             `json:"user_deleted,omitempty"`
	UserName     string           `json:"user_name,omitempty"`
	Messages     []SupportMessage `json:"messages,omitempty"`
	MessageCount int              `json:"message_count,omitempty"`
}

// SupportMessage represents a message within a support ticket.
type SupportMessage struct {
	ID          string `json:"id"`
	TicketID    string `json:"ticket_id"`
	SenderType  string `json:"sender_type"` // "user", "staff", or "system"
	SenderID    string `json:"sender_id"`
	SenderEmail string `json:"sender_email"`
	// SenderName is the author name captured at write time: the staff first
	// name for staff replies, the user's name or email for customer messages.
	SenderName string `json:"sender_name,omitempty"`
	// SenderDeleted is true when the sender's account was deleted.
	// SenderName is then DeletedUserName.
	SenderDeleted bool      `json:"sender_deleted,omitempty"`
	Body          string    `json:"body"`
	CreatedAt     time.Time `json:"created_at"`
}

// SupportNotifyChannel represents a staff notification channel for support events.
type SupportNotifyChannel struct {
	ID                 string    `json:"id"`
	Type               string    `json:"type"`
	Name               string    `json:"name"`
	Config             string    `json:"-"` // encrypted, never sent to client
	Enabled            bool      `json:"enabled"`
	MinPriority        string    `json:"min_priority"`
	NotifyOnNew        bool      `json:"notify_on_new"`
	NotifyOnReply      bool      `json:"notify_on_reply"`
	NotifyOnEscalation bool      `json:"notify_on_escalation"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// SupportMacro represents a canned response template.
type SupportMacro struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Category  string    `json:"category"`
	SortOrder int       `json:"sort_order"`
	CreatedAt time.Time `json:"created_at"`
}

// SupportTicketPriority constants.
const (
	SupportPriorityLow    = "low"
	SupportPriorityNormal = "normal"
	SupportPriorityHigh   = "high"
	SupportPriorityUrgent = "urgent"
)

// SupportTicketStatus constants.
const (
	SupportStatusOpen    = "open"
	SupportStatusPending = "pending"
	SupportStatusClosed  = "closed"
)

// SupportSenderType constants.
const (
	SupportSenderUser   = "user"
	SupportSenderStaff  = "staff"
	SupportSenderSystem = "system"
	// SupportSenderNote is staff-only. Customer-facing ticket reads must filter
	// these out.
	SupportSenderNote = "note"
)

// SupportPriorityRank returns a numeric rank for priority comparison.
// Higher rank = higher priority.
func SupportPriorityRank(p string) int {
	switch p {
	case SupportPriorityLow:
		return 0
	case SupportPriorityNormal:
		return 1
	case SupportPriorityHigh:
		return 2
	case SupportPriorityUrgent:
		return 3
	default:
		return 1
	}
}

// SupportPriorityToSeverity maps ticket priority to notification severity.
func SupportPriorityToSeverity(p string) string {
	switch p {
	case SupportPriorityUrgent:
		return "critical"
	case SupportPriorityHigh:
		return "warning"
	default:
		return "info"
	}
}
