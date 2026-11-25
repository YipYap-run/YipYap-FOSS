package sqlx

import (
	"context"
	"fmt"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
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

func (s *OIDCStore) ListLinksByUser(_ context.Context, _ string) ([]*domain.UserOIDCLink, error) {
	return nil, nil
}

func (s *OIDCStore) UnlinkUser(_ context.Context, _, _ string) error {
	return fmt.Errorf("OIDC requires Pro tier")
}

func (s *OIDCStore) CountLinksByConnection(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func (s *OIDCStore) CreateDomain(_ context.Context, _ *domain.OIDCDomain) error {
	return fmt.Errorf("OIDC requires Pro tier")
}

func (s *OIDCStore) GetDomain(_ context.Context, _ string) (*domain.OIDCDomain, error) {
	return nil, store.ErrOIDCDomainNotFound
}

func (s *OIDCStore) ListDomainsByConnection(_ context.Context, _ string) ([]*domain.OIDCDomain, error) {
	return nil, nil
}

func (s *OIDCStore) DeleteDomain(_ context.Context, _ string) error {
	return store.ErrOIDCDomainNotFound
}

func (s *OIDCStore) MarkDomainVerified(_ context.Context, _ string, _ time.Time) error {
	return fmt.Errorf("OIDC requires Pro tier")
}

func (s *OIDCStore) GetVerifiedDomain(_ context.Context, _ string) (*domain.OIDCDomain, error) {
	return nil, store.ErrOIDCDomainNotFound
}
