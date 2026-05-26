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

	// Populated by joins/enrichment, not stored:
	UserEmail    string           `json:"user_email,omitempty"`
	Messages     []SupportMessage `json:"messages,omitempty"`
	MessageCount int              `json:"message_count,omitempty"`
}

// SupportMessage represents a message within a support ticket.
type SupportMessage struct {
	ID         string    `json:"id"`
	TicketID   string    `json:"ticket_id"`
	SenderType string    `json:"sender_type"` // "user", "staff", or "system"
	SenderID   string    `json:"sender_id"`
	SenderEmail string   `json:"sender_email"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"created_at"`
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

// SupportAttachment represents a file attached to a support message.
type SupportAttachment struct {
	ID          string    `json:"id"`
	MessageID   string    `json:"message_id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	StorageKey  string    `json:"storage_key"`
	CreatedAt   time.Time `json:"created_at"`
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
