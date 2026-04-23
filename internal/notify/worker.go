package notify

import (
	"context"
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
)

// PoolStats holds runtime statistics for a single worker pool.
type PoolStats struct {
	QueueDepth int   `json:"queue_depth"`
	Inflight   int64 `json:"inflight"`
	MaxWorkers int   `json:"max_workers"`
	TotalSent  int64 `json:"total_sent"`
	TotalFailed int64 `json:"total_failed"`
}

// DeadLetterFunc is called when a job exhausts its retries.
type DeadLetterFunc func(job domain.NotificationJob, err error)

// WorkerPool manages a fixed number of goroutines that drain a job channel
// and send notifications through a single Notifier.
type WorkerPool struct {
	notifier       Notifier
	jobs           chan domain.NotificationJob
	inflight       atomic.Int64
	sent           atomic.Int64
	failed         atomic.Int64
	wg             sync.WaitGroup
	deadLetter     DeadLetterFunc
	maxRetries     int
	initialBackoff time.Duration
	maxBackoff     time.Duration
	stopOnce       sync.Once
	stopped        atomic.Bool
}

// NewWorkerPool creates a pool for the given notifier. queueSize controls the
// buffered channel depth.
func NewWorkerPool(notifier Notifier, queueSize int) *WorkerPool {
	return &WorkerPool{
		notifier:       notifier,
		jobs:           make(chan domain.NotificationJob, queueSize),
		maxRetries:     maxRetries,
		initialBackoff: initialBackoff,
		maxBackoff:     maxBackoff,
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

// Submit enqueues a job for delivery. It does not block beyond the channel
// buffer capacity. Submits after Stop are silently dropped.
func (w *WorkerPool) Submit(job domain.NotificationJob) {
	if w.stopped.Load() {
		return
	}
	// Recover from send-on-closed-channel in case Stop races with Submit.
	defer func() { _ = recover() }()
	w.jobs <- job
}

// Start launches MaxConcurrency workers that process jobs until ctx is
// cancelled or Stop is called.
func (w *WorkerPool) Start(ctx context.Context) {
	n := w.notifier.MaxConcurrency()
	for range n {
		w.wg.Add(1)
		go w.worker(ctx)
	}
}

// Stop closes the job channel and waits for all in-flight work to finish.
func (w *WorkerPool) Stop() {
	w.stopOnce.Do(func() {
		w.stopped.Store(true)
		close(w.jobs)
	})
	w.wg.Wait()
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
	for job := range w.jobs {
		w.inflight.Add(1)
		err := w.sendWithRetry(ctx, job)
		w.inflight.Add(-1)
		if err != nil {
			w.failed.Add(1)
			if w.deadLetter != nil {
				w.deadLetter(job, err)
			}
		} else {
			w.sent.Add(1)
		}
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
			"error", err,
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
