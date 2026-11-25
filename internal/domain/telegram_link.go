package domain

import "time"

// What a Telegram link code binds a chat to.
const (
	TelegramLinkPurposeChannel       = "channel"
	TelegramLinkPurposeContactMethod = "contact_method"
)

// TelegramLinkCode is a one-time code that binds the Telegram chat which
// sends "/link CODE" to the platform bot. Only the SHA-256 of the code is
// stored. SubjectID names an existing channel (or contact method) to bind;
// when it is empty, linking creates a new channel named ChannelName.
type TelegramLinkCode struct {
	ID          string
	CodeHash    string
	OrgID       string
	CreatedBy   string
	Purpose     string
	SubjectID   string
	ChannelName string
	ExpiresAt   time.Time
	UsedAt      *time.Time
	// Set once the code was used.
	LinkedChatID    string
	LinkedChatTitle string
	LinkedSubjectID string
	CreatedAt       time.Time
}
