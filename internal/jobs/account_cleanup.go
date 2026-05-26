package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

const accountDisableGracePeriod = 96 * time.Hour

// StartAccountCleanup runs a background goroutine that hard-deletes user
// accounts that have been disabled for longer than 96 hours.
func StartAccountCleanup(ctx context.Context, s store.Store) {
	go func() {
		// Run once immediately on startup, then hourly.
		cleanupDisabledAccounts(ctx, s)
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cleanupDisabledAccounts(ctx, s)
			}
		}
	}()
	slog.Info("account cleanup started", "interval", "1h", "grace_period", "96h")
}

func cleanupDisabledAccounts(ctx context.Context, s store.Store) {
	cutoff := time.Now().UTC().Add(-accountDisableGracePeriod)
	users, err := s.Users().ListDisabledBefore(ctx, cutoff)
	if err != nil {
		slog.Error("account cleanup: list disabled users failed", "error", err)
		return
	}
	for _, u := range users {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Re-fetch to guard against race with recovery (user may have been
		// re-enabled between ListDisabledBefore and this point).
		fresh, err := s.Users().GetByID(ctx, u.ID)
		if err != nil || fresh.DisabledAt == nil {
			continue
		}
		if err := s.Users().Delete(ctx, fresh.ID); err != nil {
			slog.Error("account cleanup: delete user failed", "user_id", u.ID, "error", err)
			continue
		}
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
}
