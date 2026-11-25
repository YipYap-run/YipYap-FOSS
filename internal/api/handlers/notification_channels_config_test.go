package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify/providers"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

const channelsPath = "/api/v1/notification-channels"

type configErrBody struct {
	Error string `json:"error"`
	Field string `json:"field"`
}

// expectConfigError asserts a 400 whose body names field.
func expectConfigError(t *testing.T, resp *http.Response, field string) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	var body configErrBody
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %+v)", resp.StatusCode, body)
	}
	if body.Field != field {
		t.Fatalf("field = %q, want %q (error %q)", body.Field, field, body.Error)
	}
	if !strings.HasPrefix(body.Error, field+": ") {
		t.Fatalf("error %q does not name the field %q", body.Error, field)
	}
}

func createChannel(t *testing.T, srvURL, token, typ string, cfg any) *http.Response {
	t.Helper()
	return doJSONReq(t, http.MethodPost, srvURL+channelsPath, token, map[string]any{
		"name": typ + " channel", "type": typ, "config": cfg, "enabled": true,
	})
}

func createdID(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		var body configErrBody
		_ = json.NewDecoder(resp.Body).Decode(&body)
		t.Fatalf("create: status %d (%s)", resp.StatusCode, body.Error)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.ID
}

func proSession(t *testing.T, email string) (*httptest.Server, string, func() *configStore) {
	t.Helper()
	srv, s, _ := setupCloudEventTestServer(t)
	token := ceRegisterAndGetToken(t, srv, "Cfg "+email, email, "supersecret123")
	ceBumpToProByEmail(t, s, email)
	return srv, token, func() *configStore { return &configStore{s: s} }
}

// configStore reaches past the API to read or plant a stored row.
type configStore struct{ s *sqlite.SQLiteStore }

func (c *configStore) get(t *testing.T, id string) *domain.NotificationChannel {
	t.Helper()
	ch, err := c.s.NotificationChannels().GetByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

func (c *configStore) setConfig(t *testing.T, id, cfg string) {
	t.Helper()
	ch := c.get(t, id)
	ch.Config = cfg
	if err := c.s.NotificationChannels().Update(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
}

// The form used to send the CloudEvents filter as the raw textarea string.
// The provider then failed to unmarshal it on every send.
func TestChannelConfig_CloudEventFilterStringRejected(t *testing.T) {
	srv, token, _ := proSession(t, "cfg-ce1@test.com")
	resp := createChannel(t, srv.URL, token, "cloudevent_http", map[string]any{
		"sink_url":    "https://broker.example.com/ns/default",
		"event_types": "run.yipyap.alert.*",
	})
	expectConfigError(t, resp, "event_types")

	resp = createChannel(t, srv.URL, token, "cloudevent_http", map[string]any{
		"sink_url":     "https://broker.example.com/ns/default",
		"ce_overrides": `{"environment": "production"}`,
	})
	expectConfigError(t, resp, "ce_overrides")

	resp = createChannel(t, srv.URL, token, "cloudevent_http", map[string]any{
		"sink_url":    "https://broker.example.com/ns/default",
		"event_types": []any{"run.yipyap.alert.*", 7},
	})
	expectConfigError(t, resp, "event_types")

	resp = createChannel(t, srv.URL, token, "cloudevent_http", map[string]any{
		"sink_url":    "https://broker.example.com/ns/default",
		"event_types": []string{"run.yipyap.[alert"},
	})
	expectConfigError(t, resp, "event_types")
}

func TestChannelConfig_CloudEventOverridesAndAuth(t *testing.T) {
	srv, token, _ := proSession(t, "cfg-ce2@test.com")
	base := func(extra map[string]any) map[string]any {
		m := map[string]any{"sink_url": "https://broker.example.com/ns/default"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	cases := []struct {
		name  string
		cfg   map[string]any
		field string
	}{
		{"override name with dash", base(map[string]any{"ce_overrides": map[string]string{"env-name": "x"}}), "ce_overrides"},
		{"override of spec attribute", base(map[string]any{"ce_overrides": map[string]string{"type": "x"}}), "ce_overrides"},
		{"override of alertref", base(map[string]any{"ce_overrides": map[string]string{"alertref": "x"}}), "ce_overrides"},
		{"bad mode", base(map[string]any{"mode": "batched"}), "mode"},
		{"auth not an object", base(map[string]any{"auth": "oidc"}), "auth"},
		{"oidc without secret", base(map[string]any{"auth": map[string]any{"type": "oidc", "oidc": map[string]any{
			"token_url": "https://idp.example.com/token", "client_id": "c",
		}}}), "auth.oidc.client_secret"},
		{"reply cap as string", base(map[string]any{"accepted_replies": map[string]any{"max_suppress_seconds": "60"}}), "accepted_replies.max_suppress_seconds"},
		{"reply cap negative", base(map[string]any{"accepted_replies": map[string]any{"rate_limit": -1}}), "accepted_replies.rate_limit"},
		{"typo key", base(map[string]any{"event_type": []string{"x"}}), "event_type"},
		{"missing sink", map[string]any{"mode": "binary"}, "sink_url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expectConfigError(t, createChannel(t, srv.URL, token, "cloudevent_http", tc.cfg), tc.field)
		})
	}
}

// A typo in the headers textarea used to be replaced with {} and saved,
// dropping the auth header without a word.
func TestChannelConfig_WebhookHeaders(t *testing.T) {
	srv, token, _ := proSession(t, "cfg-wh@test.com")
	cases := []struct {
		name    string
		headers any
	}{
		{"string", `{"X-Api-Key": "abc"`},
		{"number value", map[string]any{"X-Retry": 3}},
		{"header injection", map[string]any{"X-Api-Key": "abc\r\nX-Evil: 1"}},
		{"bad name", map[string]any{"X Api Key": "abc"}},
		{"managed header", map[string]any{"Host": "internal.example"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := createChannel(t, srv.URL, token, "webhook", map[string]any{
				"url": "https://example.com/alerts", "headers": tc.headers,
			})
			expectConfigError(t, resp, "headers")
		})
	}
	expectConfigError(t, createChannel(t, srv.URL, token, "webhook", map[string]any{
		"url": "https://example.com/alerts", "format": "msteams",
	}), "format")
}

func TestChannelConfig_RequiredAndTypes(t *testing.T) {
	srv, token, _ := proSession(t, "cfg-req@test.com")
	expectConfigError(t, createChannel(t, srv.URL, token, "slack", map[string]any{"bot_token": "xoxb-1"}), "channel_id")
	expectConfigError(t, createChannel(t, srv.URL, token, "slack", map[string]any{"bot_token": "xoxb-1", "channel_id": "  "}), "channel_id")
	expectConfigError(t, createChannel(t, srv.URL, token, "telegram", map[string]any{"chat_id": -100123}), "chat_id")
	expectConfigError(t, createChannel(t, srv.URL, token, "pushover", map[string]any{"api_token": "a"}), "user_key")
	expectConfigError(t, createChannel(t, srv.URL, token, "ntfy", "topic"), "config")
	expectConfigError(t, createChannel(t, srv.URL, token, "carrier_pigeon", map[string]any{}), "type")
}

// Update runs the same checks, on the merged config.
func TestChannelConfig_UpdateRejectsStringFilter(t *testing.T) {
	srv, token, _ := proSession(t, "cfg-upd@test.com")
	id := createdID(t, createChannel(t, srv.URL, token, "cloudevent_http", map[string]any{
		"sink_url":    "https://broker.example.com/ns/default",
		"event_types": []string{"run.yipyap.alert.*"},
	}))
	resp := doJSONReq(t, http.MethodPatch, srv.URL+channelsPath+"/"+id, token, map[string]any{
		"config": map[string]any{
			"sink_url":    "https://broker.example.com/ns/default",
			"event_types": "run.yipyap.alert.*,run.yipyap.monitor.*",
		},
	})
	expectConfigError(t, resp, "event_types")

	// The stored filter is unchanged.
	if got := getEventTypes(t, srv.URL, token, id); len(got) != 1 || got[0] != "run.yipyap.alert.*" {
		t.Fatalf("event_types after refused update = %v", got)
	}
}

// A stored row that already holds a key the schema does not know (seed data,
// older releases) can still be edited when the form echoes it back. A key
// the row did not have is still refused.
func TestChannelConfig_UpdateToleratesLegacyKeys(t *testing.T) {
	srv, token, store := proSession(t, "cfg-legacy@test.com")
	id := createdID(t, createChannel(t, srv.URL, token, "webhook", map[string]any{
		"url": "https://example.com/alerts",
	}))
	store().setConfig(t, id, `{"url":"https://example.com/alerts","method":"POST"}`)

	resp := doJSONReq(t, http.MethodPatch, srv.URL+channelsPath+"/"+id, token, map[string]any{
		"config": map[string]any{"url": "https://example.com/alerts", "method": "POST", "headers": map[string]string{"X-Api-Key": "k"}},
	})
	if resp.StatusCode != http.StatusOK {
		var body configErrBody
		_ = json.NewDecoder(resp.Body).Decode(&body)
		t.Fatalf("legacy echo: status %d (%s)", resp.StatusCode, body.Error)
	}
	_ = resp.Body.Close()

	resp = doJSONReq(t, http.MethodPatch, srv.URL+channelsPath+"/"+id, token, map[string]any{
		"config": map[string]any{"url": "https://example.com/alerts", "verb": "PUT"},
	})
	expectConfigError(t, resp, "verb")
}

func getEventTypes(t *testing.T, srvURL, token, id string) []string {
	t.Helper()
	resp := doJSONReq(t, http.MethodGet, srvURL+channelsPath+"/"+id, token, nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: status %d", resp.StatusCode)
	}
	var out struct {
		Config struct {
			EventTypes []string `json:"event_types"`
		} `json:"config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("event_types did not decode as a list: %v", err)
	}
	return out.Config.EventTypes
}

// A filter saved through the API comes back as a list and is applied on the
// real delivery path: router fan-out, dispatcher, cloudevent_http provider.
func TestChannelConfig_CloudEventFilterRoundTripsAndIsHonored(t *testing.T) {
	srv, token, store := proSession(t, "cfg-rt@test.com")
	id := createdID(t, createChannel(t, srv.URL, token, "cloudevent_http", map[string]any{
		"sink_url":     "https://broker.example.com/ns/default",
		"event_types":  []string{"run.yipyap.alert.*"},
		"ce_overrides": map[string]string{"environment": "prod"},
	}))
	if got := getEventTypes(t, srv.URL, token, id); len(got) != 1 || got[0] != "run.yipyap.alert.*" {
		t.Fatalf("event_types round trip = %v", got)
	}

	var mu sync.Mutex
	var gotTypes, gotEnv []string
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotTypes = append(gotTypes, r.Header.Get("Ce-Type"))
		gotEnv = append(gotEnv, r.Header.Get("Ce-Environment"))
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer sink.Close()

	// The API refuses loopback sinks, so point the saved row at the test
	// sink directly. Everything else is what the API stored.
	ch := store().get(t, id)
	var cfg map[string]any
	if err := json.Unmarshal([]byte(ch.Config), &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["sink_url"] = sink.URL
	raw, _ := json.Marshal(cfg)
	store().setConfig(t, id, string(raw))

	orig := checker.AllowPrivateControlPlaneTargets
	checker.AllowPrivateControlPlaneTargets = true
	t.Cleanup(func() { checker.AllowPrivateControlPlaneTargets = orig })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := store().s
	b := bus.NewChannel()
	defer func() { _ = b.Close() }()
	d := notify.NewDispatcher(b, s.Dedup())
	d.Register(providers.NewCloudEventHTTP(nil), 4)
	if err := d.Start(ctx); err != nil {
		t.Fatal(err)
	}
	r := cloudevents.NewRouter(b, s, nil, nil)
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	up, err := cloudevents.NewMonitorUpV1(cloudevents.OrgSource(ch.OrgID), types.MonitorUpV1Data{MonitorID: "m1", CheckID: "c1", CheckedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	fired, err := cloudevents.NewAlertFiredV1(cloudevents.OrgSource(ch.OrgID), types.AlertFiredV1Data{
		AlertID: "a1", MonitorID: "m1", Severity: "critical", Reason: "down", FiredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []interface {
		Type() string
		ID() string
	}{&up, &fired} {
		payload, _ := json.Marshal(ev)
		if err := b.PublishDurable(ctx, cloudevents.SubjectFor(ch.OrgID, ev.Type()), payload, ev.ID()); err != nil {
			t.Fatal(err)
		}
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(gotTypes)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Give a wrongly unfiltered monitor event time to land too.
	time.Sleep(100 * time.Millisecond)
	_ = d.Stop(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(gotTypes) != 1 || gotTypes[0] != fired.Type() {
		t.Fatalf("sink received %v, want only %q (the monitor event is outside the filter)", gotTypes, fired.Type())
	}
	if gotEnv[0] != "prod" {
		t.Fatalf("ce_overrides not applied: Ce-Environment = %q", gotEnv[0])
	}
}

// A row stored before the typed checks fails its test with the field to fix
// rather than sending a test the notifier cannot decode.
func TestChannelConfig_TestNamesBrokenStoredField(t *testing.T) {
	srv, token, store := proSession(t, "cfg-test@test.com")
	id := createdID(t, createChannel(t, srv.URL, token, "cloudevent_http", map[string]any{
		"sink_url": "https://broker.example.com/ns/default",
	}))
	store().setConfig(t, id, `{"sink_url":"https://broker.example.com/ns/default","event_types":"run.yipyap.alert.*"}`)
	resp := doJSONReq(t, http.MethodPost, srv.URL+channelsPath+"/"+id+"/test", token, map[string]any{})
	expectConfigError(t, resp, "event_types")
}

// Test sends an alert.fired event. When the channel's filter leaves that type
// out, the provider would drop it without an error and the SPA would say the
// test was sent. The handler says it was filtered and sends nothing.
func TestChannelConfig_TestReportsFilteredEvent(t *testing.T) {
	srv, s, sender := setupCloudEventTestServer(t)
	token := ceRegisterAndGetToken(t, srv, "Cfg filtered", "cfg-filtered@test.com", "supersecret123")
	ceBumpToProByEmail(t, s, "cfg-filtered@test.com")

	id := createdID(t, createChannel(t, srv.URL, token, "cloudevent_http", map[string]any{
		"sink_url":    "https://broker.example.com/ns/default",
		"event_types": []string{"run.yipyap.monitor.*"},
	}))
	resp := doJSONReq(t, http.MethodPost, srv.URL+channelsPath+"/"+id+"/test", token, map[string]any{})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Filtered  bool   `json:"filtered"`
		EventType string `json:"event_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Filtered || body.EventType != "run.yipyap.alert.fired.v1" {
		t.Fatalf("body = %+v, want filtered alert.fired", body)
	}
	sender.mu.Lock()
	seen := sender.seen
	sender.mu.Unlock()
	if seen {
		t.Fatal("a filtered test must not be handed to the sender")
	}

	// A filter that matches still sends.
	id2 := createdID(t, createChannel(t, srv.URL, token, "cloudevent_http", map[string]any{
		"sink_url":    "https://broker.example.com/ns/default",
		"event_types": []string{"run.yipyap.alert.*"},
	}))
	resp2 := doJSONReq(t, http.MethodPost, srv.URL+channelsPath+"/"+id2+"/test", token, map[string]any{})
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("matching filter: status = %d", resp2.StatusCode)
	}
	sender.mu.Lock()
	seen = sender.seen
	sender.mu.Unlock()
	if !seen {
		t.Fatal("a matching filter should send the test event")
	}
}
