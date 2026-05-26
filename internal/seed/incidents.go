package seed

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func seedIncidents(_ context.Context, _ store.Store, _ *domain.Org, _ []*domain.Monitor, _ ...*domain.User) error {
	return nil
}
