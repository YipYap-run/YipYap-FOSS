package bus

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// LogEntry is one retained message read from an EventLog.
type LogEntry struct {
	// Seq is the message's position in the log. It increases with every
	// append across all subjects, so readers on a filtered subject see
	// gaps between their sequence numbers.
	Seq     uint64
	Subject string
	Data    []byte
	// Time is when the entry was appended to this log.
	Time time.Time
}

// LogState describes the log as a whole.
type LogState struct {
	// Epoch identifies one incarnation of the log. Sequence numbers are
	// only comparable within an epoch: a recreated stream or a restarted
	// in-memory log starts a new epoch and restarts its numbering.
	Epoch string
	// FirstSeq is the oldest retained sequence (0 when empty).
	FirstSeq uint64
	// LastSeq is the newest sequence ever appended in this epoch (0 when
	// nothing has been appended).
	LastSeq uint64
}

// EventLog is a retained, sequence-addressed log that many independent
// readers can read from any position. The CloudEvents poll and stream API
// use it so that each client keeps its own cursor: a read never consumes
// or acknowledges anything, so a client that re-reads from an older cursor
// gets the same events again (at-least-once delivery).
type EventLog interface {
	// LogState returns the log's epoch and sequence bounds.
	LogState(ctx context.Context) (LogState, error)
	// ReadLog returns up to max entries whose subject matches filter and
	// whose sequence is greater than after, oldest first. When none are
	// available it waits up to wait for the first one; once it has one it
	// returns after a short pause with no further arrivals. It returns an
	// empty slice when wait or ctx expires first.
	ReadLog(ctx context.Context, filter string, after uint64, max int, wait time.Duration) ([]LogEntry, error)
}

// EventSeeker is implemented by logs whose sequence numbers are local to
// one process, such as MemoryEventLog. Each web replica on a plain NATS bus
// keeps its own log with its own epoch, so a cursor issued by one replica
// means nothing to another. SeekAfter lets a reader map such a cursor onto
// this log by finding the entry the cursor named.
type EventSeeker interface {
	// SeekAfter returns the sequence to read after. It looks for the first
	// entry on filter, appended at or after notBefore, for which anchor
	// returns true, and returns its sequence. Without a match it returns
	// the sequence just before the first entry appended at or after
	// notBefore, so a reader gets every entry from that time on.
	SeekAfter(filter string, notBefore time.Time, anchor func(data []byte) bool) uint64
}

// Unwrapper is implemented by Bus decorators so callers can reach optional
// interfaces (such as EventLog) on the bus they wrap.
type Unwrapper interface {
	Unwrap() Bus
}

// AsEventLog returns b, or the first bus it wraps, as an EventLog.
func AsEventLog(b Bus) (EventLog, bool) {
	for b != nil {
		if l, ok := b.(EventLog); ok {
			return l, true
		}
		u, ok := b.(Unwrapper)
		if !ok {
			return nil, false
		}
		b = u.Unwrap()
	}
	return nil, false
}

// ErrEventLogUnavailable is returned when the log's backing store does
// not exist, for example a JetStream server without the fan-out stream.
var ErrEventLogUnavailable = errors.New("event log unavailable")

// logIdleGrace is how long a read that already has entries waits for more
// before returning, so a burst lands in one batch without making every
// read pay the full wait.
const logIdleGrace = 50 * time.Millisecond

// MemoryEventLog is an in-process EventLog fed by a broadcast subscription.
// It backs the CloudEvents poll and stream API on buses without a durable
// stream (the in-process channel bus and plain NATS). It retains entries
// for maxAge, up to capacity entries, and loses them on restart; the epoch
// is random per process so cursors from before a restart are detected.
type MemoryEventLog struct {
	mu       sync.Mutex
	epoch    string
	entries  []LogEntry // oldest first
	nextSeq  uint64
	capacity int
	maxAge   time.Duration
	times    []time.Time // append time per entry, parallel to entries
	notify   chan struct{}
	clock    func() time.Time
}

// NewMemoryEventLog subscribes to subject on b and returns a log of every
// message published there. capacity and maxAge bound retention; zero
// values pick 10000 entries and 24 hours.
func NewMemoryEventLog(b Bus, subject string, capacity int, maxAge time.Duration) (*MemoryEventLog, error) {
	l := newMemoryEventLog(capacity, maxAge)
	if err := b.Subscribe(subject, func(_ context.Context, subj string, data []byte) error {
		l.Append(subj, data)
		return nil
	}); err != nil {
		return nil, err
	}
	return l, nil
}

func newMemoryEventLog(capacity int, maxAge time.Duration) *MemoryEventLog {
	if capacity <= 0 {
		capacity = 10000
	}
	if maxAge <= 0 {
		maxAge = 24 * time.Hour
	}
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return &MemoryEventLog{
		epoch:    "mem-" + hex.EncodeToString(raw[:]),
		nextSeq:  1,
		capacity: capacity,
		maxAge:   maxAge,
		notify:   make(chan struct{}),
		clock:    time.Now,
	}
}

// Append adds one entry. Exported so tests and callers that already hold
// the message can feed the log directly.
func (l *MemoryEventLog) Append(subject string, data []byte) {
	buf := make([]byte, len(data))
	copy(buf, data)
	l.mu.Lock()
	now := l.clock()
	l.entries = append(l.entries, LogEntry{Seq: l.nextSeq, Subject: subject, Data: buf, Time: now})
	l.times = append(l.times, now)
	l.nextSeq++
	l.trimLocked(now)
	close(l.notify)
	l.notify = make(chan struct{})
	l.mu.Unlock()
}

// trimLocked drops entries past capacity or older than maxAge.
func (l *MemoryEventLog) trimLocked(now time.Time) {
	drop := 0
	if over := len(l.entries) - l.capacity; over > 0 {
		drop = over
	}
	cutoff := now.Add(-l.maxAge)
	for drop < len(l.times) && l.times[drop].Before(cutoff) {
		drop++
	}
	if drop > 0 {
		l.entries = append(l.entries[:0:0], l.entries[drop:]...)
		l.times = append(l.times[:0:0], l.times[drop:]...)
	}
}

// LogState implements EventLog.
func (l *MemoryEventLog) LogState(_ context.Context) (LogState, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.trimLocked(l.clock())
	st := LogState{Epoch: l.epoch, LastSeq: l.nextSeq - 1}
	if len(l.entries) > 0 {
		st.FirstSeq = l.entries[0].Seq
	}
	return st, nil
}

// ReadLog implements EventLog.
func (l *MemoryEventLog) ReadLog(ctx context.Context, filter string, after uint64, max int, wait time.Duration) ([]LogEntry, error) {
	if max <= 0 {
		return []LogEntry{}, nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	out := []LogEntry{}
	idle := time.NewTimer(logIdleGrace)
	idle.Stop()
	defer idle.Stop()
	var grace <-chan time.Time
	for {
		var notify chan struct{}
		before := len(out)
		out, after, notify = l.collect(filter, after, max, out)
		if len(out) >= max {
			return out, nil
		}
		if len(out) > before {
			// New entries restart the idle pause.
			idle.Reset(logIdleGrace)
			grace = idle.C
		}
		select {
		case <-notify:
		case <-grace:
			return out, nil
		case <-timer.C:
			return out, nil
		case <-ctx.Done():
			return out, nil
		}
	}
}

// collect appends matching entries after the given sequence to out and
// returns the new high-water mark and the channel that closes on the next
// append.
func (l *MemoryEventLog) collect(filter string, after uint64, max int, out []LogEntry) ([]LogEntry, uint64, chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.entries {
		if e.Seq <= after {
			continue
		}
		after = e.Seq
		if !subjectMatches(filter, e.Subject) {
			continue
		}
		out = append(out, e)
		if len(out) >= max {
			break
		}
	}
	return out, after, l.notify
}

var _ EventSeeker = (*MemoryEventLog)(nil)

// SeekAfter implements EventSeeker.
func (l *MemoryEventLog) SeekAfter(filter string, notBefore time.Time, anchor func(data []byte) bool) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	var before uint64
	if len(l.entries) > 0 {
		before = l.entries[0].Seq - 1
	}
	for i, e := range l.entries {
		if l.times[i].Before(notBefore) {
			before = e.Seq
			continue
		}
		if anchor != nil && subjectMatches(filter, e.Subject) && anchor(e.Data) {
			return e.Seq
		}
	}
	return before
}
