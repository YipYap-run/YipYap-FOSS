package handlers

import (
	"context"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/limits"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// capProbe returns list params that fetch at most one row, the one at index
// capN-1. A row there means the org already has capN or more items. This keeps
// the check O(1) in transferred rows even when the operator sets a large cap.
func capProbe(capN int) store.ListParams {
	return store.ListParams{Limit: 1, Offset: capN - 1}
}

// checkMonitorLimit applies the operator cap (YIPYAP_MAX_MONITORS_PER_ORG). There are
// no plan limits in the self-hosted build, so with no cap set this is a no-op.
func checkMonitorLimit(ctx context.Context, s store.Store, orgID string, org *domain.Org) error {
	return checkMonitorLimitN(ctx, s, orgID, org, 1)
}

// checkMonitorLimitN rejects a request that would add n monitors past the
// operator's cap, so a batch is counted as a whole. FOSS has no plan
// limits: with no cap configured every request passes.
func checkMonitorLimitN(ctx context.Context, s store.Store, orgID string, _ *domain.Org, n int) error {
	max := limits.Current().MaxMonitors
	if legacy := CurrentOperatorLimits().MaxMonitorsPerOrg; legacy > max {
		max = legacy
	}
	if max <= 0 {
		return nil
	}
	monitors, err := s.Monitors().ListByOrg(ctx, orgID, store.MonitorFilter{ListParams: store.ListParams{Limit: max + 1}})
	if err != nil {
		return fmt.Errorf("failed to count monitors")
	}
	if len(monitors)+n > max {
		return &limits.CapError{Resource: "monitors", Max: max, Env: limits.EnvMaxMonitors}
	}
	return nil
}

// defaultCheckIntervalSeconds is used when a create omits the interval. It
// never goes below the operator's floor.
func defaultCheckIntervalSeconds(_ *domain.Org) int {
	return max(defaultCheckInterval, minCheckInterval(nil))
}

// checkIntervalFloor rejects an interval below the operator's floor.
func checkIntervalFloor(_ *domain.Org, seconds int) error {
	if floor := minCheckInterval(nil); seconds < floor {
		return &limits.CapError{Max: floor, Env: limits.EnvMinCheckInterval, Minimum: true}
	}
	return nil
}

// minCheckInterval is the operator-set interval floor
// (YIPYAP_MIN_CHECK_INTERVAL_SECONDS), the same for every org.
func minCheckInterval(_ *domain.Org) int {
	floor := limits.Current().MinCheckIntervalSeconds
	if legacy := CurrentOperatorLimits().MinCheckIntervalSeconds; legacy > floor {
		floor = legacy
	}
	return floor
}
