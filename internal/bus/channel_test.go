package bus

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestChannelBus_PublishSubscribe(t *testing.T) {
	b := NewChannel()
	defer func() { _ = b.Close() }()

	var got atomic.Value
	done := make(chan struct{})

	err := b.Subscribe("test.subject", func(ctx context.Context, subject string, data []byte) error {
		got.Store(string(data))
		close(done)
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	err = b.Publish(context.Background(), "test.subject", []byte("hello"))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for delivery")
	}

	if v := got.Load().(string); v != "hello" {
		t.Fatalf("got %q, want %q", v, "hello")
	}
}

func TestChannelBus_SubscribeBroadcast(t *testing.T) {
	b := NewChannel()
	defer func() { _ = b.Close() }()

	var count atomic.Int32
	var wg sync.WaitGroup
	wg.Add(3)

	for i := 0; i < 3; i++ {
		err := b.Subscribe("broadcast.subject", func(ctx context.Context, subject string, data []byte) error {
			count.Add(1)
			wg.Done()
			return nil
		})
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
	}

	_ = b.Publish(context.Background(), "broadcast.subject", []byte("msg"))

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast delivery")
	}

	if c := count.Load(); c != 3 {
		t.Fatalf("broadcast delivered to %d handlers, want 3", c)
	}
}

func TestChannelBus_QueueSubscribe(t *testing.T) {
	b := NewChannel()
	defer func() { _ = b.Close() }()

	var count atomic.Int32
	done := make(chan struct{}, 1)

	for i := 0; i < 5; i++ {
		err := b.QueueSubscribe("queue.subject", "workers", func(ctx context.Context, subject string, data []byte) error {
			count.Add(1)
			select {
			case done <- struct{}{}:
			default:
			}
			return nil
		})
		if err != nil {
			t.Fatalf("QueueSubscribe: %v", err)
		}
	}

	_ = b.Publish(context.Background(), "queue.subject", []byte("msg"))

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queue delivery")
	}

	// Give a little time for any extra (wrong) deliveries.
	time.Sleep(50 * time.Millisecond)

	if c := count.Load(); c != 1 {
		t.Fatalf("queue delivered to %d handlers, want exactly 1", c)
	}
}

func TestChannelBus_PublishDurableFallback(t *testing.T) {
	b := NewChannel()
	defer func() { _ = b.Close() }()

	var got atomic.Value
	done := make(chan struct{})

	_ = b.Subscribe("durable.subject", func(ctx context.Context, subject string, data []byte) error {
		got.Store(string(data))
		close(done)
		return nil
	})

	err := b.PublishDurable(context.Background(), "durable.subject", []byte("durable-msg"), "msg-id-123")
	if err != nil {
		t.Fatalf("PublishDurable: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for PublishDurable delivery")
	}

	if v := got.Load().(string); v != "durable-msg" {
		t.Fatalf("got %q, want %q", v, "durable-msg")
	}
}

func TestChannelBus_PullSubscribe(t *testing.T) {
	b := NewChannel()
	defer func() { _ = b.Close() }()

	var gotMsg atomic.Value
	var ackCalled atomic.Bool
	done := make(chan struct{})

	err := b.PullSubscribe("pull.subject", "consumer1", func(ctx context.Context, msg *Msg) error {
		gotMsg.Store(string(msg.Data))
		if msg.Subject != "pull.subject" {
			t.Errorf("msg.Subject = %q, want %q", msg.Subject, "pull.subject")
		}
		ackErr := msg.Ack()
		if ackErr != nil {
			t.Errorf("Ack returned error: %v", ackErr)
		}
		ackCalled.Store(true)
		close(done)
		return nil
	})
	if err != nil {
		t.Fatalf("PullSubscribe: %v", err)
	}

	_ = b.Publish(context.Background(), "pull.subject", []byte("pull-msg"))

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for PullSubscribe delivery")
	}

	if v := gotMsg.Load().(string); v != "pull-msg" {
		t.Fatalf("got %q, want %q", v, "pull-msg")
	}
	if !ackCalled.Load() {
		t.Fatal("Ack was not called")
	}
}

func TestChannelBus_PullSubscribeNak(t *testing.T) {
	b := NewChannel()
	defer func() { _ = b.Close() }()

	done := make(chan struct{})

	_ = b.PullSubscribe("pull.nak", "consumer1", func(ctx context.Context, msg *Msg) error {
		err := msg.Nak()
		if err != nil {
			t.Errorf("Nak returned error: %v", err)
		}
		close(done)
		return nil
	})

	_ = b.Publish(context.Background(), "pull.nak", []byte("nak-msg"))

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for PullSubscribe Nak delivery")
	}
}

func TestChannelBus_Close(t *testing.T) {
	b := NewChannel()

	called := make(chan struct{}, 1)
	_ = b.Subscribe("close.subject", func(ctx context.Context, subject string, data []byte) error {
		select {
		case called <- struct{}{}:
		default:
		}
		return nil
	})

	_ = b.Close()

	// After Close, publish should still succeed but no handlers should fire.
	_ = b.Publish(context.Background(), "close.subject", []byte("after-close"))

	time.Sleep(50 * time.Millisecond)

	select {
	case <-called:
		t.Fatal("handler was called after Close")
	default:
		// expected
	}
}
