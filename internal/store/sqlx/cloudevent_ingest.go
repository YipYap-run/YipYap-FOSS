package sqlx

import (
	"context"
	"time"
)

// CloudEventIngestStore implements store.CloudEventIngestStore across SQL
// backends.
type CloudEventIngestStore struct {
	q Queryable
	d Dialect
}

// NewCloudEventIngestStore returns a CloudEventIngestStore bound to q.
func NewCloudEventIngestStore(q Queryable, d Dialect) *CloudEventIngestStore {
	return &CloudEventIngestStore{q: q, d: d}
}

// TrySeen atomically records (monitorID, eventID). Returns true when the pair
// is newly recorded (fresh insert) or when an existing row was older than ttl
// and got refreshed. Returns false when a row exists within the ttl window
// (duplicate).
func (s *CloudEventIngestStore) TrySeen(ctx context.Context, monitorID, eventID string, ttl time.Duration) (bool, error) {
	now := time.Now().UTC()
	cutoff := now.Add(-ttl)

	res, err := s.q.ExecContext(ctx, `
        INSERT INTO cloudevent_ingest_idempotency (monitor_id, event_id, seen_at)
        VALUES (?, ?, ?)
        ON CONFLICT(monitor_id, event_id) DO UPDATE
            SET seen_at = excluded.seen_at
            WHERE cloudevent_ingest_idempotency.seen_at < ?
    `, monitorID, eventID, s.d.MillisTimeArg(now), s.d.MillisTimeArg(cutoff))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	// n == 1 when a new row was inserted OR an existing stale row was refreshed.
	// n == 0 when a fresh duplicate hit the ON CONFLICT with the WHERE clause filtering.
	return n == 1, nil
}

// PruneOlderThan removes rows whose seen_at is older than now - ttl.
func (s *CloudEventIngestStore) PruneOlderThan(ctx context.Context, ttl time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-ttl)
	res, err := s.q.ExecContext(ctx,
		`DELETE FROM cloudevent_ingest_idempotency WHERE seen_at < ?`, s.d.MillisTimeArg(cutoff))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
