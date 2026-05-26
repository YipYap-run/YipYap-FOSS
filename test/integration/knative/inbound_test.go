//go:build knative

// Package knative (inbound side): verifies that the yipyap ingest endpoint
// accepts CloudEvents shaped exactly as Knative Eventing produces them
// (binary-mode, canonical ce-* headers). For now this is a wire-compatibility
// test rather than a cluster-routed round-trip, the round-trip requires
// stable host↔cluster networking which isn't worth the flakiness for Phase 1.
// Upgrade to a real PingSource-driven round-trip later if the harness is
// stable on CI.
package knative

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// TestIngest_KnativeBinaryModeHeartbeat posts a CloudEvent shaped exactly as
// a Knative Broker → Trigger → sink delivery would produce (binary mode,
// lowercase `ce-*` headers per RFC 7230 header-name case-insensitivity),
// and asserts the yipyap ingest handler records a heartbeat check.
func TestIngest_KnativeBinaryModeHeartbeat(t *testing.T) {
	if os.Getenv("KNATIVE") != "1" {
		t.Skip("KNATIVE=1 required")
	}

	s := newTempStore(t)
	monitor := seedHeartbeatMonitor(t, s, "org-1")
	h := handlers.NewCloudEventHandler(s)

	r := chi.NewRouter()
	r.Post("/api/v1/cloudevents/ingest/{token}", h.Ingest)
	ts := httptest.NewServer(r)
	defer ts.Close()

	dataPayload, _ := json.Marshal(map[string]string{"note": "from knative trigger"})
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		ts.URL+"/api/v1/cloudevents/ingest/"+monitor.IntegrationKey,
		bytes.NewReader(dataPayload),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("ce-specversion", "1.0")
	req.Header.Set("ce-type", types.TypeIngestHeartbeatV1)
	req.Header.Set("ce-id", "01HXKNATIVEHBBBBBBBBBBBBBBB")
	req.Header.Set("ce-source", "https://broker-ingress.knative-eventing.svc.cluster.local/test/default")
	req.Header.Set("ce-time", time.Now().UTC().Format(time.RFC3339Nano))
	req.Header.Set("ce-subject", fmt.Sprintf("monitor/%s", monitor.ID))

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// A check row should now exist for this monitor.
	checks, err := s.Checks().ListByMonitor(context.Background(), monitor.ID, store.CheckFilter{ListParams: store.ListParams{Limit: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) == 0 {
		t.Fatal("no check rows written; ingest handler did not record the heartbeat")
	}
	if checks[0].Status != domain.StatusUp {
		t.Errorf("check status = %s, want up", checks[0].Status)
	}
}

// TestIngest_KnativeStructuredModeStatus posts a structured-mode CloudEvent
// (Content-Type: application/cloudevents+json with the full envelope in the
// body), the shape a Knative Channel Subscription would produce when sending
// to a sink via the Channel abstraction.
func TestIngest_KnativeStructuredModeStatus(t *testing.T) {
	if os.Getenv("KNATIVE") != "1" {
		t.Skip("KNATIVE=1 required")
	}

	s := newTempStore(t)
	monitor := seedHeartbeatMonitor(t, s, "org-1")
	h := handlers.NewCloudEventHandler(s)

	r := chi.NewRouter()
	r.Post("/api/v1/cloudevents/ingest/{token}", h.Ingest)
	ts := httptest.NewServer(r)
	defer ts.Close()

	envelope := map[string]any{
		"specversion":     "1.0",
		"type":            types.TypeIngestStatusV1,
		"id":              "01HXKNATIVESTATUSBBBBBBBBBB",
		"source":          "https://broker-ingress.knative-eventing.svc.cluster.local/test/default",
		"time":            time.Now().UTC().Format(time.RFC3339Nano),
		"subject":         fmt.Sprintf("monitor/%s", monitor.ID),
		"datacontenttype": "application/json",
		"data": map[string]string{
			"status":  "down",
			"message": "external probe says down",
		},
	}
	body, _ := json.Marshal(envelope)

	req, _ := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		ts.URL+"/api/v1/cloudevents/ingest/"+monitor.IntegrationKey,
		bytes.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/cloudevents+json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	checks, err := s.Checks().ListByMonitor(context.Background(), monitor.ID, store.CheckFilter{ListParams: store.ListParams{Limit: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) == 0 {
		t.Fatal("no check rows written")
	}
	if checks[0].Status != domain.StatusDown {
		t.Errorf("check status = %s, want down", checks[0].Status)
	}
}

// newTempStore returns a fresh sqlite store with migrations applied for test
// isolation. One file per test keeps parallelism safe.
func newTempStore(t *testing.T) store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := sqlite.New(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// seedHeartbeatMonitor inserts a heartbeat monitor with a random integration
// key suitable for use as the ingest URL path parameter.
func seedHeartbeatMonitor(t *testing.T, s store.Store, orgID string) *domain.Monitor {
	t.Helper()
	// Org must exist first due to FK.
	_ = s.Orgs().Create(context.Background(), &domain.Org{ID: orgID, Name: "test-org", Plan: domain.PlanFree, CreatedAt: time.Now().UTC()})
	m := &domain.Monitor{
		ID:             "01HXMONITORHEARTBEATBBBBBBBBBB",
		OrgID:          orgID,
		Name:           "test-heartbeat",
		Type:           domain.MonitorHeartbeat,
		Config:         []byte(`{"grace_period_seconds": 60}`),
		IntegrationKey: "test-ingest-token-" + t.Name(),
		Enabled:        true,
		CreatedAt:      time.Now().UTC(),
	}
	if err := s.Monitors().Create(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	return m
}
