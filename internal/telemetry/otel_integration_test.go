package telemetry_test

import (
	"context"
	"net"
	"testing"
	"time"

	collmetric "go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/grpc"

	"github.com/YipYap-run/YipYap-FOSS/internal/telemetry"
)

// fakeMetricsService implements the OTLP MetricsService gRPC server.
type fakeMetricsService struct {
	v1.UnimplementedMetricsServiceServer
	received chan *v1.ExportMetricsServiceRequest
}

func (f *fakeMetricsService) Export(_ context.Context, req *v1.ExportMetricsServiceRequest) (*v1.ExportMetricsServiceResponse, error) {
	f.received <- req
	return &v1.ExportMetricsServiceResponse{}, nil
}

// TestOTLPExportIntegration verifies that metrics recorded via the OTEL SDK
// are actually exported to a gRPC OTLP endpoint.
func TestOTLPExportIntegration(t *testing.T) {
	_ = collmetric.Metrics{} // ensure SDK metric package is importable

	// Start a fake OTLP gRPC server.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	fake := &fakeMetricsService{received: make(chan *v1.ExportMetricsServiceRequest, 10)}
	v1.RegisterMetricsServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	endpoint := lis.Addr().String()

	// Set up telemetry pointing at our fake server.
	ctx := context.Background()
	tel, err := telemetry.Setup(ctx, telemetry.SetupOptions{OTLPEndpoint: endpoint, ServiceName: "yipyap-test"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	m, err := telemetry.NewMetrics(tel.Meter)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}

	// Record some metrics.
	m.CheckLatency.Record(ctx, 42.0)
	m.NotificationSent.Add(ctx, 1)
	m.AlertsActive.Add(ctx, 3)

	// Force a flush by shutting down (flushes the periodic reader).
	if err := tel.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// Wait for the export to arrive.
	var gotMetrics []*metricpb.Metric
	timeout := time.After(5 * time.Second)
	for {
		select {
		case req := <-fake.received:
			for _, rm := range req.ResourceMetrics {
				for _, sm := range rm.ScopeMetrics {
					gotMetrics = append(gotMetrics, sm.Metrics...)
				}
			}
			// Check if we have the metrics we need.
			names := make(map[string]bool)
			for _, m := range gotMetrics {
				names[m.Name] = true
			}
			if names["yipyap.check.latency_ms"] && names["yipyap.notification.sent"] && names["yipyap.alerts.active"] {
				// All metrics received.
				return
			}
		case <-timeout:
			var names []string
			for _, m := range gotMetrics {
				names = append(names, m.Name)
			}
			t.Fatalf("timed out waiting for metrics; got: %v", names)
		}
	}
}
