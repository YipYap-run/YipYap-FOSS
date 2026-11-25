package notify

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

const (
	maxRetries     = 5
	initialBackoff = 1 * time.Second
	maxBackoff     = 60 * time.Second

	// abandonWait bounds how long Stop waits for workers to notice a
	// cancelled context after the drain deadline has passed.
	abandonWait = 5 * time.Second
)

// ErrPoolStopped is returned by Submit once Stop has been called. Callers
// must treat the job as not accepted (nak the bus message, leave the outbox
// row for another instance).
var ErrPoolStopped = errors.New("notify: worker pool stopped")

// Outcome is the final state of one submitted job.
type Outcome int

const (
	// OutcomeSent means the provider accepted the notification.
	OutcomeSent Outcome = iota
	// OutcomeDeadLettered means every retry failed. The job is final for
	// this delivery attempt and has been passed to the dead letter handler.
	OutcomeDeadLettered
	// OutcomeAbandoned means the pool shut down before the job reached a
	// final state. The job was not sent and must be redelivered.
	OutcomeAbandoned
)

func (o Outcome) String() string {
	switch o {
	case OutcomeSent:
		return "sent"
	case OutcomeDeadLettered:
		return "dead_lettered"
	case OutcomeAbandoned:
		return "abandoned"
	default:
		return "unknown"
	}
}

// DoneFunc is called exactly once per accepted job with its final outcome.
// err is the last send error (nil for OutcomeSent).
type DoneFunc func(outcome Outcome, err error)

// PoolStats holds runtime statistics for a single worker pool.
type PoolStats struct {
	QueueDepth  int   `json:"queue_depth"`
	Inflight    int64 `json:"inflight"`
	MaxWorkers  int   `json:"max_workers"`
	TotalSent   int64 `json:"total_sent"`
	TotalFailed int64 `json:"total_failed"`
}

// DeadLetterFunc is called when a job exhausts its retries.
type DeadLetterFunc func(job domain.NotificationJob, err error)

type poolItem struct {
	job  domain.NotificationJob
	done DoneFunc
}

// WorkerPool manages a fixed number of goroutines that drain a job channel
// and send notifications through a single Notifier.
type WorkerPool struct {
	notifier       Notifier
	jobs           chan poolItem
	inflight       atomic.Int64
	sent           atomic.Int64
	failed         atomic.Int64
	wg             sync.WaitGroup
	deadLetter     DeadLetterFunc
	maxRetries     int
	initialBackoff time.Duration
	maxBackoff     time.Duration

	// mu guards stopped and the close of jobs so Submit never sends on a
	// closed channel and never succeeds after Stop.
	mu      sync.RWMutex
	stopped bool

	cancelWork context.CancelFunc
}

// NewWorkerPool creates a pool for the given notifier. queueSize controls the
// buffered channel depth.
func NewWorkerPool(notifier Notifier, queueSize int) *WorkerPool {
	return &WorkerPool{
		notifier:       notifier,
		jobs:           make(chan poolItem, queueSize),
		maxRetries:     maxRetries,
		initialBackoff: initialBackoff,
		maxBackoff:     maxBackoff,
		cancelWork:     func() {},
	}
}

// SetRetryParams overrides retry behaviour (useful for testing).
func (w *WorkerPool) SetRetryParams(maxRetries int, initial, max time.Duration) {
	w.maxRetries = maxRetries
	w.initialBackoff = initial
	w.maxBackoff = max
}

// SetDeadLetterHandler sets the callback for jobs that exhaust retries.
func (w *WorkerPool) SetDeadLetterHandler(fn DeadLetterFunc) {
	w.deadLetter = fn
}

// Submit enqueues a job for delivery. done (may be nil) is called once the
// job is sent, dead-lettered or abandoned. Submit blocks while the queue is
// full and returns ErrPoolStopped once Stop has been called, in which case
// done is never called.
func (w *WorkerPool) Submit(job domain.NotificationJob, done DoneFunc) error {
	// Holding the read lock across the send keeps Stop from closing the
	// channel underneath us. Stop takes the write lock, so a Submit blocked
	// on a full queue delays Stop until a worker frees a slot.
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.stopped {
		return ErrPoolStopped
	}
	w.jobs <- poolItem{job: job, done: done}
	return nil
}

// Start launches MaxConcurrency workers that process jobs until Stop is
// called. Sends use a context derived from ctx, so cancelling ctx abandons
// queued and in-flight work.
func (w *WorkerPool) Start(ctx context.Context) {
	workCtx, cancel := context.WithCancel(ctx)
	w.cancelWork = cancel
	n := w.notifier.MaxConcurrency()
	for range n {
		w.wg.Add(1)
		go w.worker(workCtx)
	}
}

// Stop refuses new submissions and waits for queued and in-flight jobs to
// finish. Sends keep running on the context passed to Start, so a caller
// must not cancel that context before Stop returns. If ctx expires first,
// Stop cancels the remaining work (those jobs end as OutcomeAbandoned),
// waits briefly for workers to exit, and returns ctx.Err().
func (w *WorkerPool) Stop(ctx context.Context) error {
	w.mu.Lock()
	if !w.stopped {
		w.stopped = true
		close(w.jobs)
	}
	w.mu.Unlock()

	drained := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(drained)
	}()

	select {
	case <-drained:
		w.cancelWork()
		return nil
	case <-ctx.Done():
	}

	w.cancelWork()
	select {
	case <-drained:
	case <-time.After(abandonWait):
		slog.Warn("notify: workers did not exit after abandon", "channel", w.notifier.Channel())
	}
	return ctx.Err()
}

// Stats returns a snapshot of pool metrics.
func (w *WorkerPool) Stats() PoolStats {
	return PoolStats{
		QueueDepth:  len(w.jobs),
		Inflight:    w.inflight.Load(),
		MaxWorkers:  w.notifier.MaxConcurrency(),
		TotalSent:   w.sent.Load(),
		TotalFailed: w.failed.Load(),
	}
}

func (w *WorkerPool) worker(ctx context.Context) {
	defer w.wg.Done()
	for item := range w.jobs {
		w.process(ctx, item)
	}
}

func (w *WorkerPool) process(ctx context.Context, item poolItem) {
	if ctx.Err() != nil {
		finish(item.done, OutcomeAbandoned, ctx.Err())
		return
	}

	w.inflight.Add(1)
	err := w.sendWithRetry(ctx, item.job)
	w.inflight.Add(-1)

	switch {
	case err == nil:
		w.sent.Add(1)
		finish(item.done, OutcomeSent, nil)
	case ctx.Err() != nil:
		// Shutdown cut the job short. It is not a delivery failure, so it
		// is not dead-lettered: the caller redelivers it.
		finish(item.done, OutcomeAbandoned, err)
	default:
		w.failed.Add(1)
		if w.deadLetter != nil {
			w.deadLetter(item.job, err)
		}
		finish(item.done, OutcomeDeadLettered, err)
	}
}

func finish(done DoneFunc, o Outcome, err error) {
	if done != nil {
		done(o, err)
	}
}

func (w *WorkerPool) sendWithRetry(ctx context.Context, job domain.NotificationJob) error {
	backoff := w.initialBackoff
	var lastErr error
	for attempt := range w.maxRetries {
		_, err := w.notifier.Send(ctx, job)
		if err == nil {
			return nil
		}
		lastErr = err
		slog.Warn("notification send failed",
			"channel", w.notifier.Channel(),
			"attempt", attempt+1,
			"job_id", job.ID,
			"error", LogSafeError(err),
		)

		if attempt < w.maxRetries-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > w.maxBackoff {
				backoff = w.maxBackoff
			}
		}
	}
	return lastErr
}
