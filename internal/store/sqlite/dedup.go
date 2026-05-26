package sqlite

import (
	"context"
	"time"
)

// dedupStore is a no-op stub in FOSS builds.
type dedupStore struct{ q queryable }

func (s *dedupStore) Claim(_ context.Context, _ string, _ time.Duration) (bool, error) { return true, nil }
func (s *dedupStore) Cleanup(_ context.Context) error                                   { return nil }
