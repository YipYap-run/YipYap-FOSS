package handlers_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/api"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// streamDial opens a GET on /api/v1/cloudevents/stream with the supplied
// bearer and filter. The caller owns the returned cancel func and response;
// resp.Body must be drained line-by-line while the stream is alive.
func streamDial(t *testing.T, h *pollTestHarness, bearer, filter string) (*http.Response, context.CancelFunc) {
	t.Helper()
	var filters []string
	if filter != "" {
		filters = []string{filter}
	}
	return streamDialFilters(t, h, bearer, filters)
}

// streamDialFilters is like streamDial but sends zero or more `filter` query
// params, one Add per value, mirroring a Knative source forwarding every
// entry in spec.filter.types.
func streamDialFilters(t *testing.T, h *pollTestHarness, bearer string, filters []string) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	u := h.ts.URL + "/api/v1/cloudevents/stream"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		cancel()
		t.Fatalf("NewRequest: %v", err)
	}
	if len(filters) > 0 {
		q := req.URL.Query()
		for _, f := range filters {
			q.Add("filter", f)
		}
		req.URL.RawQuery = q.Encode()
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	// Use a client with no timeout, the stream is long-lived.
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("Do: %v", err)
	}
	return resp, cancel
}

// readNLines reads up to n newline-delimited lines from r, subject to a
// per-read deadline enforced by ctx. Returns what it got plus the scanner
// error (nil on clean EOF).
func readNLines(t *testing.T, r *http.Response, n int, budget time.Duration) []string {
	t.Helper()
	lines := make([]string, 0, n)
	done := make(chan struct{})
	var scanErr error
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(r.Body)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			lines = append(lines, line)
			if len(lines) >= n {
				return
			}
		}
		scanErr = scanner.Err()
	}()
	select {
	case <-done:
	case <-time.After(budget):
	}
	_ = scanErr
	return lines
}

// TestStream_DeliversEvents: publish 3 events; stream reader receives 3
// NDJSON lines corresponding to the payloads.
func TestStream_DeliversEvents(t *testing.T) {
	h := setupPollTest(t)

	// Prime the durable consumer so publishes are retained.
	resp, cancel := streamDial(t, h, h.apiKey, "")
	// Give the server a moment to register the consumer subscription.
	time.Sleep(100 * time.Millisecond)

	for i := 0; i < 3; i++ {
		publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.alert.firing")
	}

	lines := readNLines(t, resp, 3, 3*time.Second)
	cancel()
	_ = resp.Body.Close()

	if len(lines) != 3 {
		t.Fatalf("want 3 NDJSON lines, got %d: %v", len(lines), lines)
	}
	for i, line := range lines {
		var ev struct {
			Type        string `json:"type"`
			SpecVersion string `json:"specversion"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("line %d: decode: %v (raw=%q)", i, err, line)
		}
		if ev.SpecVersion != "1.0" {
			t.Fatalf("line %d: specversion=%q", i, ev.SpecVersion)
		}
		if ev.Type != "run.yipyap.alert.firing" {
			t.Fatalf("line %d: type=%q", i, ev.Type)
		}
	}
}

// TestStream_FilterGlob: 2 alerts + 1 monitor; filter='run.yipyap.alert.*'
// yields exactly 2 lines.
func TestStream_FilterGlob(t *testing.T) {
	h := setupPollTest(t)

	resp, cancel := streamDial(t, h, h.apiKey, "run.yipyap.alert.*")
	time.Sleep(100 * time.Millisecond)

	publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.alert.firing")
	publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.monitor.up")
	publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.alert.resolved")

	lines := readNLines(t, resp, 2, 3*time.Second)
	cancel()
	_ = resp.Body.Close()

	if len(lines) != 2 {
		t.Fatalf("want 2 filtered lines, got %d: %v", len(lines), lines)
	}
	for _, line := range lines {
		var ev struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("decode: %v (raw=%q)", err, line)
		}
		if !strings.HasPrefix(ev.Type, "run.yipyap.alert.") {
			t.Fatalf("filter bleed: %s", ev.Type)
		}
	}
}

// TestStream_MultiFilterGlob: with two globs, events matching EITHER glob are
// streamed and non-matching types are dropped (server-side OR).
func TestStream_MultiFilterGlob(t *testing.T) {
	h := setupPollTest(t)

	filters := []string{"run.yipyap.alert.*", "run.yipyap.monitor.*"}
	resp, cancel := streamDialFilters(t, h, h.apiKey, filters)
	time.Sleep(100 * time.Millisecond)

	publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.alert.firing")
	publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.incident.opened")
	publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.monitor.up")

	lines := readNLines(t, resp, 2, 3*time.Second)
	cancel()
	_ = resp.Body.Close()

	if len(lines) != 2 {
		t.Fatalf("want 2 filtered lines, got %d: %v", len(lines), lines)
	}
	for _, line := range lines {
		var ev struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("decode: %v (raw=%q)", err, line)
		}
		if !strings.HasPrefix(ev.Type, "run.yipyap.alert.") &&
			!strings.HasPrefix(ev.Type, "run.yipyap.monitor.") {
			t.Fatalf("filter bleed: %s", ev.Type)
		}
	}
}

// TestStream_ClientDisconnect: disconnect client mid-stream; server handler
// must return (no goroutine leak). We assert indirectly by cancelling the
// request context and confirming a subsequent request works: goroutine-leak
// detection here is best-effort. The test's real job is verifying that
// cancel() doesn't leave the server hanging.
func TestStream_ClientDisconnect(t *testing.T) {
	h := setupPollTest(t)

	resp, cancel := streamDial(t, h, h.apiKey, "")
	time.Sleep(100 * time.Millisecond)

	// Publish one event so the stream does at least one write.
	publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.alert.firing")
	_ = readNLines(t, resp, 1, 2*time.Second)

	// Cancel the client context; the server handler should exit promptly.
	cancel()
	_ = resp.Body.Close()

	// Best effort: give the server goroutine time to unwind. If it's hung,
	// later tests in the suite will observe resource contention, we
	// don't have a better knob than that. Confirm another request still
	// works.
	time.Sleep(200 * time.Millisecond)
	resp2, cancel2 := streamDial(t, h, h.apiKey, "")
	defer cancel2()
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("follow-up stream returned %d", resp2.StatusCode)
	}
}

// TestStream_ContentTypeHeader: response Content-Type matches the
// CloudEvents HTTP binding batched-mode content type.
func TestStream_ContentTypeHeader(t *testing.T) {
	h := setupPollTest(t)
	resp, cancel := streamDial(t, h, h.apiKey, "")
	defer cancel()
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	got := resp.Header.Get("Content-Type")
	const want = "application/cloudevents-batch+json"
	if got != want {
		t.Fatalf("Content-Type: got %q, want %q", got, want)
	}
}

// TestStream_Unauthorized: no bearer → 401.
func TestStream_Unauthorized(t *testing.T) {
	h := setupPollTest(t)
	resp, cancel := streamDial(t, h, "", "")
	defer cancel()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
}

// TestStream_SurvivesWriteTimeout (R2-C2 regression) wires the API
// behind a real *http.Server with an aggressive WriteTimeout: production
// main.go pins WriteTimeout=15s, which silently kills a long-lived
// /cloudevents/stream connection at the deadline. The handler's
// SetWriteDeadline(time.Time{}) opt-out must keep the stream alive past
// WriteTimeout.
//
// httptest.NewServer doesn't set timeouts, so this test deliberately
// stands up its own *http.Server with WriteTimeout=300ms and asserts
// that an event published 600ms in (i.e. past the deadline) still
// reaches the client.
func TestStream_SurvivesWriteTimeout(t *testing.T) {
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

	hasher := auth.NewAPIKeyHasher([]byte("write-timeout-test-secret"))
	jwt := auth.NewJWTIssuer([]byte("write-timeout-test-jwt"), time.Hour)

	handler := api.NewServer(s, jwt, msgBus, nil, nil, "", "", api.ServerOptions{
		APIKeyHasher: hasher,
	})

	ctx := context.Background()
	org := &domain.Org{
		ID:   "wt-" + ulid.Make().String(),
		Name: "Write Timeout Test Org",
		Slug: "wt-" + strings.ToLower(ulid.Make().String()),
	}
	if err := s.Orgs().Create(ctx, org); err != nil {
		t.Fatalf("create org: %v", err)
	}
	plaintext, hash, kPrefix, err := hasher.GenerateAPIKey()
	if err != nil {
		t.Fatalf("generate api key: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	apiKey := &domain.APIKey{
		ID:        "key-wt-" + ulid.Make().String(),
		OrgID:     org.ID,
		Name:      "wt key",
		KeyHash:   hash,
		Prefix:    kPrefix,
		Scopes:    []string{"monitors:read"},
		CreatedBy: "user-wt",
		CreatedAt: now,
	}
	if err := s.APIKeys().Create(ctx, apiKey); err != nil {
		t.Fatalf("create api key: %v", err)
	}

	// Real *http.Server with a tight WriteTimeout, mirroring main.go.
	// The reproducer fails (stream dies) without the
	// SetWriteDeadline(time.Time{}) opt-out in the stream handler.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{
		Handler:      handler,
		WriteTimeout: 300 * time.Millisecond,
		ReadTimeout:  5 * time.Second,
		IdleTimeout:  30 * time.Second,
	}
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		_ = srv.Serve(listener)
	}()
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		<-srvDone
	})

	baseURL := "http://" + listener.Addr().String()

	streamCtx, streamCancel := context.WithCancel(context.Background())
	defer streamCancel()
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet,
		baseURL+"/api/v1/cloudevents/stream", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+plaintext)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("stream Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, want 200", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	// Wait LONGER than the server's WriteTimeout to confirm the
	// connection is not torn down. Without the fix, the next Write on
	// the server side fires net.OpError "i/o timeout" and the client
	// observes EOF here.
	time.Sleep(600 * time.Millisecond)

	publishTestEvent(t, msgBus, org.ID, "run.yipyap.alert.firing")

	var line string
	var lineMu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		for scanner.Scan() {
			text := strings.TrimSpace(scanner.Text())
			if text == "" {
				continue
			}
			lineMu.Lock()
			line = text
			lineMu.Unlock()
			return
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("stream timed out before line arrived; the WriteTimeout opt-out is not in effect")
	}

	lineMu.Lock()
	got := line
	lineMu.Unlock()
	if got == "" {
		t.Fatalf("no line read from stream after publish (WriteTimeout killed the connection?)")
	}
	var ev struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(got), &ev); err != nil {
		t.Fatalf("decode line: %v (raw=%q)", err, got)
	}
	if ev.Type != "run.yipyap.alert.firing" {
		t.Fatalf("unexpected event type %q", ev.Type)
	}
}

// TestStream_CrossOrgIsolation: events published on org-2 must not appear
// on an org-1 stream.
func TestStream_CrossOrgIsolation(t *testing.T) {
	h := setupPollTest(t)

	resp1, cancel1 := streamDial(t, h, h.apiKey, "")
	resp2, cancel2 := streamDial(t, h, h.otherKey, "")
	time.Sleep(100 * time.Millisecond)

	// Publish 2 events on the OTHER org.
	publishTestEvent(t, h.msgBus, h.otherOrg.ID, "run.yipyap.alert.firing")
	publishTestEvent(t, h.msgBus, h.otherOrg.ID, "run.yipyap.alert.firing")

	// The other org's stream should see both.
	otherLines := readNLines(t, resp2, 2, 2*time.Second)
	if len(otherLines) != 2 {
		t.Fatalf("other org stream: want 2 lines, got %d", len(otherLines))
	}

	// Org-1's stream should see 0 within the budget. Read briefly; expect
	// the read to time out.
	myLines := readNLines(t, resp1, 1, 500*time.Millisecond)
	if len(myLines) != 0 {
		t.Fatalf("cross-org bleed: got %d lines on org-1 stream", len(myLines))
	}

	cancel1()
	cancel2()
	_ = resp1.Body.Close()
	_ = resp2.Body.Close()
}
