//go:build knative

// Package knative (ContainerSource side): verifies that the yipyap-knative-source
// poll pipeline runs end-to-end against a real in-process yipyap HTTP server
// and a real in-process sink. The bus, publisher, poll handler, PollClient,
// and SinkClient are all the production implementations, the only thing
// missing is the Kubernetes pod wrapper around them. That gives us a test
// that exercises almost every line of the binary's data path without the
// flakiness of a kind-cluster round-trip.
//
// All tests here are gated on the `knative` build tag AND the KNATIVE=1 env
// var, matching the rest of this harness.
package knative

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/oklog/ulid/v2"

	"github.com/YipYap-run/knative-source/cmd/receive-adapter/source"
	"github.com/YipYap-run/YipYap-FOSS/internal/api"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	cepub "github.com/YipYap-run/YipYap-FOSS/internal/cloudevents"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// containerSourceHarness bundles the pieces every ContainerSource E2E test
// wires up:
//   - the yipyap HTTP server (poll handler reachable at /api/v1/cloudevents/poll)
//   - the in-memory bus publisher events land on
//   - a captive sink server that records every inbound CloudEvent delivery
//   - a seeded org + API key for auth against the poll endpoint
//
// Tests fire events onto h.bus, construct a real PollClient + SinkClient
// pointed at the two servers, and assert on the captured sink deliveries.
type containerSourceHarness struct {
	t      *testing.T
	bus    bus.Bus
	yipyap *httptest.Server
	sink   *httptest.Server
	org    *domain.Org
	apiKey string

	sinkMu   sync.Mutex
	sinkHdrs []http.Header
	sinkBody [][]byte
}

// newContainerSourceHarness spins up everything a single ContainerSource test
// needs. Cleanup is registered via t.Cleanup so tests don't have to unwind
// manually. The sink handler records every request's headers + body behind a
// mutex so assertions can run after ctx cancels the client loops.
func newContainerSourceHarness(t *testing.T) *containerSourceHarness {
	t.Helper()

	// Unlike outbound_test.go these tests are fully in-process, no kind
	// cluster required. Just gate on KNATIVE=1 so they don't run under the
	// default `go test ./...` sweep.
	if os.Getenv("KNATIVE") != "1" {
		t.Skip("KNATIVE=1 not set; skipping ContainerSource integration test")
	}

	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	msgBus := bus.NewChannel()
	t.Cleanup(func() { _ = msgBus.Close() })

	hasher := auth.NewAPIKeyHasher([]byte("cs-test-hasher-secret"))
	jwt := auth.NewJWTIssuer([]byte("cs-test-jwt-secret"), time.Hour)

	handler := api.NewServer(s, jwt, msgBus, nil, nil, "", "", api.ServerOptions{
		APIKeyHasher: hasher,
	})
	yipyapTS := httptest.NewServer(handler)
	t.Cleanup(yipyapTS.Close)

	ctx := context.Background()
	org := &domain.Org{
		ID:   "cs-org-" + ulid.Make().String(),
		Name: "ContainerSource Test Org",
		Slug: strings.ToLower("cs-slug-" + ulid.Make().String()),
	}
	if err := s.Orgs().Create(ctx, org); err != nil {
		t.Fatalf("create org: %v", err)
	}
	plaintext, hash, prefix, err := hasher.GenerateAPIKey()
	if err != nil {
		t.Fatalf("generate api key: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.APIKeys().Create(ctx, &domain.APIKey{
		ID:        "key-" + ulid.Make().String(),
		OrgID:     org.ID,
		Name:      "cs test key",
		KeyHash:   hash,
		Prefix:    prefix,
		Scopes:    []string{"monitors:read"},
		CreatedBy: "cs-test",
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("create api key: %v", err)
	}

	h := &containerSourceHarness{
		t:      t,
		bus:    msgBus,
		yipyap: yipyapTS,
		org:    org,
		apiKey: plaintext,
	}

	sinkTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 0)
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			if n > 0 {
				body = append(body, buf[:n]...)
			}
			if err != nil {
				break
			}
		}
		h.sinkMu.Lock()
		h.sinkHdrs = append(h.sinkHdrs, r.Header.Clone())
		h.sinkBody = append(h.sinkBody, body)
		h.sinkMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(sinkTS.Close)
	h.sink = sinkTS

	return h
}

// fireEvent publishes a CloudEvent of the given type onto h.bus for h.org,
// using the same subject scheme as cepub.Publisher so the poll handler's
// durable consumer picks it up. Returns the event so tests can assert on
// the ce-id they see at the sink.
func (h *containerSourceHarness) fireEvent(ceType string) ce.Event {
	h.t.Helper()
	ev := ce.NewEvent()
	ev.SetID(ulid.Make().String())
	ev.SetType(ceType)
	ev.SetSource("containersource-test")
	ev.SetSpecVersion("1.0")
	if err := ev.SetData(ce.ApplicationJSON, map[string]string{"k": "v"}); err != nil {
		h.t.Fatalf("set data: %v", err)
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		h.t.Fatalf("marshal: %v", err)
	}
	subject := cepub.SubjectFor(h.org.ID, ev.Type())
	if err := h.bus.PublishDurable(context.Background(), subject, payload, ev.ID()); err != nil {
		h.t.Fatalf("publish: %v", err)
	}
	return ev
}

// primeConsumer issues a priming poll so the handler's durable consumer is
// subscribed to the bus BEFORE we publish. ChannelBus is fire-and-forget,
// messages published without an active subscriber are dropped on the floor.
// The real JetStream-backed bus doesn't have this problem; this priming
// call is a harness limitation, not a production path.
func (h *containerSourceHarness) primeConsumer(filter string) {
	h.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, h.yipyap.URL+"/api/v1/cloudevents/poll", nil)
	if filter != "" {
		q := req.URL.Query()
		q.Set("filter", filter)
		req.URL.RawQuery = q.Encode()
	}
	req.Header.Set("Authorization", "Bearer "+h.apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("prime: %v", err)
	}
	_ = resp.Body.Close()
}

// waitForSink waits up to timeout for the sink to record at least want
// deliveries. Returns a snapshot of the captured headers and bodies.
func (h *containerSourceHarness) waitForSink(want int, timeout time.Duration) ([]http.Header, [][]byte) {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		h.sinkMu.Lock()
		n := len(h.sinkHdrs)
		h.sinkMu.Unlock()
		if n >= want {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.sinkMu.Lock()
	defer h.sinkMu.Unlock()
	hdrs := make([]http.Header, len(h.sinkHdrs))
	copy(hdrs, h.sinkHdrs)
	bodies := make([][]byte, len(h.sinkBody))
	copy(bodies, h.sinkBody)
	return hdrs, bodies
}

// newSourceConfig builds a source.Config wired against the harness endpoints
// with fast tick intervals suitable for tests. Callers mutate specific
// fields (CEOverrides, EventFilter) before constructing the clients.
func (h *containerSourceHarness) newSourceConfig(t *testing.T) *source.Config {
	t.Helper()
	return &source.Config{
		Sink:         h.sink.URL,
		APIKey:       h.apiKey,
		BaseURL:      h.yipyap.URL,
		PollInterval: 50 * time.Millisecond,
		CursorPath:   filepath.Join(t.TempDir(), "cursor"),
		CEOverrides:  map[string]string{},
	}
}

// runPoll wires a real PollClient + SinkClient with the supplied cfg and
// runs one poll iteration. The loop exits as soon as the sink has captured
// `want` deliveries or timeout elapses, whichever first.
func (h *containerSourceHarness) runPoll(t *testing.T, cfg *source.Config, want int, timeout time.Duration) {
	t.Helper()

	sink := source.NewSinkClient(cfg)
	poller := source.NewPollClient(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Watchdog: once sink has `want` deliveries, cancel the ctx so PollClient
	// returns promptly. This is the same cancel-on-quota pattern used by the
	// stream tests in internal/source.
	go func() {
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			h.sinkMu.Lock()
			n := len(h.sinkHdrs)
			h.sinkMu.Unlock()
			if n >= want {
				cancel()
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	emit := func(ctx context.Context, ev ce.Event) error {
		return sink.Send(ctx, ev)
	}
	_ = poller.Run(ctx, emit)
}

// TestKnative_ContainerSource_PollE2E: publish 3 CloudEvents onto the
// internal bus; the real PollClient fetches them from the real yipyap HTTP
// handler; the real SinkClient POSTs each to the sink in binary mode; the
// sink records 3 deliveries with correct ce-id / ce-type and no leaked auth
// header (poll-mode sink doesn't authenticate by default).
func TestKnative_ContainerSource_PollE2E(t *testing.T) {
	h := newContainerSourceHarness(t)

	h.primeConsumer("")

	fired := make([]ce.Event, 0, 3)
	fired = append(fired, h.fireEvent("run.yipyap.alert.firing"))
	fired = append(fired, h.fireEvent("run.yipyap.alert.resolved"))
	fired = append(fired, h.fireEvent("run.yipyap.monitor.up"))
	time.Sleep(100 * time.Millisecond)

	cfg := h.newSourceConfig(t)
	h.runPoll(t, cfg, 3, 10*time.Second)

	hdrs, _ := h.waitForSink(3, 2*time.Second)
	if len(hdrs) != 3 {
		t.Fatalf("sink received %d events, want 3", len(hdrs))
	}

	// Collect ce-id and ce-type sets from what the sink saw.
	gotIDs := map[string]bool{}
	gotTypes := map[string]bool{}
	for _, h := range hdrs {
		id := h.Get("Ce-Id")
		ty := h.Get("Ce-Type")
		if id == "" || ty == "" {
			t.Errorf("missing ce-id or ce-type on sink delivery: %v", h)
		}
		gotIDs[id] = true
		gotTypes[ty] = true
		// Poll-mode sink doesn't auth in this harness. Confirm no bearer
		// leaked through the emit path, this is a regression-guard for
		// a bug we fixed in 3.6 where the poll Authorization header could
		// be cloned onto the sink request.
		if v := h.Get("Authorization"); v != "" {
			t.Errorf("sink delivery carried unexpected Authorization header: %q", v)
		}
	}
	for _, ev := range fired {
		if !gotIDs[ev.ID()] {
			t.Errorf("sink missing ce-id %s", ev.ID())
		}
		if !gotTypes[ev.Type()] {
			t.Errorf("sink missing ce-type %s", ev.Type())
		}
	}
}

// TestKnative_ContainerSource_CEOverridesApplied: configure CEOverrides on
// the source Config; fire one event; assert the sink's inbound request has
// the override as a Ce-<Extension> header. Mirrors the Knative
// K_CE_OVERRIDES contract in a real round-trip.
func TestKnative_ContainerSource_CEOverridesApplied(t *testing.T) {
	h := newContainerSourceHarness(t)
	h.primeConsumer("")

	h.fireEvent("run.yipyap.alert.firing")
	time.Sleep(100 * time.Millisecond)

	cfg := h.newSourceConfig(t)
	cfg.CEOverrides = map[string]string{"environment": "test"}

	h.runPoll(t, cfg, 1, 10*time.Second)

	hdrs, _ := h.waitForSink(1, 2*time.Second)
	if len(hdrs) != 1 {
		t.Fatalf("sink received %d events, want 1", len(hdrs))
	}
	if got := hdrs[0].Get("Ce-Environment"); got != "test" {
		t.Errorf("Ce-Environment = %q, want %q", got, "test")
	}
}

// TestKnative_ContainerSource_FilterGlob: publish 2 alert events + 1
// monitor event. With YIPYAP_EVENT_FILTER=run.yipyap.alert.* set on the
// source Config, only the two alert events should reach the sink.
func TestKnative_ContainerSource_FilterGlob(t *testing.T) {
	h := newContainerSourceHarness(t)
	h.primeConsumer("run.yipyap.alert.*")

	h.fireEvent("run.yipyap.alert.firing")
	h.fireEvent("run.yipyap.alert.resolved")
	h.fireEvent("run.yipyap.monitor.up")
	time.Sleep(150 * time.Millisecond)

	cfg := h.newSourceConfig(t)
	cfg.EventFilter = "run.yipyap.alert.*"

	// Runs until cancel: we expect 2 deliveries, so after the watchdog sees
	// 2 it cancels. We deliberately set a short upper bound so an errant
	// 3rd delivery would show up in a grace-period re-check below.
	h.runPoll(t, cfg, 2, 3*time.Second)

	// Short grace period to catch a hypothetical runaway 3rd delivery.
	time.Sleep(200 * time.Millisecond)

	hdrs, _ := h.waitForSink(2, 500*time.Millisecond)
	if len(hdrs) != 2 {
		t.Fatalf("sink received %d events, want 2 (filter should drop the monitor event)", len(hdrs))
	}
	for _, hd := range hdrs {
		ty := hd.Get("Ce-Type")
		if !strings.HasPrefix(ty, "run.yipyap.alert.") {
			t.Errorf("non-alert event slipped through filter: %s", ty)
		}
	}
}

// TestKnative_ContainerSource_CursorResume: publish 2 events, run the
// poller once until both reach the sink; assert the cursor file exists.
// Then publish a 3rd event, rerun with the same cursor path, and assert
// the sink receives exactly the 3rd event (no duplicates of the first 2).
func TestKnative_ContainerSource_CursorResume(t *testing.T) {
	h := newContainerSourceHarness(t)
	h.primeConsumer("")

	h.fireEvent("run.yipyap.alert.firing")
	h.fireEvent("run.yipyap.alert.resolved")
	time.Sleep(100 * time.Millisecond)

	cfg := h.newSourceConfig(t)
	// Share the same cursor path across both phases.
	h.runPoll(t, cfg, 2, 5*time.Second)

	// Assert cursor file exists + has content.
	b, err := os.ReadFile(cfg.CursorPath)
	if err != nil {
		t.Fatalf("cursor file after first run: %v", err)
	}
	if len(b) == 0 {
		t.Fatalf("cursor file is empty after first run")
	}

	// Snapshot sink count; publish third event; rerun and wait for it.
	h.sinkMu.Lock()
	firstRunCount := len(h.sinkHdrs)
	h.sinkMu.Unlock()
	if firstRunCount != 2 {
		t.Fatalf("first-run sink count = %d, want 2", firstRunCount)
	}

	thirdEv := h.fireEvent("run.yipyap.alert.firing")
	time.Sleep(100 * time.Millisecond)

	// Rerun with same CursorPath.
	h.runPoll(t, cfg, 3, 5*time.Second)

	hdrs, _ := h.waitForSink(3, 2*time.Second)
	if len(hdrs) != 3 {
		t.Fatalf("cumulative sink count after second run = %d, want 3", len(hdrs))
	}
	// The third delivery should be our new event, not a redelivery of the
	// first two. The poll handler advances its durable consumer on each
	// drain so the second run sees only new messages; this is the contract
	// we're validating for at-least-once + no-dupes happy path.
	if got := hdrs[2].Get("Ce-Id"); got != thirdEv.ID() {
		t.Errorf("third delivery ce-id = %q, want %q (new event only)", got, thirdEv.ID())
	}
}
