package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/oklog/ulid/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/api"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// setupCloudEventIngestTest spins up an API server and creates a monitor with
// a known integration key.
func setupCloudEventIngestTest(t *testing.T, integrationKey string) (*httptest.Server, *sqlite.SQLiteStore, *domain.Monitor) {
	t.Helper()
	ts, s, _, m := setupCloudEventIngestTestWithOrg(t, integrationKey)
	return ts, s, m
}

// setupCloudEventIngestTestWithOrg is like setupCloudEventIngestTest but also
// returns the seeded org (tests that drive OrgSettings need the org ID).
func setupCloudEventIngestTestWithOrg(t *testing.T, integrationKey string) (*httptest.Server, *sqlite.SQLiteStore, *domain.Org, *domain.Monitor) {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	msgBus := bus.NewChannel()
	t.Cleanup(func() { _ = msgBus.Close() })

	jwt := auth.NewJWTIssuer([]byte("test-secret-key-for-ingest"), 1*time.Hour)
	handler := api.NewServer(s, jwt, msgBus, nil, nil, "", "", api.ServerOptions{})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	org := &domain.Org{
		ID:   "org-ingest-" + ulid.Make().String(),
		Name: "Ingest Test Org",
		Slug: strings.ToLower("slug-" + ulid.Make().String()),
	}
	if err := s.Orgs().Create(ctx, org); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	m := &domain.Monitor{
		ID:              "mon-ingest-" + ulid.Make().String(),
		OrgID:           org.ID,
		Name:            "Ingest Monitor",
		Type:            domain.MonitorHeartbeat,
		Config:          json.RawMessage(`{}`),
		IntervalSeconds: 60,
		TimeoutSeconds:  10,
		IntegrationKey:  integrationKey,
		Enabled:         true,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.Monitors().Create(ctx, m); err != nil {
		t.Fatal(err)
	}
	return ts, s, org, m
}

func countChecks(t *testing.T, s *sqlite.SQLiteStore, monitorID string) int {
	t.Helper()
	checks, err := s.Checks().ListByMonitor(context.Background(), monitorID, store.CheckFilter{ListParams: store.ListParams{Limit: 100}})
	if err != nil {
		t.Fatalf("list checks: %v", err)
	}
	return len(checks)
}

// getChecks returns all checks for a monitor (most-recent-first order from ListByMonitor).
func getChecks(t *testing.T, s *sqlite.SQLiteStore, monitorID string) []*domain.MonitorCheck {
	t.Helper()
	checks, err := s.Checks().ListByMonitor(context.Background(), monitorID, store.CheckFilter{ListParams: store.ListParams{Limit: 100}})
	if err != nil {
		t.Fatalf("list checks: %v", err)
	}
	return checks
}

func TestIngest_BinaryModeHeartbeat(t *testing.T) {
	key := "ik-bin-hb"
	ts, s, m := setupCloudEventIngestTest(t, key)

	body := []byte(`{"note":"hello from synth"}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/cloudevents/ingest/"+key, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Ce-Specversion", "1.0")
	req.Header.Set("Ce-Id", ulid.Make().String())
	req.Header.Set("Ce-Source", "https://example.com/synth")
	req.Header.Set("Ce-Type", "run.yipyap.ingest.heartbeat.v1")
	req.Header.Set("Ce-Time", time.Now().UTC().Format(time.RFC3339Nano))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(b))
	}
	if n := countChecks(t, s, m.ID); n != 1 {
		t.Fatalf("expected 1 check, got %d", n)
	}
	checks := getChecks(t, s, m.ID)
	if checks[0].Status != domain.StatusUp {
		t.Fatalf("expected StatusUp, got %s", checks[0].Status)
	}
}

func TestIngest_StructuredModeHeartbeat(t *testing.T) {
	key := "ik-struct-hb"
	ts, s, m := setupCloudEventIngestTest(t, key)

	ev := ce.NewEvent()
	ev.SetSpecVersion("1.0")
	ev.SetID(ulid.Make().String())
	ev.SetSource("https://example.com/synth")
	ev.SetType("run.yipyap.ingest.heartbeat.v1")
	ev.SetTime(time.Now().UTC())
	_ = ev.SetData(ce.ApplicationJSON, map[string]string{"note": "structured"})
	envelope, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(ts.URL+"/api/v1/cloudevents/ingest/"+key, "application/cloudevents+json", bytes.NewReader(envelope))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(b))
	}
	if n := countChecks(t, s, m.ID); n != 1 {
		t.Fatalf("expected 1 check, got %d", n)
	}
}

func TestIngest_BatchedMode(t *testing.T) {
	key := "ik-batch"
	ts, s, m := setupCloudEventIngestTest(t, key)

	mkEvent := func() ce.Event {
		ev := ce.NewEvent()
		ev.SetSpecVersion("1.0")
		ev.SetID(ulid.Make().String())
		ev.SetSource("https://example.com/synth")
		ev.SetType("run.yipyap.ingest.heartbeat.v1")
		ev.SetTime(time.Now().UTC())
		_ = ev.SetData(ce.ApplicationJSON, map[string]string{"note": "n"})
		return ev
	}
	batch := []ce.Event{mkEvent(), mkEvent(), mkEvent()}
	body, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(ts.URL+"/api/v1/cloudevents/ingest/"+key, "application/cloudevents-batch+json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(b))
	}
	if n := countChecks(t, s, m.ID); n != 3 {
		t.Fatalf("expected 3 checks, got %d", n)
	}
}

// TestIngest_BatchedMode_OverLimitRejected is the H-17 regression. A
// batch of 257 events (cap+1) must be rejected with 413. Without the
// cap, an attacker could pack 4 MiB of minimal envelopes into a single
// request and drive ~40k DB round trips from one call.
func TestIngest_BatchedMode_OverLimitRejected(t *testing.T) {
	key := "ik-batch-overflow"
	ts, _, _ := setupCloudEventIngestTest(t, key)

	mkEvent := func() ce.Event {
		ev := ce.NewEvent()
		ev.SetSpecVersion("1.0")
		ev.SetID(ulid.Make().String())
		ev.SetSource("https://example.com/synth")
		ev.SetType("run.yipyap.ingest.heartbeat.v1")
		ev.SetTime(time.Now().UTC())
		_ = ev.SetData(ce.ApplicationJSON, map[string]string{"n": "1"})
		return ev
	}
	// 257 events = cap (256) + 1. Use minimal envelopes so the 4 MiB
	// body cap doesn't fire first.
	batch := make([]ce.Event, 0, 257)
	for i := 0; i < 257; i++ {
		batch = append(batch, mkEvent())
	}
	body, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(ts.URL+"/api/v1/cloudevents/ingest/"+key, "application/cloudevents-batch+json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 413 for oversize batch, got %d: %s", resp.StatusCode, string(b))
	}
}

// TestIngest_BatchedMode_AtLimitAccepted confirms the cap-boundary case:
// exactly 256 events (cap) must still be accepted.
func TestIngest_BatchedMode_AtLimitAccepted(t *testing.T) {
	key := "ik-batch-boundary"
	ts, s, m := setupCloudEventIngestTest(t, key)

	mkEvent := func() ce.Event {
		ev := ce.NewEvent()
		ev.SetSpecVersion("1.0")
		ev.SetID(ulid.Make().String())
		ev.SetSource("https://example.com/synth")
		ev.SetType("run.yipyap.ingest.heartbeat.v1")
		ev.SetTime(time.Now().UTC())
		_ = ev.SetData(ce.ApplicationJSON, map[string]string{"n": "1"})
		return ev
	}
	batch := make([]ce.Event, 0, 256)
	for i := 0; i < 256; i++ {
		batch = append(batch, mkEvent())
	}
	body, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(ts.URL+"/api/v1/cloudevents/ingest/"+key, "application/cloudevents-batch+json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200 for at-cap batch, got %d: %s", resp.StatusCode, string(b))
	}
	// countChecks lists only the first 100 (test helper convenience);
	// a non-zero count is sufficient to confirm the at-cap batch wasn't
	// rejected and ran the full apply loop.
	if n := countChecks(t, s, m.ID); n == 0 {
		t.Fatalf("expected non-zero checks for at-cap batch, got 0")
	}
}

func TestIngest_BadToken(t *testing.T) {
	ts, _, _ := setupCloudEventIngestTest(t, "ik-bad-token")

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/cloudevents/ingest/nope-not-real", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Ce-Specversion", "1.0")
	req.Header.Set("Ce-Id", ulid.Make().String())
	req.Header.Set("Ce-Source", "https://example.com/synth")
	req.Header.Set("Ce-Type", "run.yipyap.ingest.heartbeat.v1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestIngest_BadSpecVersion(t *testing.T) {
	key := "ik-bad-spec"
	ts, _, _ := setupCloudEventIngestTest(t, key)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/cloudevents/ingest/"+key, bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Ce-Specversion", "0.3")
	req.Header.Set("Ce-Id", ulid.Make().String())
	req.Header.Set("Ce-Source", "https://example.com/synth")
	req.Header.Set("Ce-Type", "run.yipyap.ingest.heartbeat.v1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestIngest_UnknownType(t *testing.T) {
	key := "ik-unknown-type"
	ts, _, _ := setupCloudEventIngestTest(t, key)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/cloudevents/ingest/"+key, bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Ce-Specversion", "1.0")
	req.Header.Set("Ce-Id", ulid.Make().String())
	req.Header.Set("Ce-Source", "https://example.com/synth")
	req.Header.Set("Ce-Type", "random.event.v1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", resp.StatusCode)
	}
}

func TestIngest_IdempotentDuplicate(t *testing.T) {
	key := "ik-idempotent"
	ts, s, m := setupCloudEventIngestTest(t, key)

	eventID := ulid.Make().String()
	doOnce := func() *http.Response {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/cloudevents/ingest/"+key, bytes.NewReader([]byte(`{"note":"once"}`)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Ce-Specversion", "1.0")
		req.Header.Set("Ce-Id", eventID)
		req.Header.Set("Ce-Source", "https://example.com/synth")
		req.Header.Set("Ce-Type", "run.yipyap.ingest.heartbeat.v1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	resp1 := doOnce()
	_ = resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first call expected 200, got %d", resp1.StatusCode)
	}
	resp2 := doOnce()
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("duplicate call expected 200, got %d", resp2.StatusCode)
	}
	if n := countChecks(t, s, m.ID); n != 1 {
		t.Fatalf("expected 1 check after duplicate, got %d", n)
	}
}

func TestIngest_StatusEvent_Down(t *testing.T) {
	key := "ik-status-down"
	ts, s, m := setupCloudEventIngestTest(t, key)

	body := []byte(`{"status":"down","message":"server on fire"}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/cloudevents/ingest/"+key, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Ce-Specversion", "1.0")
	req.Header.Set("Ce-Id", ulid.Make().String())
	req.Header.Set("Ce-Source", "https://example.com/synth")
	req.Header.Set("Ce-Type", "run.yipyap.ingest.status.v1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(b))
	}
	checks := getChecks(t, s, m.ID)
	if len(checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(checks))
	}
	if checks[0].Status != domain.StatusDown {
		t.Fatalf("expected StatusDown, got %s", checks[0].Status)
	}
}

func TestIngest_StatusEvent_InvalidStatus(t *testing.T) {
	key := "ik-status-invalid"
	ts, _, _ := setupCloudEventIngestTest(t, key)

	body := []byte(`{"status":"maybe"}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/cloudevents/ingest/"+key, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Ce-Specversion", "1.0")
	req.Header.Set("Ce-Id", ulid.Make().String())
	req.Header.Set("Ce-Source", "https://example.com/synth")
	req.Header.Set("Ce-Type", "run.yipyap.ingest.status.v1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestIngest_Oversize(t *testing.T) {
	key := "ik-oversize"
	ts, _, _ := setupCloudEventIngestTest(t, key)

	// 5 MiB of junk inside a JSON string field.
	big := bytes.Repeat([]byte("a"), 5<<20)
	body := append(append([]byte(`{"note":"`), big...), []byte(`"}`)...)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/cloudevents/ingest/"+key, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Ce-Specversion", "1.0")
	req.Header.Set("Ce-Id", ulid.Make().String())
	req.Header.Set("Ce-Source", "https://example.com/synth")
	req.Header.Set("Ce-Type", "run.yipyap.ingest.heartbeat.v1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", resp.StatusCode)
	}
}
