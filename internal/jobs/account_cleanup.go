package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/accounts"
	"github.com/YipYap-run/YipYap-FOSS/internal/session"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

const accountDisableGracePeriod = 96 * time.Hour

// StartAccountCleanup runs a background goroutine that hard-deletes user
// accounts that have been disabled for longer than 96 hours.
func StartAccountCleanup(ctx context.Context, s store.Store) {
	go func() {
		// Run once immediately on startup, then hourly.
		cleanupDisabledAccounts(ctx, s)
		pruneExpiredSessions(ctx, s)
		pruneMFAChallenges(ctx, s)
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cleanupDisabledAccounts(ctx, s)
				pruneExpiredSessions(ctx, s)
				pruneMFAChallenges(ctx, s)
			}
		}
	}()
	slog.Info("account cleanup started", "interval", "1h", "grace_period", "96h")
}

// expiredSessionRetention keeps expired session rows for a week so recent
// sign-ins can still be inspected, then deletes them.
const expiredSessionRetention = 7 * 24 * time.Hour

func pruneExpiredSessions(ctx context.Context, s store.Store) {
	n, err := session.PruneExpired(ctx, s, time.Now().UTC().Add(-expiredSessionRetention))
	if err != nil {
		if !errors.Is(err, session.ErrUnsupported) {
			slog.Error("account cleanup: prune expired sessions failed", "error", err)
		}
		return
	}
	if n > 0 {
		slog.Info("account cleanup: pruned expired sessions", "count", n)
	}
}

func pruneMFAChallenges(ctx context.Context, s store.Store) {
	n, err := s.MFA().DeleteExpiredChallenges(ctx, time.Now().UTC())
	if err != nil {
		slog.Error("account cleanup: prune expired MFA challenges failed", "error", err)
		return
	}
	if n > 0 {
		slog.Info("account cleanup: pruned expired MFA challenges", "count", n)
	}
}

// cleanupDisabledAccounts deletes accounts disabled for longer than the
// grace period. Deleted rows become anonymized tombstones, so records that
// reference them stay and the next run does not pick them up again. A
// failure is logged per user and does not stop the rest of the batch.
func cleanupDisabledAccounts(ctx context.Context, s store.Store) {
	cutoff := time.Now().UTC().Add(-accountDisableGracePeriod)
	users, err := s.Users().ListDisabledBefore(ctx, cutoff)
	if err != nil {
		slog.Error("account cleanup: list disabled users failed", "error", err)
		return
	}
	var deleted, failed int
	for _, u := range users {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Re-fetch to guard against race with recovery (user may have been
		// re-enabled between ListDisabledBefore and this point).
		fresh, err := s.Users().GetByID(ctx, u.ID)
		if err != nil {
			failed++
			slog.Error("account cleanup: reload user failed", "user_id", u.ID, "error", err)
			continue
		}
		if fresh.DisabledAt == nil || fresh.DeletedAt != nil {
			continue
		}
		if err := accounts.Delete(ctx, s, fresh.ID, time.Now().UTC()); err != nil {
			if !errors.Is(err, accounts.ErrSessionRevoke) {
				failed++
				slog.Error("account cleanup: delete user failed", "user_id", u.ID, "org", fresh.OrgID, "error", err)
				continue
			}
			slog.Error("account cleanup: revoke sessions failed", "user_id", u.ID, "error", err)
		}
		deleted++
		slog.Info("account cleanup: deleted disabled user", "user_id", u.ID)

		// If this was the last member, delete the orphaned org and all its data.
		remaining, err := s.Users().ListByOrg(ctx, fresh.OrgID, store.ListParams{Limit: 1})
		if err != nil {
			slog.Error("account cleanup: check org members failed", "org", fresh.OrgID, "error", err)
			continue
		}
		if len(remaining) == 0 {
			// Delete API keys before org (no cascade FK).
			if keys, err := s.APIKeys().ListByOrg(ctx, fresh.OrgID, store.ListParams{Limit: 1000}); err == nil {
				for _, k := range keys {
					_ = s.APIKeys().Delete(ctx, k.ID)
				}
			}
			if err := s.Orgs().Delete(ctx, fresh.OrgID); err != nil {
				slog.Error("account cleanup: delete orphaned org failed", "org", fresh.OrgID, "error", err)
				continue
			}
			slog.Info("account cleanup: deleted orphaned org", "org", fresh.OrgID)
		}
	}
	if deleted > 0 || failed > 0 {
		slog.Info("account cleanup: run finished", "deleted", deleted, "failed", failed)
	}
}
