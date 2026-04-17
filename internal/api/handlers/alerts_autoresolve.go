package handlers

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// maybeAutoResolveIncident is a no-op in FOSS builds.
func (h *AlertHandler) maybeAutoResolveIncident(_ context.Context, _ *domain.Alert) {}
