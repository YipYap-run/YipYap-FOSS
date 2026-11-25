package sqlite

import (
	"context"
	"testing"
	"time"
)

func TestCloudEventPositions_RoundTrip(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	positions := s.CloudEventPositions()

	if _, ok, err := positions.Get(ctx, "org-1", "key-1"); err != nil || ok {
		t.Fatalf("missing row = ok %v, err %v", ok, err)
	}

	at := time.Now().UTC().Truncate(time.Millisecond)
	if err := positions.Set(ctx, "org-1", "key-1", "cursor-a", at); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := positions.Get(ctx, "org-1", "key-1")
	if err != nil || !ok || got != "cursor-a" {
		t.Fatalf("Get = %q ok %v err %v", got, ok, err)
	}

	later := at.Add(time.Minute)
	if err := positions.Set(ctx, "org-1", "key-1", "cursor-b", later); err != nil {
		t.Fatalf("Set upsert: %v", err)
	}
	if got, _, _ := positions.Get(ctx, "org-1", "key-1"); got != "cursor-b" {
		t.Fatalf("upsert = %q, want cursor-b", got)
	}

	if err := positions.Set(ctx, "org-2", "key-1", "cursor-c", later); err != nil {
		t.Fatalf("Set other org: %v", err)
	}
	if got, _, _ := positions.Get(ctx, "org-2", "key-1"); got != "cursor-c" {
		t.Fatalf("other org = %q, want cursor-c", got)
	}
	if got, _, _ := positions.Get(ctx, "org-1", "key-1"); got != "cursor-b" {
		t.Fatalf("org-1 after org-2 write = %q, want cursor-b", got)
	}
}

func TestCloudEventPositions_PruneBefore(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	positions := s.CloudEventPositions()

	now := time.Now().UTC().Truncate(time.Millisecond)
	for key, at := range map[string]time.Time{
		"old":  now.Add(-48 * time.Hour),
		"edge": now.Add(-2 * time.Hour),
		"new":  now,
	} {
		if err := positions.Set(ctx, "org-1", key, "cursor-"+key, at); err != nil {
			t.Fatalf("Set %s: %v", key, err)
		}
	}

	n, err := positions.PruneBefore(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("PruneBefore: %v", err)
	}
	if n != 1 {
		t.Fatalf("pruned %d rows, want 1", n)
	}
	if _, ok, _ := positions.Get(ctx, "org-1", "old"); ok {
		t.Fatal("expired row survived pruning")
	}
	for _, key := range []string{"edge", "new"} {
		if _, ok, _ := positions.Get(ctx, "org-1", key); !ok {
			t.Fatalf("pruned recent row %q", key)
		}
	}
}

func TestCloudEventPositions_TrimOrg(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	positions := s.CloudEventPositions()

	base := time.Now().UTC().Truncate(time.Millisecond)
	for i := 0; i < 5; i++ {
		if err := positions.Set(ctx, "org-1", "key-"+string(rune('a'+i)), "cursor", base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("Set %d: %v", i, err)
		}
	}
	if err := positions.Set(ctx, "org-2", "keep-me", "cursor", base); err != nil {
		t.Fatalf("Set other org: %v", err)
	}

	n, err := positions.TrimOrg(ctx, "org-1", 3)
	if err != nil {
		t.Fatalf("TrimOrg: %v", err)
	}
	if n != 2 {
		t.Fatalf("trimmed %d rows, want 2", n)
	}
	for _, key := range []string{"key-a", "key-b"} {
		if _, ok, _ := positions.Get(ctx, "org-1", key); ok {
			t.Fatalf("oldest row %q survived the cap", key)
		}
	}
	for _, key := range []string{"key-c", "key-d", "key-e"} {
		if _, ok, _ := positions.Get(ctx, "org-1", key); !ok {
			t.Fatalf("recent row %q was trimmed", key)
		}
	}
	if _, ok, _ := positions.Get(ctx, "org-2", "keep-me"); !ok {
		t.Fatal("trimming org-1 removed org-2's row")
	}
}
