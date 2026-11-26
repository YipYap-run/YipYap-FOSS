package handlers

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// The self-hosted edition has no platform chat apps: DMs send through an
// org channel's own bot only.
func appContactIntegrations(context.Context, store.Store, string, bool, bool) ([]contactIntegration, error) {
	return []contactIntegration{}, nil
}
