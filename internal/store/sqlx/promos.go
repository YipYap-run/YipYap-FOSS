package sqlx

import (
	"context"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// PromoStore is a no-op stub in FOSS builds, which have no promo plan.
type PromoStore struct{}

// NewPromoStore returns a no-op PromoStore for FOSS builds.
func NewPromoStore(_ Queryable, _ Dialect) *PromoStore { return &PromoStore{} }

func (s *PromoStore) ListActive(_ context.Context) ([]*domain.Org, error) { return nil, nil }

func (s *PromoStore) DowngradeExpired(_ context.Context, _, _ string, _ time.Time) (bool, error) {
	return false, nil
}

func (s *PromoStore) ClaimWarning(_ context.Context, _, _ string, _ int, _ time.Time) (bool, error) {
	return false, nil
}

func (s *PromoStore) ReleaseWarning(_ context.Context, _, _ string, _ int) error { return nil }
