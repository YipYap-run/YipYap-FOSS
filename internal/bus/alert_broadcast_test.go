package bus

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestChannelPublishAlertDeliversOncePerListener checks that PublishAlert on
// the in-process bus reaches the pull consumer once on the durable subject
// and the broadcast listener once on the ws.alert.* subject. (core-16)
func TestChannelPublishAlertDeliversOncePerListener(t *testing.T) {
	b := NewChannel()
	var pulled, broadcast, durableBroadcast atomic.Int32
	if err := b.PullSubscribe("alert.ack", "escalation-ack", func(_ context.Context, _ *Msg) error {
		pulled.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = b.Subscribe(AlertBroadcastSubject("alert.ack"), func(context.Context, string, []byte) error {
		broadcast.Add(1)
		return nil
	})
	_ = b.Subscribe("alert.ack", func(context.Context, string, []byte) error {
		durableBroadcast.Add(1)
		return nil
	})

	if err := b.PublishAlert(context.Background(), "alert.ack", []byte(`{}`), "id"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if pulled.Load() != 1 || broadcast.Load() != 1 || durableBroadcast.Load() != 1 {
		t.Fatalf("pulled=%d broadcast=%d durable-subject listener=%d, want 1 each",
			pulled.Load(), broadcast.Load(), durableBroadcast.Load())
	}
}
