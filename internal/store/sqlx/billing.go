package sqlx

import (
	"context"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// BillingStore is a no-op stub in FOSS builds.
type BillingStore struct{}

// NewBillingStore returns a no-op BillingStore for FOSS builds.
func NewBillingStore(_ Queryable, _ Dialect) *BillingStore { return &BillingStore{} }

func (s *BillingStore) Upsert(_ context.Context, _ *domain.Billing) error {
	return fmt.Errorf("billing requires paid tier")
}
func (s *BillingStore) ApplyCheckout(_ context.Context, _ *domain.Billing) (bool, error) {
	return false, fmt.Errorf("billing requires paid tier")
}
func (s *BillingStore) GetByOrgID(_ context.Context, _ string) (*domain.Billing, error) {
	return nil, fmt.Errorf("billing requires paid tier")
}
func (s *BillingStore) ListWithSubscription(_ context.Context) ([]*domain.Billing, error) {
	return nil, nil
}
func (s *BillingStore) UpdateStatus(_ context.Context, _, _ string) error        { return nil }
func (s *BillingStore) UpdateSeatCount(_ context.Context, _ string, _ int) error { return nil }
func (s *BillingStore) ResetUsagePeriod(_ context.Context, _ string) error       { return nil }
func (s *BillingStore) Delete(_ context.Context, _ string) error                 { return nil }
func (s *BillingStore) UpdateEmail(_ context.Context, _, _ string) error {
	return fmt.Errorf("billing requires paid tier")
}
func (s *BillingStore) EnsureCustomer(_ context.Context, _, _, _ string) (string, error) {
	return "", fmt.Errorf("billing requires paid tier")
}
func (s *BillingStore) GetCheckoutAttempt(_ context.Context, _ string) (*domain.CheckoutAttempt, error) {
	return nil, nil
}
func (s *BillingStore) SaveCheckoutAttempt(_ context.Context, _ *domain.CheckoutAttempt, _ int) (bool, error) {
	return false, fmt.Errorf("billing requires paid tier")
}
func (s *BillingStore) ClearCheckoutAttempt(_ context.Context, _ string) error { return nil }
