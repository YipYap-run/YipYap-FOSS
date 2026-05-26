package sqlite

import (
	"context"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// outboxStore is a no-op stub in FOSS builds.
type outboxStore struct{ q queryable }

func (s *outboxStore) Enqueue(_ context.Context, _, _ string) error                           { return nil }
func (s *outboxStore) Claim(_ context.Context, _ string, _ int, _ time.Duration) ([]store.OutboxJob, error) { return nil, nil }
func (s *outboxStore) Complete(_ context.Context, _ string) error                              { return nil }
func (s *outboxStore) Fail(_ context.Context, _ string, _ int) error                           { return nil }
func (s *outboxStore) Cleanup(_ context.Context, _ time.Duration) error                        { return nil }
