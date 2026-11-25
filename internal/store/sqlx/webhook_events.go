package sqlx

import (
	"context"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// WebhookEventStore is a no-op stub in FOSS builds.
type WebhookEventStore struct{}

// NewWebhookEventStore returns a no-op WebhookEventStore for FOSS builds.
func NewWebhookEventStore(_ Queryable, _ Dialect) *WebhookEventStore { return &WebhookEventStore{} }

func (s *WebhookEventStore) Claim(_ context.Context, _ string, _ time.Duration) (store.WebhookClaimResult, string, error) {
	return store.WebhookClaimed, "", nil
}

func (s *WebhookEventStore) Complete(_ context.Context, _, _ string, _ time.Duration) error {
	return nil
}

func (s *WebhookEventStore) Release(_ context.Context, _, _ string) error {
	return nil
}

func (s *WebhookEventStore) Cleanup(_ context.Context) error {
	return nil
}
