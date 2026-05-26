package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	cepub "github.com/YipYap-run/YipYap-FOSS/internal/cloudevents"
)

// ceConsumerBufferSize is the per-consumer buffer depth. Messages arriving
// when the buffer is full are dropped from the channel's perspective, on
// JetStream they'll be redelivered via the pull-consumer's retry machinery;
// on the in-memory bus they're simply lost, which is acceptable for FOSS
// single-box deployments where the ContainerSource isn't expected to be
// absent for long.
const ceConsumerBufferSize = 1024

// DefaultCEConsumerMaxPerOrg caps the number of distinct (org, filter)
// consumers a single org can pin concurrently. Without a cap an
// authenticated caller could churn filter query-string values to pin an
// unbounded set of long-lived goroutines + 1024-slot channels (H-15).
const DefaultCEConsumerMaxPerOrg = 16

// DefaultCEConsumerIdleTTL is how long a consumer with no poll/stream
// access survives before the sweeper reaps it. After eviction the
// downstream buf channel is closed; new polls create a fresh consumer.
const DefaultCEConsumerIdleTTL = 5 * time.Minute

// DefaultCEConsumerSweepInterval is the janitor wake-up cadence.
const DefaultCEConsumerSweepInterval = time.Minute

// ErrConsumerCapExceeded is returned by ensure() when the caller's org
// has already hit its consumer cap. The HTTP handlers translate this to
// 429 Too Many Requests with a Retry-After hint.
var ErrConsumerCapExceeded = errors.New("consumer cap exceeded for org")

// ceConsumer holds the long-lived subscription and buffer for one
// (org, filter) pair. It's created lazily on the first poll/stream request
// for that pair and reused across subsequent requests so durable-consumer
// state (JetStream) or queue-subscription state (in-memory) is retained.
//
// Both the poll-mode and stream-mode CloudEvents HTTP transports share this
// cache so that switching modes on the same (org, filter) resumes cleanly
// without duplicating or dropping events.
type ceConsumer struct {
	orgID   string
	name    string
	subject string
	buf     chan []byte // raw CloudEvent JSON

	// lastAccess is updated on every fast-path ensure() hit.
	lastAccess atomic.Int64

	// evicted is set to 1 after the sweeper has removed this consumer
	// from the cache map. The bus handler callback checks this before
	// writing to buf so it doesn't send on a closed channel.
	evicted atomic.Bool
}

// touch records a poll/stream access.
func (c *ceConsumer) touch(now time.Time) {
	c.lastAccess.Store(now.UnixNano())
}

// ceConsumerCache is a process-local registry of long-lived pull consumers
// keyed on the consumer name (sha256 of the filter, prefixed by org). A
// single cache is owned by the handler set so poll- and stream-mode
// requests with the same filter share cursor state.
//
// Size-bounded per-org (DefaultCEConsumerMaxPerOrg) with idle eviction
// past DefaultCEConsumerIdleTTL. Both are fields to ease overriding in
// tests. See H-15.
type ceConsumerCache struct {
	mu      sync.Mutex
	bus     bus.Bus
	items   map[string]*ceConsumer
	perOrg  map[string]int
	maxOrg  int
	idleTTL time.Duration
	clock   func() time.Time
	stop    chan struct{}
	done    chan struct{}
}

// newCEConsumerCache constructs an empty cache bound to the supplied bus.
func newCEConsumerCache(b bus.Bus) *ceConsumerCache {
	c := &ceConsumerCache{
		bus:     b,
		items:   make(map[string]*ceConsumer),
		perOrg:  make(map[string]int),
		maxOrg:  DefaultCEConsumerMaxPerOrg,
		idleTTL: DefaultCEConsumerIdleTTL,
		clock:   time.Now,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go c.sweepLoop(DefaultCEConsumerSweepInterval)
	return c
}

// NewCloudEventsConsumerCache is the exported constructor used by the API
// server wiring to build a single consumer cache shared between the
// CloudEvents poll and stream handlers.
func NewCloudEventsConsumerCache(b bus.Bus) *ceConsumerCache {
	return newCEConsumerCache(b)
}

// Close stops the sweep goroutine. Idempotent.
func (c *ceConsumerCache) Close() {
	if c == nil {
		return
	}
	select {
	case <-c.stop:
		return
	default:
		close(c.stop)
	}
	<-c.done
}

// ensure returns the ceConsumer for the given (orgID, filter), creating
// and subscribing it on first use. The subscription is long-lived, its
// buffer channel accumulates events between poll/stream invocations so the
// next drain sees everything that landed while the handler was idle.
//
// Returns ErrConsumerCapExceeded when the org has hit its consumer cap
// and the requested filter is not already cached.
func (c *ceConsumerCache) ensure(_ context.Context, orgID, filter string) (*ceConsumer, error) {
	name := ceConsumerName(orgID, filter)
	now := c.clock()
	c.mu.Lock()
	if existing, ok := c.items[name]; ok {
		existing.touch(now)
		c.mu.Unlock()
		return existing, nil
	}
	// New consumer: enforce per-org cap before allocating.
	if c.perOrg[orgID] >= c.maxOrg {
		c.mu.Unlock()
		return nil, ErrConsumerCapExceeded
	}
	subject := cepub.SubjectFor(orgID, ">")
	cons := &ceConsumer{
		orgID:   orgID,
		name:    name,
		subject: subject,
		buf:     make(chan []byte, ceConsumerBufferSize),
	}
	cons.touch(now)
	c.items[name] = cons
	c.perOrg[orgID]++
	c.mu.Unlock()

	// Register the subscription. On the in-memory ChannelBus this becomes
	// a QueueSubscribe keyed on the consumer name. On JetStream it's a
	// real durable pull consumer. Either way the handler below copies
	// the payload into our per-consumer ring so downstream readers just
	// drain a channel.
	err := c.bus.PullSubscribe(subject, name, func(_ context.Context, msg *bus.Msg) error {
		// Evicted consumers ignore messages so the closed buf channel
		// never panics on send. Any bus retry machinery will see the
		// message as ack'd (we return nil), that's fine; the consumer
		// has been reaped and a fresh one will be created on next poll.
		if cons.evicted.Load() {
			return nil
		}
		// Copy to avoid holding a reference to any reusable buffer the
		// bus may reclaim.
		buf := make([]byte, len(msg.Data))
		copy(buf, msg.Data)
		select {
		case cons.buf <- buf:
			return nil
		default:
			// Buffer full: drop on the floor. On JetStream we'd Nak()
			// to trigger redelivery once the channel drains; the
			// in-memory bus's Nak is a no-op so it'd drop regardless.
			if msg.Nak != nil {
				_ = msg.Nak()
			}
			return nil
		}
	})
	if err != nil {
		// Subscription registration failed. Remove the stub so a later
		// call retries. Return the error so the handler can surface it.
		c.mu.Lock()
		delete(c.items, name)
		if c.perOrg[orgID] > 0 {
			c.perOrg[orgID]--
		}
		c.mu.Unlock()
		return nil, err
	}
	return cons, nil
}

// sweepLoop drops consumers idle longer than idleTTL until Close is
// called. Sweep cadence is a constant interval (not idleTTL) so the
// tail of idle drops lands within one interval past TTL.
func (c *ceConsumerCache) sweepLoop(interval time.Duration) {
	defer close(c.done)
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-t.C:
			c.sweepOnce()
		}
	}
}

// sweepOnce evicts every consumer whose last access is older than
// idleTTL. The evicted flag is set AND the consumer is removed from the
// cache; subsequent ensure() calls for the same (org, filter) create a
// fresh ceConsumer. The buf channel is intentionally NOT closed: the
// bus handler's evicted-check shortcircuits deliveries, and no reader
// goroutine has any legitimate reason to consume from the evicted
// instance past this point (poll/stream goroutines exit on ctx.Done
// and hold a pointer to the old buf only for the in-flight request).
//
// R2-H6: every Bus implementation now exposes Unsubscribe, so eviction
// drops the underlying subscription as well, preventing the long-lived
// orphan-subscription leak the prior comment warned about. Errors from
// Unsubscribe are logged but do not block eviction; a stuck Unsubscribe
// would otherwise hold the cache mutex for the duration.
func (c *ceConsumerCache) sweepOnce() {
	now := c.clock()
	cutoff := now.Add(-c.idleTTL).UnixNano()

	type evicted struct {
		subject string
		name    string
	}
	var toUnsub []evicted

	c.mu.Lock()
	for name, cons := range c.items {
		if cons.lastAccess.Load() < cutoff {
			cons.evicted.Store(true)
			delete(c.items, name)
			if c.perOrg[cons.orgID] > 0 {
				c.perOrg[cons.orgID]--
			}
			toUnsub = append(toUnsub, evicted{cons.subject, cons.name})
		}
	}
	c.mu.Unlock()

	for _, e := range toUnsub {
		if err := c.bus.Unsubscribe(e.subject, e.name); err != nil {
			slog.Warn("ceConsumerCache: bus unsubscribe failed",
				"subject", e.subject, "consumer", e.name, "error", err)
		}
	}
}

// size returns the total consumer count across all orgs. Test helper.
func (c *ceConsumerCache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// ceConsumerName derives a stable durable-consumer name per (org, filter).
// Different filters must not share a consumer, otherwise non-matching
// messages get ack'd on one filter's behalf and another filter's reader
// silently loses them.
func ceConsumerName(orgID, filter string) string {
	sum := sha256.Sum256([]byte(filter))
	return "knative-source-" + orgID + "-" + hex.EncodeToString(sum[:])[:12]
}

// cleanFilters trims each filter glob and drops empty/whitespace-only entries,
// returning the globs to OR-match against. Order is preserved; matching is
// order-insensitive so it doesn't matter.
func cleanFilters(filters []string) []string {
	cleaned := make([]string, 0, len(filters))
	for _, f := range filters {
		if f = strings.TrimSpace(f); f != "" {
			cleaned = append(cleaned, f)
		}
	}
	return cleaned
}

// canonicalFilter collapses a set of filter globs into one stable string so
// that equivalent filter sets, in any order, map to the same (org, filter)
// consumer identity. Empty/whitespace-only entries are dropped, the rest are
// trimmed and sorted, then joined on a newline (a byte that cannot appear in
// a query-param value before percent-decoding, so it can't collide with a
// literal filter). No filters yields the empty string, matching the legacy
// single-empty-filter "no filtering" identity.
func canonicalFilter(filters []string) string {
	cleaned := cleanFilters(filters)
	sort.Strings(cleaned)
	return strings.Join(cleaned, "\n")
}
