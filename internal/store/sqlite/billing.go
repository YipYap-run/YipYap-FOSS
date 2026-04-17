package sqlite

import (
	"context"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

type billingStore struct{ q queryable }

func (s *billingStore) Upsert(_ context.Context, _ *domain.Billing) error              { return fmt.Errorf("billing requires paid tier") }
func (s *billingStore) GetByOrgID(_ context.Context, _ string) (*domain.Billing, error) { return nil, fmt.Errorf("billing requires paid tier") }
func (s *billingStore) UpdateStatus(_ context.Context, _, _ string) error               { return nil }
func (s *billingStore) UpdateSeatCount(_ context.Context, _ string, _ int) error        { return nil }
func (s *billingStore) IncrementSMSUsed(_ context.Context, _ string) error              { return nil }
func (s *billingStore) IncrementVoiceUsed(_ context.Context, _ string) error            { return nil }
func (s *billingStore) ResetSMSVoice(_ context.Context, _ string) error                 { return nil }
func (s *billingStore) Delete(_ context.Context, _ string) error                        { return nil }
