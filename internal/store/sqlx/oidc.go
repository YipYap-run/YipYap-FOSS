package sqlx

import (
	"context"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// OIDCStore is a no-op stub in FOSS builds.
type OIDCStore struct{}

// NewOIDCStore returns a no-op OIDCStore for FOSS builds.
func NewOIDCStore(_ Queryable, _ Dialect) *OIDCStore { return &OIDCStore{} }

func (s *OIDCStore) CreateConnection(_ context.Context, _ *domain.OIDCConnection) error {
	return fmt.Errorf("OIDC requires Pro tier")
}

func (s *OIDCStore) GetConnection(_ context.Context, _ string) (*domain.OIDCConnection, error) {
	return nil, fmt.Errorf("OIDC requires Pro tier")
}

func (s *OIDCStore) ListConnectionsByOrg(_ context.Context, _ string) ([]*domain.OIDCConnection, error) {
	return nil, nil
}

func (s *OIDCStore) UpdateConnection(_ context.Context, _ *domain.OIDCConnection) error {
	return fmt.Errorf("OIDC requires Pro tier")
}

func (s *OIDCStore) DeleteConnection(_ context.Context, _ string) error {
	return fmt.Errorf("OIDC requires Pro tier")
}

func (s *OIDCStore) LinkUser(_ context.Context, _ *domain.UserOIDCLink) error {
	return fmt.Errorf("OIDC requires Pro tier")
}

func (s *OIDCStore) GetUserByOIDC(_ context.Context, _, _ string) (*domain.User, error) {
	return nil, fmt.Errorf("OIDC requires Pro tier")
}

func (s *OIDCStore) ListAllEnabled(_ context.Context) ([]*domain.OIDCConnection, error) {
	return nil, nil
}
