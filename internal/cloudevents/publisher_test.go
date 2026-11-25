package cloudevents

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
	subjects []string
	msgIDs   []string
	// failPrefix makes PublishDurable fail for subjects with this prefix.
	failPrefix string
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
	if r.failPrefix != "" && strings.HasPrefix(subject, r.failPrefix) {
		return errors.New("nats: no responders available for request")
	}
	r.subject = subject
	r.data = data
	r.msgID = msgID
	r.durables++
	r.subjects = append(r.subjects, subject)
	r.msgIDs = append(r.msgIDs, msgID)
	return nil
}

func (r *recordingBus) PublishAlert(_ context.Context, subject string, data []byte, msgID string) error {
	return r.PublishDurable(context.Background(), subject, data, msgID)
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

	// One durable write to the router's egress subject and one to the
	// fan-out subject poll/stream clients read (core-1).
	if r.durables != 2 {
		t.Fatalf("expected two PublishDurable calls, got %d", r.durables)
	}
	for i, id := range r.msgIDs {
		if id != ev.ID() {
			t.Errorf("publish %d dedup msgID = %q, want event ID %q", i, id, ev.ID())
		}
	}
	wantSubjects := []string{
		"events.cloudevents.out.org-1." + types.TypeAlertFiredV1,
		"events.cloudevents.fanout.org-1." + types.TypeAlertFiredV1,
	}
	for i, want := range wantSubjects {
		if r.subjects[i] != want {
			t.Errorf("publish %d subject = %q, want %q", i, r.subjects[i], want)
		}
	}
}

func TestFanoutSubjectFor(t *testing.T) {
	got := FanoutSubjectFor("org-1", ">")
	if got != "events.cloudevents.fanout.org-1.>" {
		t.Fatalf("FanoutSubjectFor = %q", got)
	}
}

// A failed fan-out write (for example a missing optional CLOUDEVENTS_FANOUT
// stream) must not report the emit as failed: the router copy already went
// out, so webhooks and sinks still get the event (core-1).
func TestPublisher_FanoutFailureIsNotFatal(t *testing.T) {
	r := &recordingBus{failPrefix: "events.cloudevents.fanout."}
	pub := NewPublisher(r)
	ev, err := NewAlertFiredV1(OrgSource("org-1"), types.AlertFiredV1Data{
		AlertID: "a", MonitorID: "m", Severity: "critical", Reason: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(context.Background(), "org-1", ev); err != nil {
		t.Fatalf("Publish with a failing fan-out write: %v", err)
	}
	if len(r.subjects) != 1 || r.subjects[0] != "events.cloudevents.out.org-1."+types.TypeAlertFiredV1 {
		t.Fatalf("published subjects = %v, want only the egress subject", r.subjects)
	}
}

// The egress write is the one the router needs, so its failure is returned.
func TestPublisher_EgressFailureIsReturned(t *testing.T) {
	r := &recordingBus{failPrefix: "events.cloudevents.out."}
	pub := NewPublisher(r)
	ev, err := NewAlertFiredV1(OrgSource("org-1"), types.AlertFiredV1Data{
		AlertID: "a", MonitorID: "m", Severity: "critical", Reason: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(context.Background(), "org-1", ev); err == nil {
		t.Fatal("Publish succeeded although the egress write failed")
	}
	if len(r.subjects) != 0 {
		t.Fatalf("fan-out written after a failed egress write: %v", r.subjects)
	}
}
