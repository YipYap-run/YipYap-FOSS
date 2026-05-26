package cloudevents

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
)

type capturedMsg struct {
	subject string
	data    []byte
}

func TestPublisher_SubjectAndPayload(t *testing.T) {
	b := bus.NewChannel()
	pub := NewPublisher(b)

	// ChannelBus uses exact subject matching (not NATS wildcards), so
	// subscribe to the exact subject we expect the publisher to derive.
	wantSubject := "events.cloudevents.out.org-1." + types.TypeAlertFiredV1
	captured := make(chan capturedMsg, 1)
	if err := b.Subscribe(wantSubject, func(ctx context.Context, subj string, data []byte) error {
		captured <- capturedMsg{subject: subj, data: data}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	ev, err := NewAlertFiredV1(OrgSource("org-1"), types.AlertFiredV1Data{
		AlertID:   "01HXY000000000000000000000",
		MonitorID: "01HXZ000000000000000000000",
		Severity:  "critical",
		Reason:    "x",
		FiredAt:   time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := pub.Publish(context.Background(), "org-1", ev); err != nil {
		t.Fatal(err)
	}

	select {
	case msg := <-captured:
		if msg.subject != wantSubject {
			t.Errorf("subject = %q, want %q", msg.subject, wantSubject)
		}
		var decoded ce.Event
		if err := json.Unmarshal(msg.data, &decoded); err != nil {
			t.Fatalf("payload is not a CloudEvent: %v", err)
		}
		if decoded.Type() != types.TypeAlertFiredV1 {
			t.Errorf("round-trip type mismatch: got %q", decoded.Type())
		}
		if decoded.ID() != ev.ID() {
			t.Errorf("round-trip id mismatch: got %q want %q", decoded.ID(), ev.ID())
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for published message")
	}
}

func TestPublisher_RejectsInvalidEvent(t *testing.T) {
	pub := NewPublisher(bus.NewChannel())
	ev := ce.NewEvent() // empty, fails Validate
	err := pub.Publish(context.Background(), "org-1", ev)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
}

func TestSubjectFor(t *testing.T) {
	got := SubjectFor("org-1", types.TypeAlertFiredV1)
	want := "events.cloudevents.out.org-1." + types.TypeAlertFiredV1
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// recordingBus is a test double that records PublishDurable calls so we can
// verify the publisher supplies the CloudEvent ID as the dedup msgID.
type recordingBus struct {
	mu       sync.Mutex
	subject  string
	data     []byte
	msgID    string
	durables int
}

func (r *recordingBus) Publish(_ context.Context, subject string, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subject = subject
	r.data = data
	return nil
}

func (r *recordingBus) PublishDurable(_ context.Context, subject string, data []byte, msgID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subject = subject
	r.data = data
	r.msgID = msgID
	r.durables++
	return nil
}

func (r *recordingBus) Subscribe(string, bus.Handler) error              { return nil }
func (r *recordingBus) QueueSubscribe(string, string, bus.Handler) error { return nil }
func (r *recordingBus) PullSubscribe(string, string, bus.AckHandler, ...bus.PullOpt) error {
	return nil
}
func (r *recordingBus) Unsubscribe(string, string) error { return nil }
func (r *recordingBus) Close() error                     { return nil }

func TestPublisher_UsesEventIDForDedup(t *testing.T) {
	r := &recordingBus{}
	pub := NewPublisher(r)

	ev, err := NewAlertFiredV1(OrgSource("org-1"), types.AlertFiredV1Data{
		AlertID:   "01HXY000000000000000000000",
		MonitorID: "01HXZ000000000000000000000",
		Severity:  "critical",
		Reason:    "x",
		FiredAt:   time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := pub.Publish(context.Background(), "org-1", ev); err != nil {
		t.Fatal(err)
	}

	if r.durables != 1 {
		t.Fatalf("expected one PublishDurable call, got %d", r.durables)
	}
	if r.msgID != ev.ID() {
		t.Errorf("dedup msgID = %q, want event ID %q", r.msgID, ev.ID())
	}
	if r.subject != "events.cloudevents.out.org-1."+types.TypeAlertFiredV1 {
		t.Errorf("subject = %q", r.subject)
	}
}
