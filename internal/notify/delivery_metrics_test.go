package notify

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

type countMetrics struct {
	sent atomic.Int32
	fail atomic.Int32
}

func (m *countMetrics) IncNotificationSent(context.Context, string) { m.sent.Add(1) }
func (m *countMetrics) IncNotificationFail(context.Context, string) { m.fail.Add(1) }

// The OTEL settings page describes yipyap.notification.sent as notifications
// the provider accepted and yipyap.notification.fail as notifications that
// failed after every retry. Pin that contract: sent counts only after the
// provider returns, and fail counts once per dead letter, not per attempt.
func TestDispatcher_MetricsCountSendsAndFinalFailures(t *testing.T) {
	g := newGateNotifier()
	m := &countMetrics{}
	b := &captureBus{}
	d := NewDispatcher(b, newMemDedup(), WithMetrics(m))
	d.Register(g, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := d.Start(ctx); err != nil {
		t.Fatal(err)
	}

	st, err := b.deliver(t, domain.NotificationJob{ID: "m1", Channel: "test", OrgID: "o", DedupeKey: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	<-g.started
	if got := m.sent.Load(); got != 0 {
		t.Fatalf("sent = %d before the provider returned, want 0", got)
	}
	close(g.release)
	waitFor(t, "ack after send", func() bool { return st.acks.Load() == 1 })
	if got := m.sent.Load(); got != 1 {
		t.Fatalf("sent = %d after the send, want 1", got)
	}
	if err := d.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Three attempts against a failing provider count as one failure.
	f := newGateNotifier()
	f.fail = true
	close(f.release)
	fm := &countMetrics{}
	fb := &captureBus{}
	fd := NewDispatcher(fb, newMemDedup(), WithMetrics(fm))
	fd.Register(f, 4)
	for _, p := range fd.Pools() {
		p.SetRetryParams(3, time.Millisecond, time.Millisecond)
	}
	if err := fd.Start(ctx); err != nil {
		t.Fatal(err)
	}
	fst, err := fb.deliver(t, domain.NotificationJob{ID: "m2", Channel: "test", OrgID: "o", DedupeKey: "m2"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ack after dead letter", func() bool { return fst.acks.Load() == 1 })
	if got := fm.fail.Load(); got != 1 {
		t.Fatalf("fail = %d, want 1 per dead letter", got)
	}
	if got := fm.sent.Load(); got != 0 {
		t.Fatalf("sent = %d for a dead-lettered job, want 0", got)
	}
	if err := fd.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
