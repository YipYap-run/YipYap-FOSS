package telemetry

import (
	"testing"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
)

// TestNewMetrics_NoopMeter confirms every counter/gauge/histogram registers
// against the noop meter provider without error, without asserting
// counter values.
func TestNewMetrics_NoopMeter(t *testing.T) {
	meter := metricnoop.NewMeterProvider().Meter("test")
	m, err := NewMetrics(meter)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	if m == nil {
		t.Fatal("NewMetrics returned nil")
	}
}
