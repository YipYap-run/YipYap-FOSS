package handlers

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// verifyMFAIfEnabled is a no-op in FOSS builds.
func verifyMFAIfEnabled(_ context.Context, _ store.Store, _ *domain.User, _ string) error {
	return nil
}
