// Package accounts holds account lifecycle operations shared by the API and
// background jobs.
package accounts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/session"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// Delete removes a user's account. The user row becomes an anonymized
// tombstone in one transaction (see store.UserStore.Anonymize), so support
// tickets, incidents and overrides they wrote keep a valid author shown as
// "Deleted user". Then every session of the user is revoked.
//
// A returned error means the account was not deleted unless it wraps
// ErrSessionRevoke, in which case the row is already anonymized and only the
// session cleanup failed. The row is disabled and has no credentials, so the
// leftover sessions cannot be refreshed into new ones.
func Delete(ctx context.Context, s store.Store, userID string, now time.Time) error {
	return deleteWith(ctx, s, userID, now, false)
}

func DeleteKeepAPIKeys(ctx context.Context, s store.Store, userID string, now time.Time) error {
	return deleteWith(ctx, s, userID, now, true)
}

func deleteWith(ctx context.Context, s store.Store, userID string, now time.Time, keepAPIKeys bool) error {
	if err := s.Tx(ctx, func(tx store.Store) error {
		if keepAPIKeys {
			return tx.Users().AnonymizeKeepAPIKeys(ctx, userID, now)
		}
		return tx.Users().Anonymize(ctx, userID, now)
	}); err != nil {
		return fmt.Errorf("delete user %s: %w", userID, err)
	}
	if err := session.RevokeAll(ctx, s, userID, ""); err != nil && !errors.Is(err, session.ErrUnsupported) {
		return fmt.Errorf("%w: %w", ErrSessionRevoke, err)
	}
	return nil
}

// ErrSessionRevoke wraps a session revocation failure after the account row
// was already anonymized.
var ErrSessionRevoke = errors.New("account deleted, but revoking its sessions failed")
