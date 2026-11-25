package handlers_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/google/uuid"
	"github.com/oklog/ulid/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	cepub "github.com/YipYap-run/YipYap-FOSS/internal/cloudevents"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// These tests cover the at-least-once cursor contract and the server-side
// severity and monitor_id filters (knative decisions 1 and 4).

// publishData publishes a CloudEvent of the given type and payload.
func publishData(t *testing.T, b bus.Bus, orgID, ceType string, data any) string {
	t.Helper()
	ev := ce.NewEvent()
	ev.SetID(ulid.Make().String())
	ev.SetType(ceType)
	ev.SetSource("delivery-test")
	ev.SetSpecVersion("1.0")
	if err := ev.SetData(ce.ApplicationJSON, data); err != nil {
		t.Fatalf("set data: %v", err)
	}
	if err := cepub.NewPublisher(b).Publish(context.Background(), orgID, ev); err != nil {
		t.Fatalf("publish: %v", err)
	}
	return ev.ID()
}

type pollOpts struct {
	since    string
	sourceID string
	params   url.Values
	bearer   string
}

// pollWith performs one poll and returns the status, event IDs and cursor.
func pollWith(t *testing.T, h *pollTestHarness, o pollOpts) (int, []string, string) {
	t.Helper()
	q := url.Values{}
	for k, vs := range o.params {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	if o.since != "" {
		q.Set("since", o.since)
	}
	req, err := http.NewRequest(http.MethodGet, h.ts.URL+"/api/v1/cloudevents/poll?"+q.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	bearer := o.bearer
	if bearer == "" {
		bearer = h.apiKey
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	if o.sourceID == "" {
		o.sourceID = "delivery-test-client"
	}
	req.Header.Set("X-YipYap-Source-ID", o.sourceID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil, ""
	}
	var body struct {
		Events     []json.RawMessage `json:"events"`
		NextCursor string            `json:"next_cursor"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	ids := make([]string, 0, len(body.Events))
	for _, e := range body.Events {
		ids = append(ids, eventField(t, e, "id"))
	}
	return resp.StatusCode, ids, body.NextCursor
}

func eventField(t *testing.T, raw []byte, field string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	s, _ := m[field].(string)
	return s
}

// sameSet compares IDs ignoring order. The in-process bus delivers each
// publish on its own goroutine, so log order can differ from publish order.
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, x := range a {
		seen[x]++
	}
	for _, x := range b {
		seen[x]--
		if seen[x] < 0 {
			return false
		}
	}
	return true
}

func sameIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPoll_UnconfirmedBatchRedelivered(t *testing.T) {
	h := setupPollTest(t)
	const src = "source-uid-1"

	code, _, c0 := pollWith(t, h, pollOpts{sourceID: src})
	if code != http.StatusOK || c0 == "" {
		t.Fatalf("priming poll: %d cursor %q", code, c0)
	}
	var want []string
	for i := 0; i < 3; i++ {
		want = append(want, publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.alert.fired.v1").ID())
	}
	time.Sleep(100 * time.Millisecond)

	_, got1, c1 := pollWith(t, h, pollOpts{sourceID: src, since: c0})
	if !sameSet(got1, want) {
		t.Fatalf("first poll = %v, want %v", got1, want)
	}
	// The client did not confirm c1: the same cursor gets the same batch.
	_, got2, c2 := pollWith(t, h, pollOpts{sourceID: src, since: c0})
	if !sameIDs(got2, got1) || c2 != c1 {
		t.Fatalf("redelivery = %v (cursor %q), want %v (cursor %q)", got2, c2, got1, c1)
	}
	// A restarted client that lost its cursor resumes from the last
	// confirmed position (c0) and also gets the batch again.
	_, got3, _ := pollWith(t, h, pollOpts{sourceID: src})
	if !sameIDs(got3, got1) {
		t.Fatalf("cursor-less retry = %v, want %v", got3, want)
	}
	// Confirming c1 ends redelivery, with or without the cursor.
	if _, got, _ := pollWith(t, h, pollOpts{sourceID: src, since: c1}); len(got) != 0 {
		t.Fatalf("after confirmation got %v", got)
	}
	if _, got, _ := pollWith(t, h, pollOpts{sourceID: src}); len(got) != 0 {
		t.Fatalf("cursor-less poll after confirmation got %v", got)
	}
}

func TestPoll_EventsCarryCursor(t *testing.T) {
	h := setupPollTest(t)
	_, _, c0 := pollWith(t, h, pollOpts{sourceID: "s"})
	publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.alert.fired.v1")
	publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.alert.fired.v1")
	time.Sleep(100 * time.Millisecond)

	req, _ := http.NewRequest(http.MethodGet, h.ts.URL+"/api/v1/cloudevents/poll?since="+c0, nil)
	req.Header.Set("Authorization", "Bearer "+h.apiKey)
	req.Header.Set("X-YipYap-Source-ID", "s")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Events []json.RawMessage `json:"events"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Events) != 2 {
		t.Fatalf("got %d events, want 2", len(body.Events))
	}
	// Confirming the first event's own cursor redelivers only the second.
	first := eventField(t, body.Events[0], "yipyapcursor")
	second := eventField(t, body.Events[1], "id")
	if first == "" {
		t.Fatal("event has no yipyapcursor extension")
	}
	if _, got, _ := pollWith(t, h, pollOpts{since: first}); !sameIDs(got, []string{second}) {
		t.Fatalf("partial confirmation got %v, want [%s]", got, second)
	}
}

func TestPoll_TwoSourcesEachSeeEveryEvent(t *testing.T) {
	h := setupPollTest(t)
	// Both sources use the same org and filter and send no cursor, so
	// only the source ID tells them apart (knsrc-10).
	pollWith(t, h, pollOpts{sourceID: "staging"})
	pollWith(t, h, pollOpts{sourceID: "prod"})
	var want []string
	for i := 0; i < 4; i++ {
		want = append(want, publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.alert.fired.v1").ID())
	}
	time.Sleep(100 * time.Millisecond)

	_, a, ca := pollWith(t, h, pollOpts{sourceID: "staging"})
	_, b, cb := pollWith(t, h, pollOpts{sourceID: "prod"})
	if !sameSet(a, want) || !sameSet(b, want) {
		t.Fatalf("staging=%v prod=%v, want both %v", a, b, want)
	}
	if ca != cb {
		t.Fatalf("cursors differ for the same log position: %q vs %q", ca, cb)
	}
}

func TestPoll_CursorValidation(t *testing.T) {
	h := setupPollTest(t)
	legacy := base64.RawURLEncoding.EncodeToString([]byte(`{"seq":7}`))
	if code, _, c := pollWith(t, h, pollOpts{since: legacy}); code != http.StatusOK || c == "" {
		t.Fatalf("legacy cursor: %d %q, want 200 with a fresh cursor", code, c)
	}
	for _, bad := range []string{"%%%", "bm90LWpzb24", base64.RawURLEncoding.EncodeToString([]byte(`{"v":9,"e":"x","s":1}`))} {
		if code, _, _ := pollWith(t, h, pollOpts{since: bad}); code != http.StatusBadRequest {
			t.Errorf("since=%q: status %d, want 400", bad, code)
		}
	}
	if code, _, _ := pollWith(t, h, pollOpts{sourceID: "bad id with spaces"}); code != http.StatusBadRequest {
		t.Errorf("bad source id: status %d, want 400", code)
	}
	if code, _, _ := pollWith(t, h, pollOpts{sourceID: strings.Repeat("a", 129)}); code != http.StatusBadRequest {
		t.Errorf("long source id: status %d, want 400", code)
	}
}

// seedMonitor creates a monitor in org and returns its ID.
func seedMonitor(t *testing.T, h *pollTestHarness, orgID string) string {
	t.Helper()
	now := time.Now().UTC()
	m := &domain.Monitor{
		ID: uuid.New().String(), OrgID: orgID, Name: "M", Type: domain.MonitorHTTP,
		Config: []byte(`{"url":"https://example.com"}`), IntervalSeconds: 60, TimeoutSeconds: 10,
		Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := h.store.Monitors().Create(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	return m.ID
}

func seedAlert(t *testing.T, h *pollTestHarness, monitorID string, sev domain.Severity) string {
	t.Helper()
	a := &domain.Alert{
		ID: uuid.New().String(), MonitorID: monitorID, OrgID: h.org.ID,
		Status: domain.AlertFiring, Severity: sev, StartedAt: time.Now().UTC(),
	}
	if err := h.store.Alerts().Create(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	return a.ID
}

func TestPoll_SeverityFilter(t *testing.T) {
	h := setupPollTest(t)
	mon := seedMonitor(t, h, h.org.ID)
	// One active alert per monitor, so each alert gets its own.
	critAlert := seedAlert(t, h, seedMonitor(t, h, h.org.ID), domain.SeverityCritical)
	warnAlert := seedAlert(t, h, seedMonitor(t, h, h.org.ID), domain.SeverityWarning)
	params := url.Values{"severity": {"critical"}}

	_, _, c0 := pollWith(t, h, pollOpts{params: params})
	fired := func(sev string) string {
		return publishData(t, h.msgBus, h.org.ID, "run.yipyap.alert.fired.v1",
			map[string]string{"alert_id": uuid.New().String(), "monitor_id": mon, "severity": sev})
	}
	wantCrit := fired("critical")
	fired("warning")
	fired("info")
	// Acknowledged events carry no severity; the alert's is looked up.
	wantAck := publishData(t, h.msgBus, h.org.ID, "run.yipyap.alert.acknowledged.v1",
		map[string]string{"alert_id": critAlert, "monitor_id": mon})
	publishData(t, h.msgBus, h.org.ID, "run.yipyap.alert.acknowledged.v1",
		map[string]string{"alert_id": warnAlert, "monitor_id": mon})
	// Severity does not apply to monitor events.
	wantDown := publishData(t, h.msgBus, h.org.ID, "run.yipyap.monitor.down.v1",
		map[string]string{"monitor_id": mon})
	time.Sleep(100 * time.Millisecond)

	_, got, _ := pollWith(t, h, pollOpts{params: params, since: c0})
	if want := []string{wantCrit, wantAck, wantDown}; !sameSet(got, want) {
		t.Fatalf("severity=critical got %v, want %v", got, want)
	}

	if code, _, _ := pollWith(t, h, pollOpts{params: url.Values{"severity": {"urgent"}}}); code != http.StatusBadRequest {
		t.Fatalf("unknown severity: status %d, want 400", code)
	}
}

func TestPoll_MonitorIDFilter(t *testing.T) {
	h := setupPollTest(t)
	m1 := seedMonitor(t, h, h.org.ID)
	m2 := seedMonitor(t, h, h.org.ID)
	foreign := seedMonitor(t, h, h.otherOrg.ID)
	params := url.Values{"monitor_id": {m1}}

	_, _, c0 := pollWith(t, h, pollOpts{params: params})
	want1 := publishData(t, h.msgBus, h.org.ID, "run.yipyap.monitor.down.v1", map[string]string{"monitor_id": m1})
	publishData(t, h.msgBus, h.org.ID, "run.yipyap.monitor.down.v1", map[string]string{"monitor_id": m2})
	wantMaint := publishData(t, h.msgBus, h.org.ID, "run.yipyap.maintenance.started.v1",
		map[string]any{"window_id": "w1", "monitors": []string{m2, m1}})
	publishData(t, h.msgBus, h.org.ID, "run.yipyap.ingest.heartbeat.v1", map[string]string{"note": "n"})
	time.Sleep(100 * time.Millisecond)

	_, got, _ := pollWith(t, h, pollOpts{params: params, since: c0})
	if want := []string{want1, wantMaint}; !sameSet(got, want) {
		t.Fatalf("monitor_id=m1 got %v, want %v", got, want)
	}

	for name, id := range map[string]string{"other org": foreign, "unknown": uuid.New().String()} {
		code, _, _ := pollWith(t, h, pollOpts{params: url.Values{"monitor_id": {id}}})
		if code != http.StatusBadRequest {
			t.Errorf("%s monitor: status %d, want 400", name, code)
		}
	}
}

func TestStream_ReconnectWithCursorResumes(t *testing.T) {
	h := setupPollTest(t)
	dial := func(since string) (*http.Response, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		u := h.ts.URL + "/api/v1/cloudevents/stream"
		if since != "" {
			u += "?since=" + url.QueryEscape(since)
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		req.Header.Set("Authorization", "Bearer "+h.apiKey)
		req.Header.Set("X-YipYap-Source-ID", "stream-src")
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			cancel()
			t.Fatalf("stream status %d", resp.StatusCode)
		}
		return resp, cancel
	}

	resp, cancel := dial("")
	time.Sleep(100 * time.Millisecond)
	for i := 0; i < 3; i++ {
		publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.alert.fired.v1")
	}
	lines := readNLines(t, resp, 3, 3*time.Second)
	cancel()
	_ = resp.Body.Close()
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	// The client handled only the first event before the connection
	// dropped. Two more arrive while it is away.
	confirmed := eventField(t, []byte(lines[0]), "yipyapcursor")
	if confirmed == "" {
		t.Fatal("stream event has no yipyapcursor")
	}
	want := []string{eventField(t, []byte(lines[1]), "id"), eventField(t, []byte(lines[2]), "id")}
	for i := 0; i < 2; i++ {
		want = append(want, publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.alert.fired.v1").ID())
	}
	time.Sleep(100 * time.Millisecond)

	resp2, cancel2 := dial(confirmed)
	lines2 := readNLines(t, resp2, 4, 3*time.Second)
	cancel2()
	_ = resp2.Body.Close()
	var got []string
	for _, l := range lines2 {
		got = append(got, eventField(t, []byte(l), "id"))
	}
	if !sameSet(got, want) {
		t.Fatalf("resumed stream got %v, want %v", got, want)
	}
}
