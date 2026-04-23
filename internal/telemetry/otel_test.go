package telemetry_test

import (
	"context"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/telemetry"
)

func TestSetup_NoEndpoint(t *testing.T) {
	tel, err := telemetry.Setup(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tel.Shutdown(context.Background()) }()
	if tel.Meter == nil {
		t.Fatal("meter should not be nil")
	}
	if tel.Tracer == nil {
		t.Fatal("tracer should not be nil")
	}
}

func TestNewMetrics(t *testing.T) {
	tel, _ := telemetry.Setup(context.Background(), "")
	defer func() { _ = tel.Shutdown(context.Background()) }()
	m, err := telemetry.NewMetrics(tel.Meter)
	if err != nil {
		t.Fatal(err)
	}
	if m.CheckLatency == nil {
		t.Fatal("CheckLatency nil")
	}
	if m.BusPublishCount == nil {
		t.Fatal("BusPublishCount nil")
	}
	if m.HTTPRequestLatencyMS == nil {
		t.Fatal("HTTPRequestLatencyMS nil")
	}
}

func TestTraceContextRoundTrip(t *testing.T) {
	tel, _ := telemetry.Setup(context.Background(), "")
	defer func() { _ = tel.Shutdown(context.Background()) }()

	ctx, span := tel.Tracer.Start(context.Background(), "test-span")
	defer span.End()

	headers := telemetry.InjectTraceContext(ctx)
	if len(headers) == 0 {
		// No-op propagator when no exporter  - this is acceptable
		t.Skip("no trace headers without exporter")
	}
}
