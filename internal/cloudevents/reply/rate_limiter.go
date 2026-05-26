package reply

import (
	"container/list"
	"sync"
	"time"
)

const defaultRateLimitPerMinute = 60

// rateLimiterWindowSize is the counting window. One minute matches the
// advertised per-tier caps (30/min, 60/min, etc.) and the config UI.
const rateLimiterWindowSize = time.Minute

// rateLimiterMaxBuckets is the hard cap on bucket-map size. Agent-4's
// stress probe exhibited 17,576 keys at zero evictions; a 10k cap with
// LRU eviction means a churning attacker still cannot pin unbounded
// memory.
const rateLimiterMaxBuckets = 10_000

// rateLimiterSweepInterval is how often the background janitor wakes up
// to drop buckets idle longer than rateLimiterWindowGrace.
const rateLimiterSweepInterval = time.Minute

// rateLimiterWindowGrace is how long an unvisited bucket lives past its
// current window-start before being eligible for eviction. Two windows
// gives a comfortable margin past the counting window so a bucket the
// caller might still return to is preserved across one sweep.
const rateLimiterWindowGrace = 2 * rateLimiterWindowSize

// rateLimiter is a sliding-window counter keyed by (orgID, replyType)
// (A4-N-9). The decision combines the current window's count with a
// time-weighted fraction of the previous window's count, so a burst
// straddling the boundary (59th second of window N + 1st second of
// window N+1) cannot momentarily double the advertised limit the way
// a plain fixed-window counter would.
//
// Bucket-map size is capped at rateLimiterMaxBuckets with true LRU
// eviction (A4-N-4). A churning attacker still cannot pin unbounded
// memory.
//
// rateLimiter is safe for concurrent use.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*list.Element // value = *bucket
	lru     *list.List
	clock   func() time.Time

	stop chan struct{}
	done chan struct{}
}

type bucket struct {
	key string
	// windowStart is the start of the CURRENT window this bucket is
	// counting into, always a multiple of rateLimiterWindowSize
	// relative to the zero time.
	windowStart time.Time
	// count is the number of admitted calls so far in the current
	// window.
	count int
	// prevCount is the admitted count from the immediately previous
	// window, used by the sliding-window-counter decision to blend in
	// the tail of the prior window when evaluating a call near the
	// window boundary.
	prevCount int
}

func newRateLimiter(clock func() time.Time) *rateLimiter {
	if clock == nil {
		clock = time.Now
	}
	r := &rateLimiter{
		buckets: make(map[string]*list.Element),
		lru:     list.New(),
		clock:   clock,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go r.sweepLoop(rateLimiterSweepInterval)
	return r
}

// Close stops the background sweep goroutine. Idempotent. Intended for
// tests and clean dispatcher shutdown.
func (r *rateLimiter) Close() {
	if r == nil {
		return
	}
	select {
	case <-r.stop:
		return
	default:
		close(r.stop)
	}
	<-r.done
}

// Allow reports whether a call from (orgID, replyType) may proceed under
// the provided per-minute limit. A zero or negative limit uses the default
// (60/minute).
//
// The decision uses a sliding-window counter: the rejected-vs-allowed
// verdict accounts for both the current window's admitted count AND a
// time-weighted fraction of the previous window, so a burst that
// straddles the boundary cannot exceed the advertised limit by up to 2x
// the way a plain fixed-window counter allowed.
func (r *rateLimiter) Allow(orgID, replyType string, limit int) bool {
	if limit <= 0 {
		limit = defaultRateLimitPerMinute
	}
	key := orgID + "|" + replyType
	now := r.clock()
	alignedWindow := now.Truncate(rateLimiterWindowSize)

	r.mu.Lock()
	defer r.mu.Unlock()

	var b *bucket
	if elem, ok := r.buckets[key]; ok {
		r.lru.MoveToFront(elem)
		b = elem.Value.(*bucket)
		// Roll the window forward if needed.
		switch {
		case alignedWindow.Equal(b.windowStart):
			// Same window; nothing to roll.
		case alignedWindow.Sub(b.windowStart) == rateLimiterWindowSize:
			// Adjacent window: prev := current, current := 0.
			b.prevCount = b.count
			b.count = 0
			b.windowStart = alignedWindow
		default:
			// Older than the previous window: both counters age out.
			b.prevCount = 0
			b.count = 0
			b.windowStart = alignedWindow
		}
	} else {
		// New bucket. Enforce the cap by dropping the tail (least-recently
		// used) before insert. O(1) with the linked list.
		if r.lru.Len() >= rateLimiterMaxBuckets {
			r.evictOldestLocked()
		}
		b = &bucket{key: key, windowStart: alignedWindow}
		elem := r.lru.PushFront(b)
		r.buckets[key] = elem
	}

	// Sliding-window decision, scaled by windowSize nanoseconds so
	// integer math keeps the precision the float form would otherwise
	// round off at the boundary. effective = count*W + prev*(W-elapsed).
	// Compare against limit*W; a call is admitted iff adding one full
	// unit (+W) keeps the total within limit*W.
	elapsed := now.Sub(b.windowStart)
	if elapsed < 0 {
		// Paranoid clamp, clock skew from an injected test clock can
		// push now ahead of alignedWindow in pathological ordering.
		elapsed = 0
	} else if elapsed > rateLimiterWindowSize {
		elapsed = rateLimiterWindowSize
	}
	windowNs := int64(rateLimiterWindowSize)
	scaledEffective := int64(b.count)*windowNs + int64(b.prevCount)*(windowNs-int64(elapsed))
	scaledLimit := int64(limit) * windowNs
	if scaledEffective+windowNs > scaledLimit {
		return false
	}
	b.count++
	return true
}

// evictOldestLocked removes the LRU-tail bucket. Caller holds r.mu.
func (r *rateLimiter) evictOldestLocked() {
	tail := r.lru.Back()
	if tail == nil {
		return
	}
	b := tail.Value.(*bucket)
	delete(r.buckets, b.key)
	r.lru.Remove(tail)
}

// sweepLoop runs until stop is closed, periodically dropping buckets
// idle longer than rateLimiterWindowGrace.
func (r *rateLimiter) sweepLoop(interval time.Duration) {
	defer close(r.done)
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-t.C:
			r.sweepOnce()
		}
	}
}

// sweepOnce drops every bucket whose window-start is older than the
// grace period.
func (r *rateLimiter) sweepOnce() {
	now := r.clock()
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, elem := range r.buckets {
		b := elem.Value.(*bucket)
		if now.Sub(b.windowStart) >= rateLimiterWindowGrace {
			delete(r.buckets, k)
			r.lru.Remove(elem)
		}
	}
}

// size returns the current bucket count. Package-private helper for
// tests.
func (r *rateLimiter) size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.buckets)
}
