package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// DispatcherMetrics records notification metrics. Pass nil to disable.
type DispatcherMetrics interface {
	IncNotificationSent(ctx context.Context, orgID string)
	IncNotificationFail(ctx context.Context, orgID string)
}

const (
	subjectNotifyRequest = "notify.request"
	subjectNotifyDead = "notify.dead"
	queueGroup        = "notifiers"
	dedupeTTL         = 5 * time.Minute
	dedupeCleanup     = 1 * time.Minute
)

// Dispatcher routes notification jobs to the correct provider WorkerPool and
// handles dead-lettering of exhausted jobs.
type Dispatcher struct {
	pools             map[string]*WorkerPool // keyed by channel name
	bus               bus.Bus
	dedup             store.DedupStore  // DB-backed dedup (nil falls back to in-memory)
	outbox            store.OutboxStore // DB-backed outbox (nil disables polling)
	metrics           DispatcherMetrics
	workerID          string // unique identifier for this instance
	mu                sync.RWMutex
	deadLetterHandler func(domain.NotificationJob, error)

	// in-memory deduplication fallback (single-instance only)
	dedupeMu sync.Mutex
	seen     map[string]time.Time
}

// NewDispatcher creates a dispatcher wired to the given message bus.
// Optional DedupStore and OutboxStore enable cross-instance correctness.
func NewDispatcher(b bus.Bus, dedup store.DedupStore, opts ...DispatcherOption) *Dispatcher {
	d := &Dispatcher{
		pools:    make(map[string]*WorkerPool),
		bus:      b,
		dedup:    dedup,
		workerID: fmt.Sprintf("notifier-%d", time.Now().UnixNano()),
		seen:     make(map[string]time.Time),
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// DispatcherOption configures a Dispatcher.
type DispatcherOption func(*Dispatcher)

// WithOutbox enables the DB-backed notification outbox poller.
func WithOutbox(o store.OutboxStore) DispatcherOption {
	return func(d *Dispatcher) { d.outbox = o }
}

// WithMetrics enables metrics recording on the dispatcher.
func WithMetrics(m DispatcherMetrics) DispatcherOption {
	return func(d *Dispatcher) { d.metrics = m }
}

// SetDeadLetterHandler sets a global callback for jobs that exhaust retries.
func (d *Dispatcher) SetDeadLetterHandler(fn func(domain.NotificationJob, error)) {
	d.deadLetterHandler = fn
}

// Register adds a provider's worker pool to the dispatcher.
func (d *Dispatcher) Register(notifier Notifier, queueSize int) {
	pool := NewWorkerPool(notifier, queueSize)
	pool.SetDeadLetterHandler(d.handleDeadLetter)

	d.mu.Lock()
	d.pools[notifier.Channel()] = pool
	d.mu.Unlock()
}

// TestSend synchronously sends a notification job through the provider for the
// given channel type. Used by the test endpoint for immediate feedback.
func (d *Dispatcher) TestSend(ctx context.Context, job domain.NotificationJob) (string, error) {
	d.mu.RLock()
	pool, ok := d.pools[job.Channel]
	d.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("no provider registered for channel %q", job.Channel)
	}
	return pool.notifier.Send(ctx, job)
}

// Start subscribes to the bus and launches all worker pools. It also starts
// the deduplication cleanup goroutine.
func (d *Dispatcher) Start(ctx context.Context) error {
	d.mu.RLock()
	for _, pool := range d.pools {
		pool.Start(ctx)
	}
	d.mu.RUnlock()

	// Start dedup cleanup.
	go d.cleanupLoop(ctx)

	// Start outbox poller if outbox store is configured.
	if d.outbox != nil {
		go d.outboxPollLoop(ctx)
	}

	return d.bus.PullSubscribe(subjectNotifyRequest, queueGroup, func(ctx context.Context, msg *bus.Msg) error {
		var job domain.NotificationJob
		if err := json.Unmarshal(msg.Data, &job); err != nil {
			slog.Error("failed to unmarshal notification job", "error", err)
			return msg.Ack() // bad message, don't redeliver
		}

		if err := d.Dispatch(job); err != nil {
			return err // auto-nak → redeliver
		}

		// Mark outbox complete if using outbox (backward compat).
		if d.outbox != nil && job.ID != "" {
			_ = d.outbox.Complete(context.Background(), job.ID)
		}
		return msg.Ack()
	})
}

// Stop drains all worker pools.
func (d *Dispatcher) Stop() {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, pool := range d.pools {
		pool.Stop()
	}
}

// Dispatch routes a job to the correct worker pool. It returns an error if
// no pool is registered for the job's channel.
func (d *Dispatcher) Dispatch(job domain.NotificationJob) error {
	// Deduplicate.
	if job.DedupeKey != "" && !d.markSeen(job.DedupeKey) {
		slog.Debug("deduplicated notification", "dedupe_key", job.DedupeKey)
		return nil
	}

	d.mu.RLock()
	pool, ok := d.pools[job.Channel]
	d.mu.RUnlock()
	if !ok {
		return fmt.Errorf("no provider registered for channel %q", job.Channel)
	}

	pool.Submit(job)

	if d.metrics != nil {
		d.metrics.IncNotificationSent(context.Background(), job.OrgID)
	}

	return nil
}

// Pools returns the registered pools (for the load reporter).
func (d *Dispatcher) Pools() map[string]*WorkerPool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	// Return a shallow copy.
	cp := make(map[string]*WorkerPool, len(d.pools))
	for k, v := range d.pools {
		cp[k] = v
	}
	return cp
}

// markSeen returns true if this is the first time the key is seen within the
// TTL window. Returns false (duplicate) otherwise.
func (d *Dispatcher) markSeen(key string) bool {
	// Prefer DB-backed dedup for cross-instance correctness.
	if d.dedup != nil {
		claimed, err := d.dedup.Claim(context.Background(), key, dedupeTTL)
		if err != nil {
			slog.Error("dedup claim failed, falling back to in-memory", "error", err)
			// Fall through to in-memory.
		} else {
			return claimed
		}
	}

	d.dedupeMu.Lock()
	defer d.dedupeMu.Unlock()
	if exp, ok := d.seen[key]; ok && time.Now().Before(exp) {
		return false
	}
	d.seen[key] = time.Now().Add(dedupeTTL)
	return true
}

func (d *Dispatcher) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(dedupeCleanup)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Clean up DB-backed dedup entries.
			if d.dedup != nil {
				if err := d.dedup.Cleanup(ctx); err != nil {
					slog.Error("dedup cleanup failed", "error", err)
				}
			}

			// Clean up in-memory fallback.
			d.dedupeMu.Lock()
			now := time.Now()
			for k, exp := range d.seen {
				if now.After(exp) {
					delete(d.seen, k)
				}
			}
			d.dedupeMu.Unlock()
		}
	}
}

const (
	// The outbox poller is a failsafe  - the bus handles the fast path.
	// Poll every 5s and reclaim jobs that weren't completed within 30s.
	outboxPollInterval = 5 * time.Second
	outboxStaleAfter   = 30 * time.Second
	outboxBatchSize    = 20
	outboxMaxAttempts  = 5
	outboxCleanupAge   = 24 * time.Hour
)

func (d *Dispatcher) outboxPollLoop(ctx context.Context) {
	ticker := time.NewTicker(outboxPollInterval)
	defer ticker.Stop()

	cleanupTicker := time.NewTicker(10 * time.Minute)
	defer cleanupTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.processOutbox(ctx)
		case <-cleanupTicker.C:
			if err := d.outbox.Cleanup(ctx, outboxCleanupAge); err != nil {
				slog.Error("outbox cleanup failed", "error", err)
			}
		}
	}
}

func (d *Dispatcher) processOutbox(ctx context.Context) {
	jobs, err := d.outbox.Claim(ctx, d.workerID, outboxBatchSize, outboxStaleAfter)
	if err != nil {
		slog.Error("outbox claim failed", "error", err)
		return
	}

	for _, oj := range jobs {
		var job domain.NotificationJob
		if err := json.Unmarshal([]byte(oj.Payload), &job); err != nil {
			slog.Error("outbox: bad payload", "id", oj.ID, "error", err)
			_ = d.outbox.Fail(ctx, oj.ID, outboxMaxAttempts)
			continue
		}

		if err := d.Dispatch(job); err != nil {
			slog.Warn("outbox: dispatch failed", "id", oj.ID, "error", err)
			_ = d.outbox.Fail(ctx, oj.ID, outboxMaxAttempts)
			continue
		}

		_ = d.outbox.Complete(ctx, oj.ID)
	}
}

func (d *Dispatcher) handleDeadLetter(job domain.NotificationJob, err error) {
	if d.metrics != nil {
		d.metrics.IncNotificationFail(context.Background(), job.OrgID)
	}

	slog.Error("notification dead letter",
		"job_id", job.ID,
		"channel", job.Channel,
		"alert_id", job.AlertID,
		"error", err,
	)

	if d.deadLetterHandler != nil {
		d.deadLetterHandler(job, err)
	}

	// Publish to dead letter subject.
	type deadMsg struct {
		Job   domain.NotificationJob `json:"job"`
		Error string                 `json:"error"`
	}
	data, _ := json.Marshal(deadMsg{Job: job, Error: err.Error()})
	_ = d.bus.Publish(context.Background(), subjectNotifyDead, data)
}
