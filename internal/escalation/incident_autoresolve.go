package escalation

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// maybeAutoResolveIncident is a no-op in FOSS builds.
func (e *Engine) maybeAutoResolveIncident(_ context.Context, _ *domain.Alert) {}
