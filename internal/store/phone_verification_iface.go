package store

import (
	"context"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// BudgetWindow is one fixed-window send quota. Key must be unique per
// window, Limit is the cap for that window, and WindowEnd is when the
// window closes (used for cleanup).
type BudgetWindow struct {
	Key       string
	Limit     int
	WindowEnd time.Time
}

// PhoneVerificationStore holds one-time codes sent to phone numbers before
// an SMS or voice destination goes live.
type PhoneVerificationStore interface {
	// Create inserts v without checking send quotas. Prefer the reservation
	// path (ReserveCooldown and ReserveWindow) for production sends; Create
	// exists for tests and backfills.
	Create(ctx context.Context, v *domain.PhoneVerification) error
	// Latest returns the newest verification for a subject, or nil when
	// there is none.
	Latest(ctx context.Context, subjectType, subjectID string) (*domain.PhoneVerification, error)
	// ConsumeAttempt adds one attempt if the row is unverified and below
	// maxAttempts. It reports false when no attempt was available, so
	// concurrent guesses cannot exceed the limit.
	ConsumeAttempt(ctx context.Context, id string, maxAttempts int) (bool, error)
	// MarkVerified sets verified_at once. It reports false when the row was
	// already verified.
	MarkVerified(ctx context.Context, id string, at time.Time) (bool, error)
	CountByOrgSince(ctx context.Context, orgID string, since time.Time) (int, error)
	CountByActorSince(ctx context.Context, orgID, actorID string, since time.Time) (int, error)
	CountByPhoneSince(ctx context.Context, phone string, since time.Time) (int, error)
	Delete(ctx context.Context, id string) error

	// ReserveWindow claims one unit in a fixed-window bucket, reporting false
	// when the bucket is full. It is a single conditional upsert, so
	// concurrent claims cannot exceed the cap.
	ReserveWindow(ctx context.Context, w BudgetWindow) (bool, error)
	// ReleaseWindow returns one unit to a bucket after a failed send.
	ReleaseWindow(ctx context.Context, w BudgetWindow) error
	// ReserveCooldown claims the per-subject resend slot until until. It
	// reports false when a slot is still active.
	ReserveCooldown(ctx context.Context, key string, now, until time.Time) (bool, error)
	// ReleaseCooldown drops the per-subject resend slot after a failed send.
	ReleaseCooldown(ctx context.Context, key string) error
	// CleanupBudget removes buckets whose window closed before the given
	// time.
	CleanupBudget(ctx context.Context, before time.Time) error
}

// PhoneVerificationProvider is implemented by stores that support phone
// verification. Callers type-assert the Store to reach it.
type PhoneVerificationProvider interface {
	PhoneVerifications() PhoneVerificationStore
}
