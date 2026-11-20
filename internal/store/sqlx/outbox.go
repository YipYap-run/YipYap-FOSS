package sqlx

import (
	"context"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// OutboxStore is a no-op stub in FOSS builds.
type OutboxStore struct{}

// NewOutboxStore returns a no-op OutboxStore for FOSS builds.
func NewOutboxStore(_ Queryable, _ Dialect) *OutboxStore { return &OutboxStore{} }

func (s *OutboxStore) Enqueue(_ context.Context, _, _ string) error { return nil }
func (s *OutboxStore) Claim(_ context.Context, _ string, _ int, _ time.Duration) ([]store.OutboxJob, error) {
	return nil, nil
}
func (s *OutboxStore) Complete(_ context.Context, _ string) error       { return nil }
func (s *OutboxStore) Fail(_ context.Context, _ string, _ int) error    { return nil }
func (s *OutboxStore) Cleanup(_ context.Context, _ time.Duration) error { return nil }
