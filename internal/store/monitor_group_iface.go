package store

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// MonitorGroupStore manages monitor group CRUD.
type MonitorGroupStore interface {
	Create(ctx context.Context, g *domain.MonitorGroup) error
	GetByID(ctx context.Context, id string) (*domain.MonitorGroup, error)
	ListByOrg(ctx context.Context, orgID string) ([]*domain.MonitorGroup, error)
	Update(ctx context.Context, g *domain.MonitorGroup) error
	Delete(ctx context.Context, id string) error
}

// MonitorGroupProvider is implemented by concrete store types that support
// monitor groups. Handler code uses a type assertion to obtain the sub-store.
type MonitorGroupProvider interface {
	MonitorGroups() MonitorGroupStore
}
