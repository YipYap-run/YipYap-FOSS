package store

import (
	"context"
	"errors"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// ContactMethodStore manages per-user contact methods. Every read and write
// is scoped by user ID so a caller can only reach rows it already owns.
type ContactMethodStore interface {
	Create(ctx context.Context, m *domain.ContactMethod) error
	// Get returns the method only when it belongs to userID.
	Get(ctx context.Context, userID, id string) (*domain.ContactMethod, error)
	ListByUser(ctx context.Context, userID string) ([]*domain.ContactMethod, error)
	CountByUser(ctx context.Context, userID string) (int, error)
	// Update writes label and config. When resetVerification is true the
	// method goes back to unverified and any pending code is dropped.
	Update(ctx context.Context, m *domain.ContactMethod, resetVerification bool) error
	Delete(ctx context.Context, userID, id string) error

	// SetVerificationCode stores a hashed code, its expiry and the send
	// time, and resets the attempt counter.
	SetVerificationCode(ctx context.Context, userID, id, codeHash string, expiresAt, sentAt time.Time) error
	// RecordSend stamps last_sent_at (used for the send cooldown).
	RecordSend(ctx context.Context, userID, id string, at time.Time) error
	ReserveSendBudget(ctx context.Context, userID, orgID string, at time.Time, smsCode bool, userDailyLimit, orgSMSDailyLimit int) (bool, error)
	// IncrementVerifyAttempts bumps the attempt counter and returns the
	// new value.
	IncrementVerifyAttempts(ctx context.Context, userID, id string) (int, error)
	// MarkVerified sets verified_at and clears the pending code.
	MarkVerified(ctx context.Context, userID, id string, at time.Time) error

	// SeedDefaultEmail gives a user whose account email is verified an
	// email contact method and a paging rule for it, unless they already
	// have an email method for that address. Safe to call repeatedly.
	SeedDefaultEmail(ctx context.Context, userID string, at time.Time) error
}

// NotificationRuleStore manages per-user notification rules.
type NotificationRuleStore interface {
	Create(ctx context.Context, r *domain.NotificationRule) error
	Get(ctx context.Context, userID, id string) (*domain.NotificationRule, error)
	ListByUser(ctx context.Context, userID string) ([]*domain.NotificationRule, error)
	CountByUser(ctx context.Context, userID string) (int, error)
	Update(ctx context.Context, r *domain.NotificationRule) error
	Delete(ctx context.Context, userID, id string) error

	// ListPagingTargets returns the user's rules joined to their contact
	// methods, ordered by delay. Rules whose method is unverified are
	// left out, so an unverified method can never be paged.
	ListPagingTargets(ctx context.Context, userID string) ([]domain.PagingTarget, error)

	// PagingTypesByOrg returns, per user in the org, the distinct contact
	// method types that have a rule on a verified method. A user missing
	// from the map has nothing the engine can page.
	PagingTypesByOrg(ctx context.Context, orgID string) (map[string][]domain.ContactMethodType, error)
}

// ContactMethodProvider is implemented by stores that hold per-user contact
// methods and notification rules. Both SQL stores implement it.
type ContactMethodProvider interface {
	ContactMethods() ContactMethodStore
	NotificationRules() NotificationRuleStore
}

// ErrContactMethodNotFound is returned when a contact method does not exist or belongs to another user.
var ErrContactMethodNotFound = errors.New("contact method not found")

// ErrNotificationRuleNotFound is returned when a notification rule does not
// exist or belongs to another user.
var ErrNotificationRuleNotFound = errors.New("notification rule not found")
