package handlers

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// checkTeamLimitN is a no-op in FOSS, which has no team plan limits.
func checkTeamLimitN(_ context.Context, _ store.Store, _ string, _ int) error {
	return nil
}
