package store

import (
	"context"
	"errors"
	"time"
)

// ErrChallengeNotFound is returned by TakeChallenge when the challenge does
// not exist, was already used, or has expired.
var ErrChallengeNotFound = errors.New("challenge not found or expired")

// MFALockoutPolicy bounds failed second-factor attempts per account. After
// MaxFailures failures inside Window the account is locked for Lockout.
type MFALockoutPolicy struct {
	MaxFailures int
	Window      time.Duration
	Lockout     time.Duration
}

// MFAGuard holds the shared state that protects second-factor checks: the
// last accepted one-time code step, the per-account failure counter, and
// credential challenge sessions. It lives in the database so every web
// replica sees the same state.
type MFAGuard interface {
	// AcceptTOTPStep records step as the user's last accepted one-time code
	// step. It returns false, and changes nothing, when step is not newer
	// than the stored one (the code was already used).
	AcceptTOTPStep(ctx context.Context, userID string, step int64) (bool, error)
	// MFALockedUntil returns the end of the user's lockout, or the zero time
	// when the account is not locked at now.
	MFALockedUntil(ctx context.Context, userID string, now time.Time) (time.Time, error)
	// RecordMFAFailure counts one failed attempt. When the count inside the
	// policy window reaches MaxFailures the account is locked and the
	// returned time is the end of the lockout; otherwise it is zero.
	RecordMFAFailure(ctx context.Context, userID string, now time.Time, p MFALockoutPolicy) (time.Time, error)
	// ResetMFAFailures clears the failure counter after a good attempt.
	ResetMFAFailures(ctx context.Context, userID string) error

	// PutChallenge stores a credential challenge session under key,
	// replacing any earlier one with the same key.
	PutChallenge(ctx context.Context, key string, data []byte, expiresAt time.Time) error
	// TakeChallenge removes and returns the challenge under key. It returns
	// ErrChallengeNotFound when there is none or it expired before now, so
	// a challenge is used at most once.
	TakeChallenge(ctx context.Context, key string, now time.Time) ([]byte, error)
	// DeleteExpiredChallenges removes challenges that expired before the
	// cutoff.
	DeleteExpiredChallenges(ctx context.Context, before time.Time) (int64, error)
}
