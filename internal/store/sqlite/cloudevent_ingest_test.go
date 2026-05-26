package sqlite

import (
	"context"
	"testing"
	"time"
)

// testInsertWithSeenAt is a test-only helper that inserts a row with a
// controlled seen_at, sidestepping the TrySeen atomicity so we can
// simulate aged rows without actually sleeping.
func (s *cloudEventIngestStore) testInsertWithSeenAt(ctx context.Context, monitorID, eventID string, seenAt time.Time) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO cloudevent_ingest_idempotency (monitor_id, event_id, seen_at) VALUES (?, ?, ?)`,
		monitorID, eventID, seenAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	)
	return err
}

func TestTrySeen_Insert(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	got, err := s.CloudEventIngest().TrySeen(ctx, "mon-1", "evt-1", time.Hour)
	if err != nil {
		t.Fatalf("TrySeen: %v", err)
	}
	if !got {
		t.Fatalf("expected true (fresh insert), got false")
	}
}

func TestTrySeen_Duplicate(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	first, err := s.CloudEventIngest().TrySeen(ctx, "mon-1", "evt-1", time.Hour)
	if err != nil {
		t.Fatalf("first TrySeen: %v", err)
	}
	if !first {
		t.Fatalf("first call: expected true, got false")
	}

	second, err := s.CloudEventIngest().TrySeen(ctx, "mon-1", "evt-1", time.Hour)
	if err != nil {
		t.Fatalf("second TrySeen: %v", err)
	}
	if second {
		t.Fatalf("second call: expected false (duplicate within ttl), got true")
	}
}

func TestTrySeen_RefreshesAfterTTL(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	ingest := s.CloudEventIngest().(*cloudEventIngestStore)

	// Seed a row with seen_at 2h ago.
	old := time.Now().UTC().Add(-2 * time.Hour)
	if err := ingest.testInsertWithSeenAt(ctx, "mon-1", "evt-1", old); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// ttl=1h means the 2h-old row is stale; TrySeen should refresh and return true.
	got, err := ingest.TrySeen(ctx, "mon-1", "evt-1", time.Hour)
	if err != nil {
		t.Fatalf("TrySeen: %v", err)
	}
	if !got {
		t.Fatalf("expected true (stale row refreshed), got false")
	}

	// Immediately after, a second call within the ttl should now see it as fresh duplicate.
	again, err := ingest.TrySeen(ctx, "mon-1", "evt-1", time.Hour)
	if err != nil {
		t.Fatalf("second TrySeen: %v", err)
	}
	if again {
		t.Fatalf("second call after refresh: expected false, got true")
	}
}

func TestPruneOlderThan(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	ingest := s.CloudEventIngest().(*cloudEventIngestStore)

	// Two fresh rows.
	if _, err := ingest.TrySeen(ctx, "mon-1", "evt-fresh-1", time.Hour); err != nil {
		t.Fatalf("insert fresh 1: %v", err)
	}
	if _, err := ingest.TrySeen(ctx, "mon-1", "evt-fresh-2", time.Hour); err != nil {
		t.Fatalf("insert fresh 2: %v", err)
	}

	// One stale row.
	old := time.Now().UTC().Add(-2 * time.Hour)
	if err := ingest.testInsertWithSeenAt(ctx, "mon-1", "evt-stale", old); err != nil {
		t.Fatalf("seed stale: %v", err)
	}

	deleted, err := ingest.PruneOlderThan(ctx, time.Hour)
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}

	// The two fresh rows are still fresh duplicates.
	got, err := ingest.TrySeen(ctx, "mon-1", "evt-fresh-1", time.Hour)
	if err != nil {
		t.Fatalf("TrySeen fresh-1: %v", err)
	}
	if got {
		t.Fatalf("fresh-1 expected false (still within ttl), got true")
	}

	// The stale row was pruned, TrySeen with the same key should now succeed as a fresh insert.
	got, err = ingest.TrySeen(ctx, "mon-1", "evt-stale", time.Hour)
	if err != nil {
		t.Fatalf("TrySeen stale after prune: %v", err)
	}
	if !got {
		t.Fatalf("stale (pruned) expected true, got false")
	}
}
