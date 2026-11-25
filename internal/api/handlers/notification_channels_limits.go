package handlers

import (
	"context"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/limits"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// checkChannelLimit applies the operator cap
// (YIPYAP_MAX_NOTIFICATION_CHANNELS_PER_ORG). No cap set means unlimited.
func checkChannelLimit(ctx context.Context, s store.Store, orgID string) error {
	return checkChannelLimitN(ctx, s, orgID, 1)
}

// checkChannelLimitN rejects a request that would add n channels past the
// operator's cap. FOSS has no plan limits: with no cap configured every
// request passes.
func checkChannelLimitN(ctx context.Context, s store.Store, orgID string, n int) error {
	capN := limits.Current().MaxNotificationChannels
	if legacy := CurrentOperatorLimits().MaxNotificationChannelsPerOrg; legacy > capN {
		capN = legacy
	}
	if capN <= 0 {
		return nil
	}
	offset := capN - n
	if offset < 0 {
		return &limits.CapError{Resource: "notification channels", Max: capN, Env: limits.EnvMaxNotificationChannels}
	}
	rows, err := s.NotificationChannels().ListByOrg(ctx, orgID, capProbe(offset+1))
	if err != nil {
		return fmt.Errorf("failed to count notification channels")
	}
	if len(rows) > 0 {
		return &limits.CapError{Resource: "notification channels", Max: capN, Env: limits.EnvMaxNotificationChannels}
	}
	return nil
}
