package providers

import (
	"testing"

	metricnoop "go.opentelemetry.io/otel/metric/noop"

	"github.com/YipYap-run/YipYap-FOSS/internal/telemetry"
)

// TestCloudEventHTTP_SetMetricsNilSafe confirms SetMetrics(nil) is accepted.
func TestCloudEventHTTP_SetMetricsNilSafe(t *testing.T) {
	p := NewCloudEventHTTP(nil)
	p.SetMetrics(nil)
	if p == nil {
		t.Fatal("provider is nil")
	}
}

// TestCloudEventHTTP_SetMetricsNoopMeter wires a noop-meter-backed
// telemetry.Metrics bundle and confirms setter wiring does not panic.
func TestCloudEventHTTP_SetMetricsNoopMeter(t *testing.T) {
	meter := metricnoop.NewMeterProvider().Meter("test")
	m, err := telemetry.NewMetrics(meter)
	if err != nil {
		t.Fatal(err)
	}
	p := NewCloudEventHTTP(nil)
	p.SetMetrics(m)
	if p == nil {
		t.Fatal("provider is nil")
	}
}
