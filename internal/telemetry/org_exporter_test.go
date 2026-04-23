package telemetry_test

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	v1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/grpc"

	"github.com/YipYap-run/YipYap-FOSS/internal/telemetry"
)

// mockOrgSettings is an in-memory mock of telemetry.OrgSettingsReader.
type mockOrgSettings struct {
	mu   sync.RWMutex
	data map[string]map[string]string // org_id -> key -> value
}

func newMockSettings() *mockOrgSettings {
	return &mockOrgSettings{data: make(map[string]map[string]string)}
}

func (m *mockOrgSettings) Get(_ context.Context, orgID, key string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if org, ok := m.data[orgID]; ok {
		if v, ok := org[key]; ok {
			return v, nil
		}
	}
	return "", fmt.Errorf("not found")
}

func (m *mockOrgSettings) GetAll(_ context.Context, orgID string) (map[string]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]string)
	if org, ok := m.data[orgID]; ok {
		for k, v := range org {
			result[k] = v
		}
	}
	return result, nil
}

func (m *mockOrgSettings) set(orgID, key, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.data[orgID]; !ok {
		m.data[orgID] = make(map[string]string)
	}
	m.data[orgID][key] = value
}

func (m *mockOrgSettings) del(orgID, key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if org, ok := m.data[orgID]; ok {
		delete(org, key)
	}
}

// startFakeOTLP starts a fake OTLP gRPC metrics server and returns its
// address and a channel that receives exported metric names.
func startFakeOTLP(t *testing.T) (string, *fakeMetricsService) {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	srv := grpc.NewServer()
	fake := &fakeMetricsService{received: make(chan *v1.ExportMetricsServiceRequest, 100)}
	v1.RegisterMetricsServiceServer(srv, fake)

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })

	return lis.Addr().String(), fake
}

// collectMetricNames drains the fake server channel and returns all metric
// names received. It waits up to initialWait for the first message, then
// drains any remaining messages that arrive within 200ms of silence.
func collectMetricNames(fake *fakeMetricsService, initialWait time.Duration) map[string]bool {
	names := make(map[string]bool)
	drain := func(timeout time.Duration) bool {
		select {
		case req := <-fake.received:
			for _, rm := range req.ResourceMetrics {
				for _, sm := range rm.ScopeMetrics {
					for _, m := range sm.Metrics {
						names[m.Name] = true
					}
				}
			}
			return true
		case <-time.After(timeout):
			return false
		}
	}
	// Wait for the first batch.
	if !drain(initialWait) {
		return names
	}
	// Drain remaining with a short timeout.
	for drain(200 * time.Millisecond) {
	}
	return names
}


func TestOrgExporterIsolation(t *testing.T) {
	ctx := context.Background()

	// 1. Start a fake OTLP gRPC server.
	endpoint, fake := startFakeOTLP(t)

	// 2. Create OrgExporterManager with mock settings.
	settings := newMockSettings()
	noopDecrypt := func(s string) (string, error) { return s, nil }
	manager := telemetry.NewOrgExporterManager(settings, noopDecrypt)
	t.Cleanup(func() { _ = manager.Stop(ctx) })

	// 3. Configure "test-org" with the fake server endpoint.
	settings.set("test-org", "otel_endpoint", endpoint)

	// 4. SyncOrg("test-org") -- creates exporter.
	if err := manager.SyncOrg(ctx, "test-org"); err != nil {
		t.Fatalf("SyncOrg(test-org): %v", err)
	}

	// 5. Get the per-org meter.
	meter := manager.GetMeter("test-org")
	if meter == nil {
		t.Fatal("GetMeter(test-org) returned nil; expected a meter")
	}

	// 6. Create a counter and record values.
	counter, err := meter.Int64Counter("test.org.counter")
	if err != nil {
		t.Fatalf("Int64Counter: %v", err)
	}
	counter.Add(ctx, 42)

	// 7. Flush by shutting down the manager (triggers provider flush).
	if err := manager.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// 8. Assert: fake server received the metric.
	names := collectMetricNames(fake, 5*time.Second)
	if !names["test.org.counter"] {
		t.Fatalf("expected metric 'test.org.counter' to be exported; got: %v", names)
	}

	// 9. Assert: GetMeter("other-org") returns nil (no config => isolation).
	if m := manager.GetMeter("other-org"); m != nil {
		t.Fatal("GetMeter(other-org) should return nil for unconfigured org")
	}
}

func TestOrgExporterRemoval(t *testing.T) {
	ctx := context.Background()

	endpoint, _ := startFakeOTLP(t)

	settings := newMockSettings()
	noopDecrypt := func(s string) (string, error) { return s, nil }
	manager := telemetry.NewOrgExporterManager(settings, noopDecrypt)
	t.Cleanup(func() { _ = manager.Stop(ctx) })

	// Set up and sync.
	settings.set("test-org", "otel_endpoint", endpoint)
	if err := manager.SyncOrg(ctx, "test-org"); err != nil {
		t.Fatalf("SyncOrg: %v", err)
	}
	if meter := manager.GetMeter("test-org"); meter == nil {
		t.Fatal("expected meter after SyncOrg")
	}

	// Remove config and sync again.
	settings.del("test-org", "otel_endpoint")
	if err := manager.SyncOrg(ctx, "test-org"); err != nil {
		t.Fatalf("SyncOrg after removal: %v", err)
	}

	// Meter should now be nil -- exporter was removed.
	if meter := manager.GetMeter("test-org"); meter != nil {
		t.Fatal("expected nil meter after config removal, but got a meter")
	}
}

func TestOrgExporterCrossOrgIsolation(t *testing.T) {
	ctx := context.Background()

	// Two separate fake servers -- one per org.
	endpointA, fakeA := startFakeOTLP(t)
	endpointB, fakeB := startFakeOTLP(t)

	settings := newMockSettings()
	noopDecrypt := func(s string) (string, error) { return s, nil }
	manager := telemetry.NewOrgExporterManager(settings, noopDecrypt)
	t.Cleanup(func() { _ = manager.Stop(ctx) })

	// Configure two orgs pointing at different servers.
	settings.set("org-a", "otel_endpoint", endpointA)
	settings.set("org-b", "otel_endpoint", endpointB)

	if err := manager.SyncOrg(ctx, "org-a"); err != nil {
		t.Fatalf("SyncOrg(org-a): %v", err)
	}
	if err := manager.SyncOrg(ctx, "org-b"); err != nil {
		t.Fatalf("SyncOrg(org-b): %v", err)
	}

	// Record metrics on each org's meter.
	meterA := manager.GetMeter("org-a")
	meterB := manager.GetMeter("org-b")
	if meterA == nil || meterB == nil {
		t.Fatal("expected both meters to be non-nil")
	}

	counterA, _ := meterA.Int64Counter("metric.from.org_a")
	counterB, _ := meterB.Int64Counter("metric.from.org_b")
	counterA.Add(ctx, 1)
	counterB.Add(ctx, 1)

	// Flush.
	if err := manager.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Verify server A got org-a's metric and NOT org-b's.
	namesA := collectMetricNames(fakeA, 5*time.Second)
	if !namesA["metric.from.org_a"] {
		t.Fatalf("server A should have received metric.from.org_a; got: %v", namesA)
	}
	if namesA["metric.from.org_b"] {
		t.Fatalf("server A should NOT have received metric.from.org_b (cross-org leak)")
	}

	// Verify server B got org-b's metric and NOT org-a's.
	namesB := collectMetricNames(fakeB, 5*time.Second)
	if !namesB["metric.from.org_b"] {
		t.Fatalf("server B should have received metric.from.org_b; got: %v", namesB)
	}
	if namesB["metric.from.org_a"] {
		t.Fatalf("server B should NOT have received metric.from.org_a (cross-org leak)")
	}
}
