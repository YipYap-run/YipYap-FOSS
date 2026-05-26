package telemetry

import (
	"testing"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
)

// TestNewMetrics_NoopMeter confirms every counter/gauge/histogram registers
// against the noop meter provider without error. This locks in the
// CloudEvents additions (emitted/ingested/duplicates/reply/dead_letter/
// emit_latency_ms) without asserting counter values.
func TestNewMetrics_NoopMeter(t *testing.T) {
	meter := metricnoop.NewMeterProvider().Meter("test")
	m, err := NewMetrics(meter)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	if m == nil {
		t.Fatal("NewMetrics returned nil")
	}
	if m.CloudEventsEmitted == nil {
		t.Fatal("CloudEventsEmitted counter not initialised")
	}
	if m.CloudEventsIngested == nil {
		t.Fatal("CloudEventsIngested counter not initialised")
	}
	if m.CloudEventsIngestDuplicates == nil {
		t.Fatal("CloudEventsIngestDuplicates counter not initialised")
	}
	if m.CloudEventsReplyReceived == nil {
		t.Fatal("CloudEventsReplyReceived counter not initialised")
	}
	if m.CloudEventsDeadLetter == nil {
		t.Fatal("CloudEventsDeadLetter counter not initialised")
	}
	if m.CloudEventsEmitLatencyMS == nil {
		t.Fatal("CloudEventsEmitLatencyMS histogram not initialised")
	}
}
