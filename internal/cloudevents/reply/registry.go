package reply

import (
	"container/list"
	"context"
	"fmt"
	"sync"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
)

// Entry records an outbound CloudEvent so replies can correlate back to it
// via the ce_alertref extension attribute.
type Entry struct {
	EventID   string
	OrgID     string
	AlertID   string // may be empty for monitor/maintenance events
	MonitorID string // may be empty
	ChannelID string // which channel emitted this event (per-channel opt-in)
	EmittedAt time.Time
}

// OutboundRegistry tracks recently-emitted outbound CloudEvents so that
// reply events can reference them. Implementations must be safe for
// concurrent use.
type OutboundRegistry interface {
	// Record stores the event. orgID and channelID are passed alongside
	// the event because they aren't guaranteed to be present as extensions.
	Record(ctx context.Context, ev ce.Event, orgID, channelID string) error
	// Lookup returns the entry for the given CloudEvent id. Returns
	// (nil, false) if the entry is unknown or has expired past the
	// registry's TTL.
	Lookup(ctx context.Context, eventID string) (*Entry, bool)
}

// MemoryRegistry is an in-process LRU with TTL. It is backed by a map and a
// doubly-linked list ordered from least- to most-recently-used. Safe for
// concurrent use.
//
// MemoryRegistry is intentionally simple: cross-replica correlation is not
// a goal for Phase 2. A future JetStream KV-backed OutboundRegistry can be
// layered on when multi-replica support becomes necessary.
type MemoryRegistry struct {
	mu         sync.Mutex
	maxEntries int
	ttl        time.Duration
	clock      func() time.Time

	index map[string]*list.Element // eventID → list element
	order *list.List               // front = most recent; back = least recent
}

// NewMemoryRegistry returns a MemoryRegistry with the given capacity and
// TTL. maxEntries<=0 disables the capacity bound; ttl<=0 disables the TTL
// check (entries live until evicted by capacity).
func NewMemoryRegistry(maxEntries int, ttl time.Duration) *MemoryRegistry {
	return &MemoryRegistry{
		maxEntries: maxEntries,
		ttl:        ttl,
		clock:      time.Now,
		index:      make(map[string]*list.Element),
		order:      list.New(),
	}
}

// SetClock swaps the clock used for TTL checks. Intended for tests.
func (r *MemoryRegistry) SetClock(fn func() time.Time) {
	if fn == nil {
		fn = time.Now
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clock = fn
}

// Record stores ev in the registry, keyed by ev.ID(). orgID and channelID
// are captured because neither is guaranteed to be present as a CE
// extension attribute.
func (r *MemoryRegistry) Record(ctx context.Context, ev ce.Event, orgID, channelID string) error {
	if ev.ID() == "" {
		return fmt.Errorf("reply: MemoryRegistry.Record: event has empty id")
	}

	entry := &Entry{
		EventID:   ev.ID(),
		OrgID:     orgID,
		ChannelID: channelID,
		EmittedAt: r.now(),
	}
	entry.AlertID, entry.MonitorID = extractDataIDs(ev)

	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.index[entry.EventID]; ok {
		existing.Value = entry
		r.order.MoveToFront(existing)
	} else {
		el := r.order.PushFront(entry)
		r.index[entry.EventID] = el
	}

	r.evictLocked()
	return nil
}

// Lookup returns the entry for the given event id or (nil, false) if the
// entry is unknown or has expired past the TTL. An expired entry is also
// removed from the registry as a side effect.
func (r *MemoryRegistry) Lookup(ctx context.Context, eventID string) (*Entry, bool) {
	if eventID == "" {
		return nil, false
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	el, ok := r.index[eventID]
	if !ok {
		return nil, false
	}
	entry := el.Value.(*Entry)
	if r.expiredLocked(entry) {
		r.removeLocked(el)
		return nil, false
	}
	return entry, true
}

// Len returns the number of live entries currently held. Primarily useful
// for tests.
func (r *MemoryRegistry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.order.Len()
}

func (r *MemoryRegistry) now() time.Time {
	if r.clock == nil {
		return time.Now()
	}
	return r.clock()
}

func (r *MemoryRegistry) expiredLocked(e *Entry) bool {
	if r.ttl <= 0 {
		return false
	}
	return r.now().Sub(e.EmittedAt) > r.ttl
}

func (r *MemoryRegistry) evictLocked() {
	// Evict any expired entries from the back (oldest first).
	if r.ttl > 0 {
		for r.order.Len() > 0 {
			el := r.order.Back()
			if !r.expiredLocked(el.Value.(*Entry)) {
				break
			}
			r.removeLocked(el)
		}
	}
	// Enforce capacity.
	if r.maxEntries > 0 {
		for r.order.Len() > r.maxEntries {
			r.removeLocked(r.order.Back())
		}
	}
}

func (r *MemoryRegistry) removeLocked(el *list.Element) {
	if el == nil {
		return
	}
	entry := el.Value.(*Entry)
	delete(r.index, entry.EventID)
	r.order.Remove(el)
}

// extractDataIDs pulls best-effort alert_id / monitor_id identifiers from
// the event's structured data payload. Missing fields return "".
func extractDataIDs(ev ce.Event) (alertID, monitorID string) {
	data := ev.Data()
	if len(data) == 0 {
		return "", ""
	}
	// Small inline parse, we don't want to depend on the generated types
	// package from this low-level package, and every Phase-1 data payload
	// that has these ids spells them exactly this way.
	var probe struct {
		AlertID   string `json:"alert_id"`
		MonitorID string `json:"monitor_id"`
	}
	_ = unmarshalJSON(data, &probe)
	return probe.AlertID, probe.MonitorID
}
