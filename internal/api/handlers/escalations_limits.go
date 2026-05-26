package handlers

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func checkEscalationPolicyLimit(_ context.Context, _ store.Store, _ string) error {
	return nil
}
