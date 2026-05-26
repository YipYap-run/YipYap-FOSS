package reply

import (
	"strconv"
	"testing"
	"time"
)

func TestRateLimiter_Allow(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	rl := newRateLimiter(func() time.Time { return now })

	for i := 0; i < 5; i++ {
		if !rl.Allow("org-1", "type-x", 10) {
			t.Fatalf("call %d should be allowed", i+1)
		}
	}
}

func TestRateLimiter_Reject(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	rl := newRateLimiter(func() time.Time { return now })

	for i := 0; i < 3; i++ {
		if !rl.Allow("org-1", "type-x", 3) {
			t.Fatalf("call %d should be allowed", i+1)
		}
	}
	if rl.Allow("org-1", "type-x", 3) {
		t.Fatalf("4th call should be rejected")
	}
}

func TestRateLimiter_WindowReset(t *testing.T) {
	// Align start to a window boundary so the sliding-window math has
	// clean numbers to reason about.
	cur := time.Unix(1_700_000_000, 0).UTC().Truncate(rateLimiterWindowSize)
	rl := newRateLimiter(func() time.Time { return cur })

	for i := 0; i < 2; i++ {
		if !rl.Allow("org-1", "type-x", 2) {
			t.Fatalf("call %d should be allowed", i+1)
		}
	}
	if rl.Allow("org-1", "type-x", 2) {
		t.Fatalf("over limit inside window should reject")
	}

	// Advance two full windows so the sliding-window tail from the
	// original window ages out completely. A 61-second advance would
	// still have ~98% of the original window's tail bleeding into the
	// new window, that's the whole point of the sliding counter
	// compared to the fixed one.
	cur = cur.Add(2 * rateLimiterWindowSize)
	if !rl.Allow("org-1", "type-x", 2) {
		t.Fatalf("after full sliding-window flush, should be allowed")
	}
}

// TestRateLimiter_SlidingWindow_BoundaryBurst is the A4-N-9 regression:
// a fixed-window counter would let 2*limit admissions through a burst
// straddling the window boundary (last second of window N + first
// second of window N+1). Sliding-window math blocks that burst.
func TestRateLimiter_SlidingWindow_BoundaryBurst(t *testing.T) {
	cur := time.Unix(1_700_000_000, 0).UTC().Truncate(rateLimiterWindowSize)
	rl := newRateLimiter(func() time.Time { return cur })
	defer rl.Close()

	// Fill the first window to the limit.
	for i := 0; i < 4; i++ {
		if !rl.Allow("org-1", "t", 4) {
			t.Fatalf("seed call %d should be allowed", i+1)
		}
	}
	// Advance 1 second into the NEXT window. Fixed-window would allow
	// another 4 admissions here; sliding-window blends 4 * (59/60) from
	// the prior window and rejects.
	cur = cur.Add(rateLimiterWindowSize + time.Second)
	if rl.Allow("org-1", "t", 4) {
		t.Fatalf("sliding-window must reject the boundary burst")
	}
}

func TestRateLimiter_PerKeyBuckets(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	rl := newRateLimiter(func() time.Time { return now })

	for i := 0; i < 2; i++ {
		if !rl.Allow("org-A", "t", 2) {
			t.Fatalf("org-A call %d should be allowed", i+1)
		}
	}
	if rl.Allow("org-A", "t", 2) {
		t.Fatalf("org-A should be limited")
	}
	if !rl.Allow("org-B", "t", 2) {
		t.Fatalf("org-B must not share org-A's bucket")
	}
	if !rl.Allow("org-A", "other", 2) {
		t.Fatalf("type split must split bucket")
	}
}

func TestRateLimiter_DefaultLimit(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	rl := newRateLimiter(func() time.Time { return now })
	defer rl.Close()

	for i := 0; i < 60; i++ {
		if !rl.Allow("org-1", "t", 0) {
			t.Fatalf("default limit should allow 60 calls; failed at %d", i+1)
		}
	}
	if rl.Allow("org-1", "t", 0) {
		t.Fatalf("61st call at default limit should reject")
	}
}

// TestRateLimiter_BucketCap_CapsAtMax is the H-13 regression. Prior to
// the cap, an attacker churning distinct reply_type values pinned
// unbounded (org, type) buckets. We push 20k unique buckets and assert
// the map never exceeds rateLimiterMaxBuckets.
func TestRateLimiter_BucketCap_CapsAtMax(t *testing.T) {
	// Freeze the clock so LRU eviction sees identical windowStart and
	// can still find "an oldest" deterministically.
	cur := time.Unix(1_700_000_000, 0).UTC()
	tick := func() time.Time {
		cur = cur.Add(time.Millisecond)
		return cur
	}
	rl := newRateLimiter(tick)
	defer rl.Close()

	const n = 20_000
	for i := 0; i < n; i++ {
		rl.Allow("org-1", "t-"+strconv.Itoa(i), 100)
	}

	if got := rl.size(); got > rateLimiterMaxBuckets {
		t.Fatalf("bucket map grew past cap: size=%d max=%d", got, rateLimiterMaxBuckets)
	}
}

// TestRateLimiter_SweepOnce_EvictsStaleBuckets confirms the janitor
// actually drops idle buckets. Without the sweep, a bucket visited once
// and abandoned would live until process exit.
func TestRateLimiter_SweepOnce_EvictsStaleBuckets(t *testing.T) {
	cur := time.Unix(1_700_000_000, 0).UTC()
	rl := newRateLimiter(func() time.Time { return cur })
	defer rl.Close()

	for i := 0; i < 50; i++ {
		rl.Allow("org-1", "t-"+strconv.Itoa(i), 100)
	}
	if got := rl.size(); got != 50 {
		t.Fatalf("seed size = %d, want 50", got)
	}

	// Advance past the grace period.
	cur = cur.Add(rateLimiterWindowGrace + time.Second)
	rl.sweepOnce()

	if got := rl.size(); got != 0 {
		t.Fatalf("after sweep size = %d, want 0", got)
	}
}
