package store

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// MonitorStateStore manages custom monitor states for an organization.
type MonitorStateStore interface {
	Create(ctx context.Context, s *domain.MonitorState) error
	GetByID(ctx context.Context, id string) (*domain.MonitorState, error)
	ListByOrg(ctx context.Context, orgID string) ([]*domain.MonitorState, error)
	Update(ctx context.Context, s *domain.MonitorState) error
	Delete(ctx context.Context, id string) error
	SeedBuiltins(ctx context.Context, orgID string) error
}

// MonitorStateProvider is implemented by concrete store types that support
// custom monitor states. Handler code uses a type assertion to obtain the sub-store.
type MonitorStateProvider interface {
	MonitorStates() MonitorStateStore
}

// MonitorMatchRuleStore manages match rules that map check conditions to states.
type MonitorMatchRuleStore interface {
	ListByMonitor(ctx context.Context, monitorID string) ([]*domain.MonitorMatchRule, error)
	ReplaceForMonitor(ctx context.Context, monitorID string, rules []*domain.MonitorMatchRule) error
}

// MonitorMatchRuleProvider is implemented by concrete store types that support
// monitor match rules.
type MonitorMatchRuleProvider interface {
	MonitorMatchRules() MonitorMatchRuleStore
}
