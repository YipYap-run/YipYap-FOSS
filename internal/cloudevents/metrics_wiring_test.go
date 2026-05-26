package cloudevents

import (
	"context"
	"testing"
	"time"

	metricnoop "go.opentelemetry.io/otel/metric/noop"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/telemetry"
)

// TestPublisher_SetMetricsNilSafe verifies that a Publisher configured without
// metrics does not panic on Publish, the nil-safe recording path is used.
func TestPublisher_SetMetricsNilSafe(t *testing.T) {
	b := bus.NewChannel()
	pub := NewPublisher(b)
	pub.SetMetrics(nil)

	ev, err := NewAlertFiredV1(OrgSource("org-1"), types.AlertFiredV1Data{
		AlertID:   "01HXY000000000000000000000",
		MonitorID: "01HXZ000000000000000000000",
		Severity:  "critical",
		Reason:    "nil-safe",
		FiredAt:   time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(context.Background(), "org-1", ev); err != nil {
		t.Fatalf("Publish with nil metrics: %v", err)
	}
}

// TestPublisher_SetMetricsNoopMeter verifies that a Publisher configured with
// a no-op OTel meter-provider records without panicking.
func TestPublisher_SetMetricsNoopMeter(t *testing.T) {
	meter := metricnoop.NewMeterProvider().Meter("test")
	m, err := telemetry.NewMetrics(meter)
	if err != nil {
		t.Fatal(err)
	}

	b := bus.NewChannel()
	pub := NewPublisher(b)
	pub.SetMetrics(m)

	ev, err := NewAlertFiredV1(OrgSource("org-1"), types.AlertFiredV1Data{
		AlertID:   "01HXY000000000000000000000",
		MonitorID: "01HXZ000000000000000000000",
		Severity:  "critical",
		Reason:    "noop-ok",
		FiredAt:   time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(context.Background(), "org-1", ev); err != nil {
		t.Fatalf("Publish with noop meter metrics: %v", err)
	}
}

// TestRouter_SetMetricsNilAndNoop exercises the Router setter under both nil
// and no-op-meter-backed telemetry bundles. We call SetMetrics directly and
// confirm no panic occurs; we don't start the bus subscription because the
// router's fanout callback is invoked asynchronously by the bus and the goal
// here is to cover the setter contract.
func TestRouter_SetMetricsNilAndNoop(t *testing.T) {
	r := &Router{}
	r.SetMetrics(nil)

	meter := metricnoop.NewMeterProvider().Meter("test")
	m, err := telemetry.NewMetrics(meter)
	if err != nil {
		t.Fatal(err)
	}
	r.SetMetrics(m)
}
