//go:build knative

// Package knative (reply side): exercises the outbound cloudevent_http
// provider end-to-end with a real reply.Dispatcher and the Phase-2 reply
// handlers wired against a sqlite store. A local httptest server stands in
// for the downstream sink and responds with a reply CloudEvent; these tests
// prove that a matching `ce_alertref` produces the expected timeline row on
// the alert.
//
// Like the rest of the knative harness these tests are wire-compatibility
// tests against a local httptest sink, not a real cluster.
package knative

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/oklog/ulid/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	replyhandlers "github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify/providers"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// newReplyStore returns a fresh in-memory sqlite store with migrations
// applied. Each test gets its own store for isolation.
func newReplyStore(t *testing.T) *sqlite.SQLiteStore {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// seedFiringAlert creates an Org, a HTTP monitor, and a firing critical
// alert and returns the alert. Each call generates fresh IDs so tests may
// chain multiple seedings if ever needed.
func seedFiringAlert(t *testing.T, s *sqlite.SQLiteStore) *domain.Alert {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	org := &domain.Org{
		ID:        "org-reply-" + ulid.Make().String(),
		Name:      "knative-reply-test",
		Slug:      "reply-" + ulid.Make().String(),
		Plan:      domain.PlanFree,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.Orgs().Create(ctx, org); err != nil {
		t.Fatalf("Orgs.Create: %v", err)
	}

	mon := &domain.Monitor{
		ID:              "mon-reply-" + ulid.Make().String(),
		OrgID:           org.ID,
		Name:            "reply-monitor",
		Type:            domain.MonitorHTTP,
		Config:          json.RawMessage(`{"url":"https://example.com"}`),
		IntervalSeconds: 60,
		TimeoutSeconds:  10,
		Enabled:         true,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.Monitors().Create(ctx, mon); err != nil {
		t.Fatalf("Monitors.Create: %v", err)
	}

	alert := &domain.Alert{
		ID:        "alert-reply-" + ulid.Make().String(),
		OrgID:     org.ID,
		MonitorID: mon.ID,
		Status:    domain.AlertFiring,
		Severity:  domain.SeverityCritical,
		StartedAt: now,
	}
	if err := s.Alerts().Create(ctx, alert); err != nil {
		t.Fatalf("Alerts.Create: %v", err)
	}
	return alert
}

// buildReplyingSinkServer stands up an httptest server that responds to
// every POST with the reply CloudEvent produced by replyCEBuilder. The
// outbound CE id arrives as the `Ce-Id` header (binary-mode outbound); the
// builder typically uses it as the reply's `alertref` extension.
func buildReplyingSinkServer(t *testing.T, replyCEBuilder func(outboundID string) ce.Event) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outboundID := r.Header.Get("Ce-Id")
		ev := replyCEBuilder(outboundID)
		body, err := json.Marshal(ev)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/cloudevents+json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// buildOutboundAlertFiredJob constructs a realistic outbound alert.fired
// CloudEvent, wraps it as a NotificationJob with a plaintext-encoded
// cloudevent_http channel config targeting sinkURL, and returns the job
// along with the constructed event (so tests can assert on its ID).
func buildOutboundAlertFiredJob(t *testing.T, alert *domain.Alert, sinkURL string, acceptedReplyTypes []string) (domain.NotificationJob, ce.Event) {
	t.Helper()
	ev := ce.NewEvent()
	ev.SetSpecVersion("1.0")
	ev.SetID(ulid.Make().String())
	ev.SetSource("https://console.yipyap.run/orgs/" + alert.OrgID)
	ev.SetType(types.TypeAlertFiredV1)
	ev.SetSubject("alert/" + alert.ID)
	ev.SetTime(time.Now().UTC())
	if err := ev.SetData(ce.ApplicationJSON, types.AlertFiredV1Data{
		AlertID:   alert.ID,
		MonitorID: alert.MonitorID,
		Severity:  string(alert.Severity),
		Reason:    "knative integration",
		FiredAt:   alert.StartedAt,
	}); err != nil {
		t.Fatalf("SetData: %v", err)
	}
	evJSON, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	cfg := providers.CloudEventHTTPConfig{
		SinkURL: sinkURL,
		Mode:    "binary",
		AcceptedReplies: &providers.ReplyConfig{
			Types: acceptedReplyTypes,
		},
	}
	cfgJSON, _ := json.Marshal(cfg)
	// Plaintext config envelope: 0x00 version byte + JSON, base64-encoded.
	raw := append([]byte{0x00}, cfgJSON...)

	job := domain.NotificationJob{
		ID:           "job-" + alert.ID,
		AlertID:      alert.ID,
		OrgID:        alert.OrgID,
		MonitorName:  "reply-monitor",
		Severity:     string(alert.Severity),
		Channel:      "cloudevent_http",
		Message:      string(evJSON),
		TargetConfig: base64.StdEncoding.EncodeToString(raw),
	}
	return job, ev
}

// wireReplyProvider builds a CloudEventHTTP provider with an in-memory
// outbound registry and a reply dispatcher registered with the three
// Phase-2 handlers backed by s. Returns the provider and dispatcher so tests
// can, if desired, assert on dispatcher behaviour directly.
func wireReplyProvider(t *testing.T, s *sqlite.SQLiteStore) (*providers.CloudEventHTTP, *reply.Dispatcher) {
	t.Helper()
	reg := reply.NewMemoryRegistry(256, time.Hour)
	d := reply.NewDispatcher(reg)
	// RegisterAll wires every handler the store supports. Phase-2 ships three
	// read-only handlers; Phase-5 added ten state-mutating ones. The exact
	// count is asserted over in the Phase-5 integration suite (see
	// wirePhase5ReplyProvider); here we just require that RegisterAll
	// produced a non-zero list so a future registration regression trips.
	if got := replyhandlers.RegisterAll(d, s); len(got) == 0 {
		t.Fatalf("RegisterAll returned no handlers")
	}

	p := providers.NewCloudEventHTTP(nil)
	p.SetOutboundRegistry(reg)
	p.SetReplyDispatcher(d)
	return p, d
}

// findAlertEvent returns the first AlertEvent for alertID whose EventType
// matches `want`, or nil.
func findAlertEvent(t *testing.T, s *sqlite.SQLiteStore, alertID string, want domain.AlertEventType) *domain.AlertEvent {
	t.Helper()
	evs, err := s.Alerts().ListEvents(context.Background(), alertID)
	if err != nil {
		t.Fatalf("Alerts.ListEvents: %v", err)
	}
	for _, e := range evs {
		if e.EventType == want {
			return e
		}
	}
	return nil
}

// TestKnative_Reply_Claimed_E2E, sink responds with a `claimed.v1` reply
// whose alertref matches the outbound event id; the timeline gains a
// `reply.claimed` row carrying the reported `who` and (OIDC sub absent at
// this layer) sub fields.
func TestKnative_Reply_Claimed_E2E(t *testing.T) {
	if os.Getenv("KNATIVE") != "1" {
		t.Skip("KNATIVE=1 required")
	}
	s := newReplyStore(t)
	alert := seedFiringAlert(t, s)

	claimedAt := time.Now().UTC().Truncate(time.Second)
	sink := buildReplyingSinkServer(t, func(outboundID string) ce.Event {
		ev := ce.NewEvent()
		ev.SetSpecVersion("1.0")
		ev.SetID("reply-" + outboundID)
		ev.SetSource("https://consumer.example.com/")
		ev.SetType(types.TypeReplyAlertClaimedV1)
		ev.SetTime(time.Now().UTC())
		ev.SetExtension("alertref", outboundID)
		_ = ev.SetData(ce.ApplicationJSON, types.ReplyAlertClaimedV1Data{
			AlertID:   alert.ID,
			Who:       "alice@example.com",
			ClaimedAt: claimedAt,
			Note:      "on it",
		})
		return ev
	})

	p, _ := wireReplyProvider(t, s)
	job, ev := buildOutboundAlertFiredJob(t, alert, sink.URL,
		[]string{"run.yipyap.reply.alert.*"})

	id, err := p.Send(context.Background(), job)
	if err != nil {
		t.Fatalf("provider.Send: %v", err)
	}
	if id != ev.ID() {
		t.Fatalf("returned ce-id = %q, want %q", id, ev.ID())
	}

	got := findAlertEvent(t, s, alert.ID, domain.EventReplyClaimed)
	if got == nil {
		t.Fatalf("no reply.claimed event on alert %s timeline", alert.ID)
	}
	var payload replyhandlers.ClaimedPayload
	if err := json.Unmarshal(got.Detail, &payload); err != nil {
		t.Fatalf("unmarshal claimed detail: %v (%q)", err, got.Detail)
	}
	if payload.ReportedAs != "alice@example.com" {
		t.Errorf("claimed.reported_as = %q, want %q", payload.ReportedAs, "alice@example.com")
	}
	if payload.Note != "on it" {
		t.Errorf("claimed.note = %q, want %q", payload.Note, "on it")
	}
}

// TestKnative_Reply_Enriched_E2E, sink responds with an `enriched.v1`
// reply carrying three attachments; the timeline row records all three with
// their labels preserved.
func TestKnative_Reply_Enriched_E2E(t *testing.T) {
	if os.Getenv("KNATIVE") != "1" {
		t.Skip("KNATIVE=1 required")
	}
	s := newReplyStore(t)
	alert := seedFiringAlert(t, s)

	attachments := []map[string]any{
		{"label": "runbook", "href": "https://runbooks.example.com/svc-a", "kind": "link"},
		{"label": "dashboard", "href": "https://grafana.example.com/d/abc", "kind": "link"},
		{"label": "log excerpt", "preview": "ERROR: upstream timeout", "kind": "text"},
	}

	sink := buildReplyingSinkServer(t, func(outboundID string) ce.Event {
		ev := ce.NewEvent()
		ev.SetSpecVersion("1.0")
		ev.SetID("reply-" + outboundID)
		ev.SetSource("https://consumer.example.com/")
		ev.SetType(types.TypeReplyAlertEnrichedV1)
		ev.SetTime(time.Now().UTC())
		ev.SetExtension("alertref", outboundID)
		_ = ev.SetData(ce.ApplicationJSON, types.ReplyAlertEnrichedV1Data{
			AlertID:     alert.ID,
			Attachments: attachments,
		})
		return ev
	})

	p, _ := wireReplyProvider(t, s)
	job, _ := buildOutboundAlertFiredJob(t, alert, sink.URL,
		[]string{"run.yipyap.reply.alert.*"})

	if _, err := p.Send(context.Background(), job); err != nil {
		t.Fatalf("provider.Send: %v", err)
	}

	got := findAlertEvent(t, s, alert.ID, domain.EventReplyEnriched)
	if got == nil {
		t.Fatalf("no reply.enriched event on alert %s timeline", alert.ID)
	}
	var payload replyhandlers.EnrichedPayload
	if err := json.Unmarshal(got.Detail, &payload); err != nil {
		t.Fatalf("unmarshal enriched detail: %v (%q)", err, got.Detail)
	}
	if len(payload.Attachments) != 3 {
		t.Fatalf("enriched attachments = %d, want 3: %+v", len(payload.Attachments), payload.Attachments)
	}
	wantLabels := map[string]bool{"runbook": false, "dashboard": false, "log excerpt": false}
	for _, a := range payload.Attachments {
		if _, ok := wantLabels[a.Label]; ok {
			wantLabels[a.Label] = true
		}
	}
	for lbl, seen := range wantLabels {
		if !seen {
			t.Errorf("enriched attachment %q missing from payload", lbl)
		}
	}
}

// TestKnative_Reply_Linked_E2E, sink responds with a `linked.v1` reply
// binding the alert to a Jira ticket; the timeline records kind+id.
func TestKnative_Reply_Linked_E2E(t *testing.T) {
	if os.Getenv("KNATIVE") != "1" {
		t.Skip("KNATIVE=1 required")
	}
	s := newReplyStore(t)
	alert := seedFiringAlert(t, s)

	sink := buildReplyingSinkServer(t, func(outboundID string) ce.Event {
		ev := ce.NewEvent()
		ev.SetSpecVersion("1.0")
		ev.SetID("reply-" + outboundID)
		ev.SetSource("https://consumer.example.com/")
		ev.SetType(types.TypeReplyAlertLinkedV1)
		ev.SetTime(time.Now().UTC())
		ev.SetExtension("alertref", outboundID)
		_ = ev.SetData(ce.ApplicationJSON, types.ReplyAlertLinkedV1Data{
			AlertID: alert.ID,
			Kind:    "jira",
			ID:      "INC-123",
			URL:     "https://example.atlassian.net/browse/INC-123",
			Title:   "Upstream timeout triage",
		})
		return ev
	})

	p, _ := wireReplyProvider(t, s)
	job, _ := buildOutboundAlertFiredJob(t, alert, sink.URL,
		[]string{"run.yipyap.reply.alert.*"})

	if _, err := p.Send(context.Background(), job); err != nil {
		t.Fatalf("provider.Send: %v", err)
	}

	got := findAlertEvent(t, s, alert.ID, domain.EventReplyLinked)
	if got == nil {
		t.Fatalf("no reply.linked event on alert %s timeline", alert.ID)
	}
	var payload replyhandlers.LinkedPayload
	if err := json.Unmarshal(got.Detail, &payload); err != nil {
		t.Fatalf("unmarshal linked detail: %v (%q)", err, got.Detail)
	}
	if payload.Kind != "jira" || payload.ID != "INC-123" {
		t.Errorf("linked payload = %+v, want kind=jira id=INC-123", payload)
	}
}

// TestKnative_Reply_OptOut_Silent, channel opts in only to `linked.v1`; a
// sink reply of type `claimed.v1` is silently dropped. Send returns ok and
// no timeline row is written.
func TestKnative_Reply_OptOut_Silent(t *testing.T) {
	if os.Getenv("KNATIVE") != "1" {
		t.Skip("KNATIVE=1 required")
	}
	s := newReplyStore(t)
	alert := seedFiringAlert(t, s)

	sink := buildReplyingSinkServer(t, func(outboundID string) ce.Event {
		ev := ce.NewEvent()
		ev.SetSpecVersion("1.0")
		ev.SetID("reply-" + outboundID)
		ev.SetSource("https://consumer.example.com/")
		ev.SetType(types.TypeReplyAlertClaimedV1)
		ev.SetTime(time.Now().UTC())
		ev.SetExtension("alertref", outboundID)
		_ = ev.SetData(ce.ApplicationJSON, types.ReplyAlertClaimedV1Data{
			AlertID:   alert.ID,
			Who:       "bob",
			ClaimedAt: time.Now().UTC(),
		})
		return ev
	})

	p, _ := wireReplyProvider(t, s)
	// Channel opts in to linked.v1 only, a claimed.v1 reply is silent.
	job, _ := buildOutboundAlertFiredJob(t, alert, sink.URL,
		[]string{"run.yipyap.reply.alert.linked.v1"})

	if _, err := p.Send(context.Background(), job); err != nil {
		t.Fatalf("provider.Send: %v", err)
	}

	if got := findAlertEvent(t, s, alert.ID, domain.EventReplyClaimed); got != nil {
		t.Fatalf("expected no reply.claimed row when channel is opted out, got %+v", got)
	}
}

// TestKnative_Reply_BadAlertref_NotActedOn, sink responds with a reply
// whose `alertref` does not match any outbound event in the registry. The
// outbound delivery still succeeds (the sink returned 200), but no timeline
// row is written because there's nothing to correlate against.
func TestKnative_Reply_BadAlertref_NotActedOn(t *testing.T) {
	if os.Getenv("KNATIVE") != "1" {
		t.Skip("KNATIVE=1 required")
	}
	s := newReplyStore(t)
	alert := seedFiringAlert(t, s)

	sink := buildReplyingSinkServer(t, func(_ string) ce.Event {
		ev := ce.NewEvent()
		ev.SetSpecVersion("1.0")
		ev.SetID("reply-orphan")
		ev.SetSource("https://consumer.example.com/")
		ev.SetType(types.TypeReplyAlertClaimedV1)
		ev.SetTime(time.Now().UTC())
		// Deliberately bogus alertref, not matching anything in the registry.
		ev.SetExtension("alertref", "not-a-real-outbound-id")
		_ = ev.SetData(ce.ApplicationJSON, types.ReplyAlertClaimedV1Data{
			AlertID:   alert.ID,
			Who:       "mallory",
			ClaimedAt: time.Now().UTC(),
		})
		return ev
	})

	p, _ := wireReplyProvider(t, s)
	job, _ := buildOutboundAlertFiredJob(t, alert, sink.URL,
		[]string{"run.yipyap.reply.alert.*"})

	if _, err := p.Send(context.Background(), job); err != nil {
		// The outbound delivery itself returned 200, Send must not error.
		t.Fatalf("provider.Send: %v", err)
	}

	if got := findAlertEvent(t, s, alert.ID, domain.EventReplyClaimed); got != nil {
		t.Fatalf("expected no reply.claimed row for unknown alertref, got %+v", got)
	}
}
