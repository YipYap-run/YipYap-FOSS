package sqlx

import (
	"context"
	"time"
)

// DedupStore is a no-op stub in FOSS builds.
type DedupStore struct{}

// NewDedupStore returns a no-op DedupStore for FOSS builds.
func NewDedupStore(_ Queryable, _ Dialect) *DedupStore { return &DedupStore{} }

func (s *DedupStore) Claim(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return true, nil
}
func (s *DedupStore) Cleanup(_ context.Context) error { return nil }
