package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/telemetry"
)

// DispatcherMetrics records notification metrics. Pass nil to disable.
type DispatcherMetrics interface {
	IncNotificationSent(ctx context.Context, orgID string)
	IncNotificationFail(ctx context.Context, orgID string)
}

const (
	subjectNotifyRequest = "notify.request"
	subjectNotifyDead    = "notify.dead"
	queueGroup           = "notifiers"
	dedupeTTL            = 5 * time.Minute
	dedupeCleanup        = 1 * time.Minute
)

// Dispatcher routes notification jobs to the correct provider WorkerPool and
// handles dead-lettering of exhausted jobs.
type Dispatcher struct {
	pools             map[string]*WorkerPool // keyed by channel name
	bus               bus.Bus
	dedup             store.DedupStore  // DB-backed dedup (nil falls back to in-memory)
	outbox            store.OutboxStore // DB-backed outbox (nil disables polling)
	metrics           DispatcherMetrics
	orgRec            telemetry.OrgRecorder
	recorder          OutcomeRecorder
	workerID          string // unique identifier for this instance
	mu                sync.RWMutex
	deadLetterHandler func(domain.NotificationJob, error)

	stopping  atomic.Bool
	stopLoops context.CancelFunc
	loops     sync.WaitGroup

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

// WithOrgRecorder records notification outcomes for each org's own OTLP
// export. Only jobs built from the org's notification channels count;
// staff and support notifications that name an org do not.
func WithOrgRecorder(r telemetry.OrgRecorder) DispatcherOption {
	return func(d *Dispatcher) { d.orgRec = r }
}

// SetOrgRecorder is WithOrgRecorder for an existing dispatcher. Call it
// before Start.
func (d *Dispatcher) SetOrgRecorder(r telemetry.OrgRecorder) { d.orgRec = r }

// recordOrgOutcome counts job for its org's export. A job without a
// ChannelID did not come from the org's channels (support, staff alerts,
// tests) and is skipped.
func (d *Dispatcher) recordOrgOutcome(job domain.NotificationJob, sent bool) {
	if d.orgRec == nil || job.OrgID == "" || job.ChannelID == "" {
		return
	}
	d.orgRec.RecordNotification(context.Background(), job.OrgID, job.Channel, sent)
}

// WithRecorder records each successful send (timeline event and channel
// health) right after the provider accepts it. Failures are recorded by
// the notify.dead consumer, see StartDeadLetterConsumer.
func WithRecorder(r OutcomeRecorder) DispatcherOption {
	return func(d *Dispatcher) { d.recorder = r }
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

// ErrDispatcherStopped is returned for work offered after Stop began. Bus
// handlers return it so the message is naked and redelivered elsewhere.
var ErrDispatcherStopped = errors.New("notify: dispatcher stopping")

// errDuplicate marks a job whose dedupe key is already held: it was sent
// recently or another instance (or the outbox poller) is sending it now.
var errDuplicate = errors.New("notify: duplicate job")

// Start subscribes to the bus and launches all worker pools. It also starts
// the deduplication cleanup goroutine and, with an outbox, the outbox poller.
//
// Pool sends run on ctx. Callers must call Stop with a separate deadline
// context before cancelling ctx, or queued jobs are abandoned instead of
// sent.
func (d *Dispatcher) Start(ctx context.Context) error {
	d.mu.RLock()
	for _, pool := range d.pools {
		pool.Start(ctx)
	}
	d.mu.RUnlock()

	loopCtx, cancel := context.WithCancel(ctx)
	d.stopLoops = cancel

	// Start dedup cleanup.
	d.loops.Add(1)
	go func() {
		defer d.loops.Done()
		d.cleanupLoop(loopCtx)
	}()

	// Start outbox poller if outbox store is configured.
	if d.outbox != nil {
		d.loops.Add(1)
		go func() {
			defer d.loops.Done()
			d.outboxPollLoop(loopCtx)
		}()
	}

	return d.bus.PullSubscribe(subjectNotifyRequest, queueGroup, d.handleMsg)
}

// handleMsg is the notify.request bus handler. It acks the message only
// after the job reaches a final state (sent or dead-lettered). Returning an
// error makes the bus nak the message for redelivery.
func (d *Dispatcher) handleMsg(_ context.Context, msg *bus.Msg) error {
	var job domain.NotificationJob
	if err := json.Unmarshal(msg.Data, &job); err != nil {
		slog.Error("failed to unmarshal notification job", "error", err)
		return msg.Ack() // bad message, don't redeliver
	}

	err := d.dispatch(job, func(o Outcome, _ error) {
		switch o {
		case OutcomeSent:
			d.completeOutbox(job.ID)
			ackMsg(msg, job.ID)
		case OutcomeDeadLettered:
			d.deadOutbox(job.ID)
			ackMsg(msg, job.ID)
		default:
			// Abandoned on shutdown: hand it back for redelivery. The
			// outbox row stays pending or claimed for the poller.
			if err := msg.Nak(); err != nil {
				slog.Warn("notify: nak failed", "job_id", job.ID, "error", err)
			}
		}
	})
	if errors.Is(err, errDuplicate) {
		// Another holder of the dedupe key owns delivery and the outbox
		// row. Acking here only drops this copy.
		return msg.Ack()
	}
	// nil: the done callback acks later. Non-nil: the bus naks.
	return err
}

func ackMsg(msg *bus.Msg, jobID string) {
	if err := msg.Ack(); err != nil {
		slog.Warn("notify: ack failed", "job_id", jobID, "error", err)
	}
}

func (d *Dispatcher) completeOutbox(id string) {
	if d.outbox == nil || id == "" {
		return
	}
	if err := d.outbox.Complete(context.Background(), id); err != nil {
		slog.Error("outbox complete failed", "id", id, "error", err)
	}
}

// deadOutbox marks an outbox row dead after a final dead letter so the
// poller does not resend a job whose retries are already exhausted.
func (d *Dispatcher) deadOutbox(id string) {
	if d.outbox == nil || id == "" {
		return
	}
	if err := d.outbox.Fail(context.Background(), id, 1); err != nil {
		slog.Error("outbox fail failed", "id", id, "error", err)
	}
}

// Stop refuses new work, stops the outbox poller, and drains every worker
// pool. Queued jobs keep sending on the context given to Start, so callers
// cancel that context only after Stop returns. If ctx expires first, the
// remaining jobs are abandoned (naked for redelivery, not dead-lettered)
// and ctx.Err() is returned.
func (d *Dispatcher) Stop(ctx context.Context) error {
	d.stopping.Store(true)
	if d.stopLoops != nil {
		d.stopLoops()
	}
	d.loops.Wait()

	d.mu.RLock()
	pools := make([]*WorkerPool, 0, len(d.pools))
	for _, pool := range d.pools {
		pools = append(pools, pool)
	}
	d.mu.RUnlock()

	errs := make(chan error, len(pools))
	for _, pool := range pools {
		go func(p *WorkerPool) { errs <- p.Stop(ctx) }(pool)
	}
	var first error
	for range pools {
		if err := <-errs; err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Dispatch routes a job to the correct worker pool. It returns an error if
// no pool is registered for the job's channel or the dispatcher is
// stopping. A duplicate job is dropped and returns nil.
func (d *Dispatcher) Dispatch(job domain.NotificationJob) error {
	err := d.dispatch(job, nil)
	if errors.Is(err, errDuplicate) {
		return nil
	}
	return err
}

// dedupeKey returns the key that guards a job against double delivery.
// Jobs without an explicit key fall back to their ID, which the bus copy
// and the outbox copy share.
func dedupeKey(job domain.NotificationJob) string {
	if job.DedupeKey != "" {
		return job.DedupeKey
	}
	if job.ID != "" {
		return "job:" + job.ID
	}
	return ""
}

// dispatch claims the job's dedupe key and submits it. The key is held
// while the job is in flight and kept after a successful send. It is
// released when the job is not accepted, abandoned on shutdown, or
// dead-lettered, so a later redelivery (bus or outbox) can try again.
// done is called once with the final outcome only when dispatch returns nil.
func (d *Dispatcher) dispatch(job domain.NotificationJob, done DoneFunc) error {
	if d.stopping.Load() {
		return ErrDispatcherStopped
	}

	// Resolve the pool before claiming the dedupe key, so a job for an
	// unregistered channel does not use up the key for a later valid job.
	d.mu.RLock()
	pool, ok := d.pools[job.Channel]
	d.mu.RUnlock()
	if !ok {
		return fmt.Errorf("no provider registered for channel %q", job.Channel)
	}

	key := dedupeKey(job)
	if key != "" && !d.markSeen(key) {
		slog.Debug("deduplicated notification", "dedupe_key", key)
		return errDuplicate
	}
	marker := d.markInflight(job.ID)

	err := pool.Submit(job, func(o Outcome, err error) {
		d.clearInflight(marker)
		if o == OutcomeSent {
			if d.metrics != nil {
				d.metrics.IncNotificationSent(context.Background(), job.OrgID)
			}
			d.recordOrgOutcome(job, true)
			d.recordSent(job)
		} else if key != "" {
			d.release(key)
		}
		if done != nil {
			done(o, err)
		}
	})
	if err != nil {
		d.clearInflight(marker)
		if key != "" {
			d.release(key)
		}
		if errors.Is(err, ErrPoolStopped) {
			return ErrDispatcherStopped
		}
		return err
	}
	return nil
}

// inflightTTL outlives the dedupe claim, so after a hard crash the dedupe
// key expires (and the outbox resends) before the marker can mislead the
// poller into treating the job as finished.
const inflightTTL = dedupeTTL + time.Minute

func inflightKey(jobID string) string { return "inflight:" + jobID }

// markInflight records, in the shared dedup store, that this job ID is
// being sent right now. The outbox poller uses it to tell "the same job is
// in flight elsewhere" apart from "the dedupe key was used by a sent or
// different job". Best effort: returns "" when there is nothing to clear.
func (d *Dispatcher) markInflight(jobID string) string {
	if d.dedup == nil || jobID == "" {
		return ""
	}
	key := inflightKey(jobID)
	if _, err := d.dedup.Claim(context.Background(), key, inflightTTL); err != nil {
		slog.Warn("inflight marker claim failed", "job_id", jobID, "error", err)
	}
	return key
}

func (d *Dispatcher) clearInflight(marker string) {
	if marker == "" {
		return
	}
	if err := d.dedup.Release(context.Background(), marker); err != nil {
		slog.Warn("inflight marker release failed", "key", marker, "error", err)
	}
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

// release drops a dedupe claim so the job can be delivered again.
func (d *Dispatcher) release(key string) {
	if d.dedup != nil {
		if err := d.dedup.Release(context.Background(), key); err != nil {
			slog.Error("dedup release failed", "dedupe_key", key, "error", err)
		}
	}
	d.dedupeMu.Lock()
	delete(d.seen, key)
	d.dedupeMu.Unlock()
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

		id := oj.ID
		done := func(o Outcome, _ error) {
			switch o {
			case OutcomeSent:
				d.completeOutbox(id)
			case OutcomeDeadLettered:
				d.deadOutbox(id)
			}
			// Abandoned: leave the row claimed. The stale-claim reclaim
			// hands it to the next poll after outboxStaleAfter.
		}
		err := d.dispatch(job, done)
		switch {
		case errors.Is(err, errDuplicate):
			d.outboxDuplicate(ctx, id, job, done)
		case err == nil, errors.Is(err, ErrDispatcherStopped):
			// Accepted, or shutting down (the row is reclaimed once stale).
		default:
			slog.Warn("outbox: dispatch failed", "id", id, "error", err)
			_ = d.outbox.Fail(ctx, id, outboxMaxAttempts)
		}
	}
}

// outboxDuplicate handles an outbox row whose dedupe key is held. If the
// same job ID is in flight on some sender, that sender completes the row,
// so it is left claimed (and reclaimed later if that sender dies). If not,
// the key belongs to a job that was already sent or to a different job
// with the same key: try once more in case the holder just let go, and
// otherwise complete the row so it is not resent when the key expires.
func (d *Dispatcher) outboxDuplicate(ctx context.Context, id string, job domain.NotificationJob, done DoneFunc) {
	if d.dedup == nil || job.ID == "" {
		return
	}
	marker := inflightKey(job.ID)
	free, err := d.dedup.Claim(ctx, marker, inflightTTL)
	if err != nil || !free {
		return
	}
	_ = d.dedup.Release(ctx, marker)

	err = d.dispatch(job, done)
	switch {
	case err == nil, errors.Is(err, ErrDispatcherStopped):
	case errors.Is(err, errDuplicate):
		d.completeOutbox(id)
	default:
		slog.Warn("outbox: dispatch failed", "id", id, "error", err)
		_ = d.outbox.Fail(ctx, id, outboxMaxAttempts)
	}
}

func (d *Dispatcher) recordSent(job domain.NotificationJob) {
	if d.recorder == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.recorder.RecordSent(ctx, job); err != nil {
		slog.Warn("notify: record sent outcome", "job_id", job.ID, "channel_id", job.ChannelID, "error", err)
	}
}

// deadLetterMsgID keeps the notify.dead publish out of the stream's
// duplicate window for the notify.request message, which uses the bare job
// ID. Both subjects share the NOTIFICATIONS stream, so reusing the job ID
// would make JetStream drop the dead letter as a duplicate.
func deadLetterMsgID(jobID string) string { return "dead:" + jobID }

func (d *Dispatcher) handleDeadLetter(job domain.NotificationJob, err error) {
	if d.metrics != nil {
		d.metrics.IncNotificationFail(context.Background(), job.OrgID)
	}
	d.recordOrgOutcome(job, false)

	slog.Error("notification dead letter",
		"job_id", job.ID,
		"channel", job.Channel,
		"channel_id", job.ChannelID,
		"alert_id", job.AlertID,
		"error", LogSafeError(err),
	)

	if d.deadLetterHandler != nil {
		d.deadLetterHandler(job, err)
	}

	// Publish to notify.dead, where the delivery recorder turns it into a
	// notification_failed timeline event and channel health. The payload
	// holds the sanitized reason only.
	if d.bus == nil {
		return
	}
	data, mErr := json.Marshal(NewDeadLetter(job, err, time.Now()))
	if mErr != nil {
		slog.Error("notify: marshal dead letter", "job_id", job.ID, "error", mErr)
		return
	}
	if pErr := d.bus.PublishDurable(context.Background(), subjectNotifyDead, data, deadLetterMsgID(job.ID)); pErr != nil {
		slog.Error("notify: publish dead letter", "job_id", job.ID, "error", pErr)
	}
}
