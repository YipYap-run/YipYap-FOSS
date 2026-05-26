package handlers

import (
	"context"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func checkMonitorLimit(ctx context.Context, s store.Store, orgID string, org *domain.Org) error {
	monitors, err := s.Monitors().ListByOrg(ctx, orgID, store.MonitorFilter{ListParams: store.ListParams{Limit: domain.FreeMaxMonitors + 1}})
	if err != nil {
		return fmt.Errorf("failed to count monitors")
	}
	if len(monitors) >= domain.FreeMaxMonitors {
		return fmt.Errorf("free plan limited to %d monitors. Upgrade to add more", domain.FreeMaxMonitors)
	}
	return nil
}
