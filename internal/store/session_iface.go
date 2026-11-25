package store

import (
	"context"
	"errors"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// ErrSessionNotFound is returned when a session row does not exist, or does
// not belong to the user named in the call.
var ErrSessionNotFound = errors.New("session not found")

// SessionStore manages server-side login sessions (user_sessions).
type SessionStore interface {
	Create(ctx context.Context, s *domain.Session) error
	// Get returns one session or ErrSessionNotFound.
	Get(ctx context.Context, id string) (*domain.Session, error)
	// ListActiveByUser returns the user's sessions that are neither revoked
	// nor expired at now, newest first.
	ListActiveByUser(ctx context.Context, userID string, now time.Time) ([]*domain.Session, error)
	// Touch sets last_seen_at to at when the stored value is older than
	// notAfter, so concurrent requests write at most once per interval.
	Touch(ctx context.Context, id string, at, notAfter time.Time) error
	// Extend moves expires_at of an unrevoked session to expiresAt.
	Extend(ctx context.Context, id string, expiresAt time.Time) error
	// Revoke revokes one of userID's sessions. It returns ErrSessionNotFound
	// when the session does not exist, belongs to someone else, or is
	// already revoked.
	Revoke(ctx context.Context, userID, id string, at time.Time) error
	// RevokeAllForUser revokes every unrevoked session of userID except
	// exceptID (pass "" to revoke all) and returns the revoked IDs.
	RevokeAllForUser(ctx context.Context, userID, exceptID string, at time.Time) ([]string, error)
	// RevokeOrgSessions revokes the unrevoked sessions of org members that
	// the filter does not keep and returns the revoked IDs.
	RevokeOrgSessions(ctx context.Context, orgID string, f OrgSessionFilter, at time.Time) ([]string, error)
	// DeleteExpired removes sessions that expired before the cutoff.
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
}

// OrgSessionFilter picks the sessions RevokeOrgSessions keeps. A session is
// kept when its auth method is in KeepMethods or its user's role is in
// KeepRoles. Sessions whose user_id is not a user of the org (staff
// impersonation) are always kept.
type OrgSessionFilter struct {
	KeepMethods []string
	KeepRoles   []string
}

// SessionProvider is implemented by stores that keep server-side sessions.
type SessionProvider interface {
	Sessions() SessionStore
}
