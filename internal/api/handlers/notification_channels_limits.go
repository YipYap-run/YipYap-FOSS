package handlers

import (
	"context"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

const freeMaxChannels = 5

func checkChannelLimit(ctx context.Context, s store.Store, orgID string) error {
	channels, err := s.NotificationChannels().ListByOrg(ctx, orgID, store.ListParams{Limit: freeMaxChannels + 1})
	if err != nil {
		return fmt.Errorf("failed to count notification channels")
	}
	if len(channels) >= freeMaxChannels {
		return fmt.Errorf("free plan limited to %d notification channels. Upgrade to add more", freeMaxChannels)
	}
	return nil
}
