package cloudevents

import (
	"context"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
)

// Compile-time assertion that *Publisher satisfies Emitter.
var _ Emitter = (*Publisher)(nil)

func TestEmitter_NilSafeHelpers(t *testing.T) {
	// All four helpers must be no-ops when the Emitter is nil.
	if err := EmitAlertFired(context.Background(), nil, "org", types.AlertFiredV1Data{}); err != nil {
		t.Errorf("nil EmitAlertFired: %v", err)
	}
	if err := EmitAlertAcknowledged(context.Background(), nil, "org", types.AlertAcknowledgedV1Data{}); err != nil {
		t.Errorf("nil EmitAlertAcknowledged: %v", err)
	}
	if err := EmitAlertResolved(context.Background(), nil, "org", types.AlertResolvedV1Data{}); err != nil {
		t.Errorf("nil EmitAlertResolved: %v", err)
	}
	if err := EmitAlertEscalated(context.Background(), nil, "org", types.AlertEscalatedV1Data{}); err != nil {
		t.Errorf("nil EmitAlertEscalated: %v", err)
	}
}

func TestPublisher_EmitAlertFired_PublishesToExpectedSubject(t *testing.T) {
	b := bus.NewChannel()
	pub := NewPublisher(b)

	captured := make(chan []byte, 1)
	if err := b.Subscribe("events.cloudevents.out.org-1."+types.TypeAlertFiredV1, func(ctx context.Context, subj string, data []byte) error {
		captured <- data
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := pub.EmitAlertFired(context.Background(), "org-1", types.AlertFiredV1Data{
		AlertID:   "alert-1",
		MonitorID: "mon-1",
		Severity:  "critical",
		Reason:    "down",
		FiredAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-captured:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for fired event")
	}
}
