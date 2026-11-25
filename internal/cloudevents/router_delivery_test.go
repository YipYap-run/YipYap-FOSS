package cloudevents

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// handlerBus captures the router's pull handler so a test can deliver an
// event and observe ack/nak, the way JetStream does.
type handlerBus struct {
	bus.Bus
	handler   bus.AckHandler
	publishes []domain.NotificationJob
	failPub   bool
}

func (h *handlerBus) PullSubscribe(_, _ string, fn bus.AckHandler, _ ...bus.PullOpt) error {
	h.handler = fn
	return nil
}

func (h *handlerBus) PublishDurable(_ context.Context, _ string, data []byte, _ string) error {
	if h.failPub {
		return errors.New("bus down")
	}
	var job domain.NotificationJob
	_ = json.Unmarshal(data, &job)
	h.publishes = append(h.publishes, job)
	return nil
}

// deliver runs one event through the router handler. It returns whether
// the message was acked and whether it was naked (handler error).
func (h *handlerBus) deliver(t *testing.T, orgID string) (acked, naked bool) {
	t.Helper()
	ev := buildAlertFired(t, orgID)
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var acks, naks atomic.Int32
	msg := &bus.Msg{
		Subject: SubjectFor(orgID, ev.Type()),
		Data:    data,
		Ack:     func() error { acks.Add(1); return nil },
		Nak:     func() error { naks.Add(1); return nil },
	}
	if err := h.handler(context.Background(), msg); err != nil {
		_ = msg.Nak()
	}
	return acks.Load() > 0, naks.Load() > 0
}

// failingChannels makes the channel lookup fail, as during a DB blip.
type failingChannels struct{ store.NotificationChannelStore }

func (failingChannels) ListByOrg(context.Context, string, store.ListParams) ([]*domain.NotificationChannel, error) {
	return nil, errors.New("connection reset")
}

type blipStore struct{ store.Store }

func (b blipStore) NotificationChannels() store.NotificationChannelStore {
	return failingChannels{b.Store.NotificationChannels()}
}

// A failed channel lookup must nak the event so it is redelivered, not ack
// and drop it (notif-15).
func TestRouter_ChannelLookupErrorNaks(t *testing.T) {
	s, org := routerTestStore(t)
	hb := &handlerBus{}
	r := NewRouter(hb, blipStore{s}, nil, nil)
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	acked, naked := hb.deliver(t, org.ID)
	if acked || !naked {
		t.Fatalf("acked=%v naked=%v, want a nak", acked, naked)
	}
}

// When a fan-out job reaches neither the outbox nor the bus, the event is
// naked. Redelivery reuses the same job IDs and dedupe keys, so channels
// that did get the job are not sent it twice.
func TestRouter_UnqueuedJobNaksAndRetriesWithStableIDs(t *testing.T) {
	s, org := routerTestStore(t)
	seedChannel(t, s, org.ID, "cloudevent_http", "a", `{"sink_url":"https://a.example/"}`, true)
	seedChannel(t, s, org.ID, "cloudevent_http", "b", `{"sink_url":"https://b.example/"}`, true)

	hb := &handlerBus{failPub: true}
	r := NewRouter(hb, s, nil, nil)
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	ev := buildAlertFired(t, org.ID)
	data, _ := json.Marshal(ev)
	subj := SubjectFor(org.ID, ev.Type())
	run := func() ([]domain.NotificationJob, error) {
		hb.publishes = nil
		err := hb.handler(context.Background(), &bus.Msg{
			Subject: subj, Data: data,
			Ack: func() error { return nil }, Nak: func() error { return nil },
		})
		return append([]domain.NotificationJob(nil), hb.publishes...), err
	}

	if _, err := run(); err == nil {
		t.Fatal("expected an error (nak) when no job could be queued")
	}

	hb.failPub = false
	first, err := run()
	if err != nil {
		t.Fatal(err)
	}
	second, _ := run()
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("publishes = %d and %d, want 2 each", len(first), len(second))
	}
	ids := map[string]string{}
	for _, j := range first {
		ids[j.ID] = j.DedupeKey
	}
	for _, j := range second {
		if ids[j.ID] != j.DedupeKey {
			t.Fatalf("redelivery produced job %s/%s, not stable", j.ID, j.DedupeKey)
		}
	}
	if first[0].DedupeKey == first[1].DedupeKey {
		t.Fatal("each channel needs its own dedupe key")
	}
}

// With an outbox, a bus publish failure is not fatal: the job is durable.
func TestRouter_OutboxCoversBusFailure(t *testing.T) {
	s, org := routerTestStore(t)
	seedChannel(t, s, org.ID, "cloudevent_http", "a", `{"sink_url":"https://a.example/"}`, true)

	hb := &handlerBus{failPub: true}
	r := NewRouter(hb, s, s.Outbox(), nil)
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	acked, naked := hb.deliver(t, org.ID)
	if !acked || naked {
		t.Fatalf("acked=%v naked=%v, want ack", acked, naked)
	}
}
