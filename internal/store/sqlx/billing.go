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
func (s *BillingStore) GetByOrgID(_ context.Context, _ string) (*domain.Billing, error) {
	return nil, fmt.Errorf("billing requires paid tier")
}
func (s *BillingStore) UpdateStatus(_ context.Context, _, _ string) error        { return nil }
func (s *BillingStore) UpdateSeatCount(_ context.Context, _ string, _ int) error { return nil }
func (s *BillingStore) IncrementSMSUsed(_ context.Context, _ string) error       { return nil }
func (s *BillingStore) IncrementVoiceUsed(_ context.Context, _ string) error     { return nil }
func (s *BillingStore) ResetSMSVoice(_ context.Context, _ string) error          { return nil }
func (s *BillingStore) Delete(_ context.Context, _ string) error                 { return nil }
