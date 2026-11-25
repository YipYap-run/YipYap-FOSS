package sqlite

import (
	"context"
	"testing"
	"time"
)

func TestRateBudgetFixedWindow(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	rb := s.RateBudget()
	now := time.Now().UTC().Truncate(time.Second)

	for i := 1; i <= 3; i++ {
		count, resetAt, err := rb.Take(ctx, "tickets:user-1", time.Minute, now)
		if err != nil {
			t.Fatal(err)
		}
		if count != i {
			t.Fatalf("take %d = %d, want %d", i, count, i)
		}
		if want := now.Add(time.Minute); !resetAt.Equal(want) {
			t.Errorf("reset at %s, want %s", resetAt, want)
		}
	}

	count, _, err := rb.Peek(ctx, "tickets:user-1", time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("peek = %d, want 3", count)
	}
	if count, _, err := rb.Peek(ctx, "tickets:user-2", time.Minute, now); err != nil || count != 0 {
		t.Errorf("other key peek = %d, %v, want 0", count, err)
	}

	count, _, err = rb.Take(ctx, "tickets:user-1", time.Minute, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("new window take = %d, want 1", count)
	}
	count, _, err = rb.Peek(ctx, "tickets:user-1", time.Minute, now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expired window peek = %d, want 0", count)
	}
}
