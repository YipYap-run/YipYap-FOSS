package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// captureBus records the notify.request pull handler so tests can deliver
// messages with observable Ack/Nak, the way JetStream does.
type captureBus struct {
	bus.Bus
	mu      sync.Mutex
	handler bus.AckHandler
	dead    int
}

func (b *captureBus) PullSubscribe(_, _ string, h bus.AckHandler, _ ...bus.PullOpt) error {
	b.mu.Lock()
	b.handler = h
	b.mu.Unlock()
	return nil
}

func (b *captureBus) PublishDurable(_ context.Context, subject string, _ []byte, _ string) error {
	if subject == subjectNotifyDead {
		b.mu.Lock()
		b.dead++
		b.mu.Unlock()
	}
	return nil
}

func (b *captureBus) deadCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dead
}

// ackState tracks what happened to one delivered message.
type ackState struct {
	acks atomic.Int32
	naks atomic.Int32
}

// deliver hands a job to the dispatcher's handler and returns the handler
// error and the message's ack state. A non-nil handler error means the bus
// naks the message, as JetStreamBus.fetchLoop does.
func (b *captureBus) deliver(t *testing.T, job domain.NotificationJob) (*ackState, error) {
	t.Helper()
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	st := &ackState{}
	msg := &bus.Msg{
		Subject: subjectNotifyRequest,
		Data:    data,
		Ack:     func() error { st.acks.Add(1); return nil },
		Nak:     func() error { st.naks.Add(1); return nil },
	}
	b.mu.Lock()
	h := b.handler
	b.mu.Unlock()
	if h == nil {
		t.Fatal("dispatcher did not subscribe")
	}
	herr := h(context.Background(), msg)
	if herr != nil {
		_ = msg.Nak()
	}
	return st, herr
}

// memDedup is a shared DedupStore, standing in for the DB table that all
// notifier replicas share.
type memDedup struct {
	mu   sync.Mutex
	keys map[string]time.Time
}

func newMemDedup() *memDedup { return &memDedup{keys: map[string]time.Time{}} }

func (m *memDedup) Claim(_ context.Context, key string, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if exp, ok := m.keys[key]; ok && time.Now().Before(exp) {
		return false, nil
	}
	m.keys[key] = time.Now().Add(ttl)
	return true, nil
}

func (m *memDedup) Release(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.keys, key)
	return nil
}

func (m *memDedup) Peek(_ context.Context, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	exp, ok := m.keys[key]
	return ok && time.Now().Before(exp), nil
}

func (m *memDedup) Cleanup(context.Context) error { return nil }

func (m *memDedup) has(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.keys[key]
	return ok
}

// memOutbox records outbox transitions.
type memOutbox struct {
	mu        sync.Mutex
	pending   []store.OutboxJob
	completed []string
	failed    map[string]int // id -> maxAttempts passed
}

func newMemOutbox() *memOutbox { return &memOutbox{failed: map[string]int{}} }

func (o *memOutbox) Enqueue(_ context.Context, id, payload string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pending = append(o.pending, store.OutboxJob{ID: id, Payload: payload})
	return nil
}

func (o *memOutbox) Claim(_ context.Context, _ string, _ int, _ time.Duration) ([]store.OutboxJob, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := o.pending
	o.pending = nil
	return out, nil
}

func (o *memOutbox) Complete(_ context.Context, id string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.completed = append(o.completed, id)
	return nil
}

func (o *memOutbox) Fail(_ context.Context, id string, maxAttempts int) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.failed[id] = maxAttempts
	return nil
}

func (o *memOutbox) Cleanup(context.Context, time.Duration) error { return nil }

func (o *memOutbox) snapshot() (completed []string, failed map[string]int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	f := make(map[string]int, len(o.failed))
	for k, v := range o.failed {
		f[k] = v
	}
	return append([]string(nil), o.completed...), f
}

// gateNotifier blocks each Send until release is closed or ctx ends.
type gateNotifier struct {
	release chan struct{}
	started chan string
	sent    atomic.Int32
	fail    bool
}

func newGateNotifier() *gateNotifier {
	return &gateNotifier{release: make(chan struct{}), started: make(chan string, 64)}
}

func (g *gateNotifier) Channel() string     { return "test" }
func (g *gateNotifier) MaxConcurrency() int { return 1 }
func (g *gateNotifier) Send(ctx context.Context, job domain.NotificationJob) (string, error) {
	select {
	case g.started <- job.ID:
	default:
	}
	select {
	case <-g.release:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if g.fail {
		return "", errors.New("provider down")
	}
	g.sent.Add(1)
	return "ok", nil
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func newTestDispatcher(t *testing.T, n Notifier, dedup store.DedupStore, outbox store.OutboxStore) (*Dispatcher, *captureBus, context.CancelFunc) {
	t.Helper()
	b := &captureBus{}
	var opts []DispatcherOption
	if outbox != nil {
		opts = append(opts, WithOutbox(outbox))
	}
	d := NewDispatcher(b, dedup, opts...)
	d.Register(n, 16)
	ctx, cancel := context.WithCancel(context.Background())
	if err := d.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	return d, b, cancel
}

// The bus message and the outbox row must stay open until the provider has
// actually sent the notification (core-6, notif-3).
func TestDispatcher_AcksAndCompletesOnlyAfterSend(t *testing.T) {
	g := newGateNotifier()
	ob := newMemOutbox()
	d, b, _ := newTestDispatcher(t, g, newMemDedup(), ob)

	st, err := b.deliver(t, domain.NotificationJob{ID: "j1", Channel: "test", DedupeKey: "k1"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	<-g.started
	if st.acks.Load() != 0 {
		t.Fatal("message acked before the send happened")
	}
	if done, _ := ob.snapshot(); len(done) != 0 {
		t.Fatal("outbox completed before the send happened")
	}

	close(g.release)
	waitFor(t, "ack after send", func() bool { return st.acks.Load() == 1 })
	if done, _ := ob.snapshot(); len(done) != 1 || done[0] != "j1" {
		t.Fatalf("outbox completed = %v, want [j1]", done)
	}
	if st.naks.Load() != 0 {
		t.Fatal("unexpected nak")
	}
	if err := d.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// A replica that dies before sending must not swallow the job: the message
// is naked, the dedupe claim is released, and the redelivery to another
// replica sends it exactly once (core-6, notif-3).
func TestDispatcher_CrashBeforeSendIsRedeliveredAndSentOnce(t *testing.T) {
	dedup := newMemDedup()
	job := domain.NotificationJob{ID: "j-crash", Channel: "test", DedupeKey: "alert-1-step-1"}

	// Replica A accepts the job but its provider never completes.
	gA := newGateNotifier()
	dA, bA, _ := newTestDispatcher(t, gA, dedup, nil)
	stA, err := bA.deliver(t, job)
	if err != nil {
		t.Fatalf("replica A handler error: %v", err)
	}
	<-gA.started
	if !dedup.has(job.DedupeKey) {
		t.Fatal("dedupe key should be held while the job is in flight")
	}

	// A is killed: the drain deadline passes before the send finishes.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := dA.Stop(ctx); err == nil {
		t.Fatal("expected the drain deadline error")
	}
	if stA.acks.Load() != 0 || stA.naks.Load() != 1 {
		t.Fatalf("replica A: acks=%d naks=%d, want 0 and 1", stA.acks.Load(), stA.naks.Load())
	}
	if dedup.has(job.DedupeKey) {
		t.Fatal("dedupe key must be released when the job was not sent")
	}
	if bA.deadCount() != 0 {
		t.Fatal("an abandoned job must not be dead-lettered")
	}

	// The bus redelivers to replica B, which sends it.
	gB := newGateNotifier()
	close(gB.release)
	dB, bB, _ := newTestDispatcher(t, gB, dedup, nil)
	stB, err := bB.deliver(t, job)
	if err != nil {
		t.Fatalf("replica B handler error: %v", err)
	}
	waitFor(t, "replica B ack", func() bool { return stB.acks.Load() == 1 })

	// A further redelivery (AckWait expiry, outbox poller) is a duplicate.
	stDup, err := bB.deliver(t, job)
	if err != nil {
		t.Fatalf("duplicate handler error: %v", err)
	}
	if stDup.acks.Load() != 1 {
		t.Fatal("duplicate copy should be acked and dropped")
	}
	if err := dB.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := gA.sent.Load() + gB.sent.Load(); got != 1 {
		t.Fatalf("sent %d times, want exactly 1", got)
	}
}

// Shutdown drains queued jobs while the send context is still live, so
// they are sent and acked instead of dead-lettered (notif-4).
func TestDispatcher_ShutdownDrainsQueuedJobs(t *testing.T) {
	g := newGateNotifier()
	d, b, cancelRoot := newTestDispatcher(t, g, newMemDedup(), nil)

	const n = 8
	states := make([]*ackState, n)
	for i := range n {
		st, err := b.deliver(t, domain.NotificationJob{ID: fmt.Sprintf("q%d", i), Channel: "test"})
		if err != nil {
			t.Fatalf("deliver %d: %v", i, err)
		}
		states[i] = st
	}

	// Release the provider shortly after shutdown begins.
	go func() {
		time.Sleep(20 * time.Millisecond)
		close(g.release)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.Stop(ctx); err != nil {
		t.Fatalf("drain: %v", err)
	}
	cancelRoot()

	if got := g.sent.Load(); got != n {
		t.Fatalf("sent %d, want %d", got, n)
	}
	for i, st := range states {
		if st.acks.Load() != 1 || st.naks.Load() != 0 {
			t.Fatalf("job %d: acks=%d naks=%d", i, st.acks.Load(), st.naks.Load())
		}
	}
	if b.deadCount() != 0 {
		t.Fatalf("dead letters = %d, want 0", b.deadCount())
	}
}

// Work offered after Stop must be naked, not silently dropped (notif-4).
func TestDispatcher_SubmitAfterStopNaks(t *testing.T) {
	g := newGateNotifier()
	close(g.release)
	dedup := newMemDedup()
	d, b, _ := newTestDispatcher(t, g, dedup, nil)
	if err := d.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	st, err := b.deliver(t, domain.NotificationJob{ID: "late", Channel: "test", DedupeKey: "late-key"})
	if !errors.Is(err, ErrDispatcherStopped) {
		t.Fatalf("handler error = %v, want ErrDispatcherStopped", err)
	}
	if st.acks.Load() != 0 || st.naks.Load() != 1 {
		t.Fatalf("acks=%d naks=%d, want 0 and 1", st.acks.Load(), st.naks.Load())
	}
	if dedup.has("late-key") {
		t.Fatal("a refused job must not hold its dedupe key")
	}
}

func TestWorkerPool_SubmitAfterStopErrors(t *testing.T) {
	g := newGateNotifier()
	close(g.release)
	pool := NewWorkerPool(g, 4)
	pool.Start(context.Background())
	if err := pool.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	called := false
	err := pool.Submit(domain.NotificationJob{ID: "x"}, func(Outcome, error) { called = true })
	if !errors.Is(err, ErrPoolStopped) {
		t.Fatalf("Submit after Stop = %v, want ErrPoolStopped", err)
	}
	if called {
		t.Fatal("done must not run for a refused job")
	}
}

// A final dead letter acks the message, marks the outbox row dead so the
// poller does not resend it, and releases the dedupe key.
func TestDispatcher_DeadLetterAcksAndClosesOutbox(t *testing.T) {
	g := newGateNotifier()
	g.fail = true
	close(g.release)
	dedup := newMemDedup()
	ob := newMemOutbox()
	b := &captureBus{}
	d := NewDispatcher(b, dedup, WithOutbox(ob))
	d.Register(g, 4)
	for _, p := range d.Pools() {
		p.SetRetryParams(2, time.Millisecond, time.Millisecond)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := d.Start(ctx); err != nil {
		t.Fatal(err)
	}

	st, err := b.deliver(t, domain.NotificationJob{ID: "dl", Channel: "test", DedupeKey: "dl-key"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ack after dead letter", func() bool { return st.acks.Load() == 1 })
	done, failed := ob.snapshot()
	if len(done) != 0 {
		t.Fatalf("outbox completed = %v, want none", done)
	}
	if failed["dl"] != 1 {
		t.Fatalf("outbox Fail maxAttempts = %d, want 1 (mark dead)", failed["dl"])
	}
	if dedup.has("dl-key") {
		t.Fatal("dedupe key should be released after a dead letter")
	}
	if err := d.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// The outbox poller completes a row only after the send. A row whose job
// is in flight on another sender is left for that sender. A row whose
// dedupe key belongs to a job that already went out is completed, so it is
// not resent when the key expires.
func TestDispatcher_OutboxCompletesOnlyAfterSend(t *testing.T) {
	g := newGateNotifier()
	dedup := newMemDedup()
	ob := newMemOutbox()
	b := &captureBus{}
	d := NewDispatcher(b, dedup, WithOutbox(ob))
	d.Register(g, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := d.Start(ctx); err != nil {
		t.Fatal(err)
	}

	enqueue := func(job domain.NotificationJob) {
		payload, _ := json.Marshal(job)
		_ = ob.Enqueue(ctx, job.ID, string(payload))
	}
	// Fresh job: sent by the poller.
	enqueue(domain.NotificationJob{ID: "ob1", Channel: "test", DedupeKey: "ob-key"})
	// Same job in flight on another replica: key and in-flight marker held.
	enqueue(domain.NotificationJob{ID: "ob2", Channel: "test", DedupeKey: "busy-key"})
	_, _ = dedup.Claim(ctx, "busy-key", time.Minute)
	_, _ = dedup.Claim(ctx, inflightKey("ob2"), time.Minute)
	// Key used by a job that was already sent; nothing in flight.
	enqueue(domain.NotificationJob{ID: "ob3", Channel: "test", DedupeKey: "sent-key"})
	_, _ = dedup.Claim(ctx, "sent-key", time.Minute)

	d.processOutbox(ctx)
	<-g.started
	if done, _ := ob.snapshot(); len(done) != 1 || done[0] != "ob3" {
		t.Fatalf("before the send, completed = %v, want [ob3]", done)
	}
	close(g.release)
	waitFor(t, "outbox complete", func() bool {
		done, _ := ob.snapshot()
		return len(done) == 2
	})
	done, failed := ob.snapshot()
	if done[1] != "ob1" {
		t.Fatalf("completed %v, want [ob3 ob1]", done)
	}
	if len(failed) != 0 {
		t.Fatalf("no row should be failed, got %v", failed)
	}
	if got := g.sent.Load(); got != 1 {
		t.Fatalf("sent %d, want 1", got)
	}
	if err := d.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
