package handlers_test

import (
	"testing"

	metricnoop "go.opentelemetry.io/otel/metric/noop"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/telemetry"
)

// TestCloudEventHandler_SetMetricsNilSafe confirms the SetMetrics setter
// accepts nil without panic.
func TestCloudEventHandler_SetMetricsNilSafe(t *testing.T) {
	h := handlers.NewCloudEventHandler(nil)
	h.SetMetrics(nil)
	if h == nil {
		t.Fatal("handler is nil")
	}
}

// TestCloudEventHandler_SetMetricsNoopMeter wires a noop-meter-backed
// telemetry bundle and confirms the setter does not panic.
func TestCloudEventHandler_SetMetricsNoopMeter(t *testing.T) {
	meter := metricnoop.NewMeterProvider().Meter("test")
	m, err := telemetry.NewMetrics(meter)
	if err != nil {
		t.Fatal(err)
	}
	h := handlers.NewCloudEventHandler(nil)
	h.SetMetrics(m)
	if h == nil {
		t.Fatal("handler is nil")
	}
}
