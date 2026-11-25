package store

import (
	"context"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// TelegramLinkCodeStore holds one-time codes that link a Telegram chat to
// a channel or contact method.
type TelegramLinkCodeStore interface {
	Create(ctx context.Context, c *domain.TelegramLinkCode) error
	// GetByID returns nil, nil when the code does not exist.
	GetByID(ctx context.Context, id string) (*domain.TelegramLinkCode, error)
	// GetByHash returns nil, nil when no code has that hash.
	GetByHash(ctx context.Context, codeHash string) (*domain.TelegramLinkCode, error)
	// Claim sets used_at if the code is unused and not expired at now. It
	// reports false otherwise, so two chats racing on one code cannot both
	// win.
	Claim(ctx context.Context, id string, now time.Time) (bool, error)
	// SetLinked records which chat used the code and what it was bound to.
	SetLinked(ctx context.Context, id, chatID, chatTitle, subjectID string) error
	CountByOrgSince(ctx context.Context, orgID string, since time.Time) (int, error)
}

// TelegramLinkCodeProvider is implemented by stores that support Telegram
// chat linking. Callers type-assert the Store to reach it.
type TelegramLinkCodeProvider interface {
	TelegramLinkCodes() TelegramLinkCodeStore
}
