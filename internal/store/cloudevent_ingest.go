package store

import (
	"context"
	"time"
)

// CloudEventIngest records accepted inbound CloudEvent IDs per monitor so
// that duplicate deliveries are no-ops. Rows are pruned by a background
// janitor after `ttl` has elapsed since `seen_at`.
type CloudEventIngest interface {
	// TrySeen records (monitorID, eventID) atomically and returns true if
	// this is the first time the pair has been seen within the retention
	// window. Returns false without error if the pair was already recorded
	// within `ttl` of now. Returns an error only for actual storage
	// failures, not for duplicates.
	TrySeen(ctx context.Context, monitorID, eventID string, ttl time.Duration) (bool, error)

	// PruneOlderThan removes rows with `seen_at < now - ttl`. Returns the
	// number of rows deleted. Called by a background janitor; invoking it
	// manually from tests is supported.
	PruneOlderThan(ctx context.Context, ttl time.Duration) (int64, error)
}
