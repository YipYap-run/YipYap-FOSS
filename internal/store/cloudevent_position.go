package store

import (
	"context"
	"time"
)

type CloudEventPositionStore interface {
	Get(ctx context.Context, orgID, key string) (string, bool, error)
	Set(ctx context.Context, orgID, key, cursor string, at time.Time) error
	TrimOrg(ctx context.Context, orgID string, keep int) (int64, error)
	PruneBefore(ctx context.Context, before time.Time) (int64, error)
}
