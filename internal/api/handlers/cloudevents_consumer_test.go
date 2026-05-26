package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
)

// TestCEConsumerCache_PerOrgCapRejects_H15 exercises the per-org
// consumer cap. Without the cap, an attacker with a single API key
// could pin unbounded (org, filter) consumers by varying the filter
// query string, 3,000 in agent-4's stress probe.
func TestCEConsumerCache_PerOrgCapRejects_H15(t *testing.T) {
	b := bus.NewChannel()
	t.Cleanup(func() { _ = b.Close() })
	c := newCEConsumerCache(b)
	t.Cleanup(c.Close)

	c.maxOrg = 5

	for i := 0; i < 5; i++ {
		if _, err := c.ensure(context.Background(), "org-1", "filter-"+itoa(i)); err != nil {
			t.Fatalf("ensure[%d]: %v", i, err)
		}
	}
	// 6th must be rejected.
	if _, err := c.ensure(context.Background(), "org-1", "filter-5"); !errors.Is(err, ErrConsumerCapExceeded) {
		t.Fatalf("6th ensure: want ErrConsumerCapExceeded, got %v", err)
	}

	// Different org still works.
	if _, err := c.ensure(context.Background(), "org-2", "filter-0"); err != nil {
		t.Fatalf("other-org ensure: %v", err)
	}

	// And re-ensure of a cached filter is a no-op for the cap.
	if _, err := c.ensure(context.Background(), "org-1", "filter-0"); err != nil {
		t.Fatalf("re-ensure cached filter: %v", err)
	}
}

// TestCEConsumerCache_IdleEviction_H15 confirms the sweeper reaps
// consumers idle longer than idleTTL. cap-freed slots then let the org
// open new consumers again.
func TestCEConsumerCache_IdleEviction_H15(t *testing.T) {
	b := bus.NewChannel()
	t.Cleanup(func() { _ = b.Close() })
	c := newCEConsumerCache(b)
	t.Cleanup(c.Close)

	// Inject a fake clock so we can drive the sweep deterministically.
	cur := time.Unix(1_700_000_000, 0).UTC()
	c.clock = func() time.Time { return cur }
	c.idleTTL = time.Minute
	c.maxOrg = 3

	// Seed 3 consumers.
	for i := 0; i < 3; i++ {
		if _, err := c.ensure(context.Background(), "org-x", "f-"+itoa(i)); err != nil {
			t.Fatalf("seed ensure[%d]: %v", i, err)
		}
	}
	// Cap is full.
	if _, err := c.ensure(context.Background(), "org-x", "f-overflow"); !errors.Is(err, ErrConsumerCapExceeded) {
		t.Fatalf("pre-sweep overflow: want cap error, got %v", err)
	}

	// Advance past idleTTL and force a sweep.
	cur = cur.Add(2 * time.Minute)
	c.sweepOnce()

	if got := c.size(); got != 0 {
		t.Fatalf("post-sweep size = %d, want 0 (all idle)", got)
	}

	// Cap freed; new ensures succeed.
	for i := 0; i < 3; i++ {
		if _, err := c.ensure(context.Background(), "org-x", "fresh-"+itoa(i)); err != nil {
			t.Fatalf("post-sweep ensure[%d]: %v", i, err)
		}
	}
}

// TestCEConsumerCache_TouchDefersEviction confirms that a poll/stream
// hit within idleTTL resets the idle timer and preserves the consumer.
func TestCEConsumerCache_TouchDefersEviction(t *testing.T) {
	b := bus.NewChannel()
	t.Cleanup(func() { _ = b.Close() })
	c := newCEConsumerCache(b)
	t.Cleanup(c.Close)

	cur := time.Unix(1_700_000_000, 0).UTC()
	c.clock = func() time.Time { return cur }
	c.idleTTL = time.Minute

	if _, err := c.ensure(context.Background(), "org-x", "f-keep"); err != nil {
		t.Fatalf("seed ensure: %v", err)
	}

	// Tick forward 30s, touch, then advance another 30s. Still under
	// idleTTL relative to the touch.
	cur = cur.Add(30 * time.Second)
	if _, err := c.ensure(context.Background(), "org-x", "f-keep"); err != nil {
		t.Fatalf("touch ensure: %v", err)
	}
	cur = cur.Add(40 * time.Second)
	c.sweepOnce()

	if c.size() != 1 {
		t.Fatalf("after touch + sub-TTL sweep, size = %d, want 1", c.size())
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// TestCanonicalFilter verifies the (org, filter) cache identity stays stable
// across equivalent filter sets: order-independent, empties dropped, trimmed.
// Equivalent sets must map to the same canonical string (and thus the same
// consumer name); genuinely different sets must not collide.
func TestCanonicalFilter(t *testing.T) {
	tests := []struct {
		name string
		a    []string
		b    []string
		same bool
	}{
		{"order independent", []string{"a.*", "b.*"}, []string{"b.*", "a.*"}, true},
		{"empties dropped", []string{"a.*", "", "b.*"}, []string{"b.*", "a.*"}, true},
		{"whitespace trimmed", []string{" a.* ", "b.*"}, []string{"a.*", " b.* "}, true},
		{"single equals itself", []string{"a.*"}, []string{"a.*"}, true},
		{"nil equals empty", nil, []string{}, true},
		{"nil equals all-empty", nil, []string{"", "  "}, true},
		{"different sets differ", []string{"a.*"}, []string{"b.*"}, false},
		{"subset differs", []string{"a.*"}, []string{"a.*", "b.*"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ca := canonicalFilter(tc.a)
			cb := canonicalFilter(tc.b)
			if tc.same && ca != cb {
				t.Fatalf("want same canonical, got %q vs %q", ca, cb)
			}
			if !tc.same && ca == cb {
				t.Fatalf("want different canonical, both %q", ca)
			}
			// The cache key derives from the canonical string, so equivalent
			// sets must also yield the same consumer name.
			na := ceConsumerName("org-1", ca)
			nb := ceConsumerName("org-1", cb)
			if tc.same && na != nb {
				t.Fatalf("want same consumer name, got %q vs %q", na, nb)
			}
			if !tc.same && na == nb {
				t.Fatalf("want different consumer name, both %q", na)
			}
		})
	}

	// No filters must canonicalize to the empty string so the "no filtering"
	// path and the legacy single-empty-filter path share one identity.
	if got := canonicalFilter(nil); got != "" {
		t.Fatalf("no filters: want empty canonical, got %q", got)
	}
}
