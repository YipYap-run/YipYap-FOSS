package sqlite

import (
	"context"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

type oidcStore struct{ q queryable }

func (s *oidcStore) CreateConnection(_ context.Context, _ *domain.OIDCConnection) error {
	return fmt.Errorf("OIDC requires Pro tier")
}

func (s *oidcStore) GetConnection(_ context.Context, _ string) (*domain.OIDCConnection, error) {
	return nil, fmt.Errorf("OIDC requires Pro tier")
}

func (s *oidcStore) ListConnectionsByOrg(_ context.Context, _ string) ([]*domain.OIDCConnection, error) {
	return nil, nil
}

func (s *oidcStore) UpdateConnection(_ context.Context, _ *domain.OIDCConnection) error {
	return fmt.Errorf("OIDC requires Pro tier")
}

func (s *oidcStore) DeleteConnection(_ context.Context, _ string) error {
	return fmt.Errorf("OIDC requires Pro tier")
}

func (s *oidcStore) LinkUser(_ context.Context, _ *domain.UserOIDCLink) error {
	return fmt.Errorf("OIDC requires Pro tier")
}

func (s *oidcStore) GetUserByOIDC(_ context.Context, _, _ string) (*domain.User, error) {
	return nil, fmt.Errorf("OIDC requires Pro tier")
}

func (s *oidcStore) ListAllEnabled(_ context.Context) ([]*domain.OIDCConnection, error) {
	return nil, nil
}
