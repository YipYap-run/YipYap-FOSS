package handlers

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// validateEscalationPolicy has no plan gates in the self-hosted build:
// looping policies are allowed, and the engine honors MaxLoops.
func validateEscalationPolicy(_ context.Context, _ store.Store, _ string, _ *domain.EscalationPolicy) error {
	return nil
}
