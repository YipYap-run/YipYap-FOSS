package notify

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// mockNotifier implements Notifier for testing.
type mockNotifier struct {
	channel    string
	maxConc    int
	sendFn     func(ctx context.Context, job domain.NotificationJob) (string, error)
	sentCount  atomic.Int64
}

func (m *mockNotifier) Channel() string    { return m.channel }
func (m *mockNotifier) MaxConcurrency() int { return m.maxConc }
func (m *mockNotifier) Send(ctx context.Context, job domain.NotificationJob) (string, error) {
	m.sentCount.Add(1)
	if m.sendFn != nil {
		return m.sendFn(ctx, job)
	}
	return "ok", nil
}

func TestWorkerPool_RetryThenSucceed(t *testing.T) {
	var attempts atomic.Int64
	failTimes := 3

	mock := &mockNotifier{
		channel: "test",
		maxConc: 1,
		sendFn: func(_ context.Context, _ domain.NotificationJob) (string, error) {
			n := attempts.Add(1)
			if n <= int64(failTimes) {
				return "", fmt.Errorf("transient error %d", n)
			}
			return "ok", nil
		},
	}

	pool := NewWorkerPool(mock, 10)
	pool.SetRetryParams(5, time.Millisecond, 10*time.Millisecond)

	var deadLetterCalled atomic.Int64
	pool.SetDeadLetterHandler(func(_ domain.NotificationJob, _ error) {
		deadLetterCalled.Add(1)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool.Start(ctx)
	pool.Submit(domain.NotificationJob{ID: "j1", Channel: "test"})
	pool.Stop()

	if attempts.Load() != int64(failTimes+1) {
		t.Fatalf("expected %d attempts, got %d", failTimes+1, attempts.Load())
	}
	if deadLetterCalled.Load() != 0 {
		t.Fatal("dead letter should not be called on eventual success")
	}

	stats := pool.Stats()
	if stats.TotalSent != 1 {
		t.Fatalf("expected 1 sent, got %d", stats.TotalSent)
	}
}

func TestWorkerPool_DeadLetter(t *testing.T) {
	mock := &mockNotifier{
		channel: "test",
		maxConc: 1,
		sendFn: func(_ context.Context, _ domain.NotificationJob) (string, error) {
			return "", fmt.Errorf("permanent error")
		},
	}

	pool := NewWorkerPool(mock, 10)
	pool.SetRetryParams(5, time.Millisecond, 10*time.Millisecond)

	var deadLetterCalled atomic.Int64
	var deadJob domain.NotificationJob
	pool.SetDeadLetterHandler(func(job domain.NotificationJob, _ error) {
		deadLetterCalled.Add(1)
		deadJob = job
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool.Start(ctx)
	pool.Submit(domain.NotificationJob{ID: "j-dead", Channel: "test"})
	pool.Stop()

	if deadLetterCalled.Load() != 1 {
		t.Fatalf("expected dead letter to be called once, got %d", deadLetterCalled.Load())
	}
	if deadJob.ID != "j-dead" {
		t.Fatalf("dead letter got wrong job: %s", deadJob.ID)
	}

	// Must have attempted maxRetries times.
	if mock.sentCount.Load() != int64(maxRetries) {
		t.Fatalf("expected %d attempts, got %d", maxRetries, mock.sentCount.Load())
	}

	stats := pool.Stats()
	if stats.TotalFailed != 1 {
		t.Fatalf("expected 1 failed, got %d", stats.TotalFailed)
	}
}
