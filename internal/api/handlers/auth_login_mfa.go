package handlers

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func userHasMFA(_ context.Context, _ store.Store, _ *domain.User) (bool, []string) {
	return false, nil
}
