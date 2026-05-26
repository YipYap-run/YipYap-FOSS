package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// mustCE constructs an AlertFired v1 event and returns its structured-mode
// (canonical) JSON encoding suitable for NotificationJob.Message.
func mustCE(t *testing.T) (ce.Event, []byte) {
	t.Helper()
	ev, err := cloudevents.NewAlertFiredV1("https://console.yipyap.run/orgs/org1", types.AlertFiredV1Data{
		AlertID:   "a1",
		MonitorID: "m1",
		Severity:  "critical",
		Reason:    "host down",
		FiredAt:   time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("build event: %v", err)
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return ev, b
}

// mustMonitorCE builds a monitor.up event for filter tests.
func mustMonitorCE(t *testing.T) (ce.Event, []byte) {
	t.Helper()
	ev, err := cloudevents.NewMonitorUpV1("https://console.yipyap.run/orgs/org1/monitors/m1", types.MonitorUpV1Data{
		MonitorID: "m1",
		CheckID:   "c1",
		CheckedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("build event: %v", err)
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return ev, b
}

func jobWithCE(message []byte, config CloudEventHTTPConfig) domain.NotificationJob {
	cfgJSON, _ := json.Marshal(config)
	return domain.NotificationJob{
		ID:          "j1",
		AlertID:     "a1",
		OrgID:       "org1",
		MonitorName: "web-check",
		Severity:    "critical",
		Channel:     "cloudevent_http",
		Message:     string(message),
		// Plaintext version envelope (0x00 prefix) + JSON, base64-encoded.
		TargetConfig: encodePlaintextConfig(cfgJSON),
	}
}

func encodePlaintextConfig(jsonBytes []byte) string {
	// Plaintext envelope: version 0x00 + JSON payload, base64-encoded.
	// resolveConfig accepts base64(0x00 || json) as plaintext.
	buf := make([]byte, 0, len(jsonBytes)+1)
	buf = append(buf, 0x00)
	buf = append(buf, jsonBytes...)
	return base64.StdEncoding.EncodeToString(buf)
}

func TestCloudEventHTTP_BinaryMode(t *testing.T) {
	var gotHeaders http.Header
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(202)
	}))
	defer srv.Close()

	ev, msg := mustCE(t)
	p := NewCloudEventHTTP(nil)
	cfg := CloudEventHTTPConfig{SinkURL: srv.URL, Mode: "binary"}
	id, err := p.Send(context.Background(), jobWithCE(msg, cfg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != ev.ID() {
		t.Errorf("expected id %q, got %q", ev.ID(), id)
	}
	if got := gotHeaders.Get("Ce-Type"); got != ev.Type() {
		t.Errorf("expected Ce-Type %q, got %q", ev.Type(), got)
	}
	if got := gotHeaders.Get("Ce-Id"); got != ev.ID() {
		t.Errorf("expected Ce-Id %q, got %q", ev.ID(), got)
	}
	if got := gotHeaders.Get("Ce-Specversion"); got != "1.0" {
		t.Errorf("expected Ce-Specversion 1.0, got %q", got)
	}
	if got := gotHeaders.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("expected application/json Content-Type, got %q", got)
	}
	// Body should be the data payload, not the envelope.
	var data map[string]any
	if err := json.Unmarshal(gotBody, &data); err != nil {
		t.Fatalf("body is not JSON: %v; body=%s", err, gotBody)
	}
	if data["alert_id"] != "a1" {
		t.Errorf("expected alert_id=a1 in body, got %v", data["alert_id"])
	}
	if _, ok := data["specversion"]; ok {
		t.Errorf("binary-mode body should not contain envelope field 'specversion': %s", gotBody)
	}
}

func TestCloudEventHTTP_StructuredMode(t *testing.T) {
	var gotHeaders http.Header
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	_, msg := mustCE(t)
	p := NewCloudEventHTTP(nil)
	cfg := CloudEventHTTPConfig{SinkURL: srv.URL, Mode: "structured"}
	if _, err := p.Send(context.Background(), jobWithCE(msg, cfg)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ct := gotHeaders.Get("Content-Type"); !strings.HasPrefix(ct, "application/cloudevents+json") {
		t.Errorf("expected Content-Type application/cloudevents+json, got %q", ct)
	}
	if !strings.Contains(string(gotBody), `"specversion":"1.0"`) {
		t.Errorf("expected specversion in body, got %s", gotBody)
	}
	if !strings.Contains(string(gotBody), `"type":"run.yipyap.alert.fired.v1"`) {
		t.Errorf("expected CE type in body, got %s", gotBody)
	}
}

func TestCloudEventHTTP_EventTypeFilter(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(200)
	}))
	defer srv.Close()

	_, msg := mustCE(t) // AlertFired event
	p := NewCloudEventHTTP(nil)
	cfg := CloudEventHTTPConfig{
		SinkURL:    srv.URL,
		EventTypes: []string{"run.yipyap.monitor.*"}, // won't match alert.fired
	}
	id, err := p.Send(context.Background(), jobWithCE(msg, cfg))
	if err != nil {
		t.Fatalf("expected no error for filtered event, got %v", err)
	}
	if id != "" {
		t.Errorf("expected empty id when filtered, got %q", id)
	}
	if requests != 0 {
		t.Errorf("expected 0 requests, got %d", requests)
	}
}

func TestCloudEventHTTP_EventTypeFilterMatches(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(200)
	}))
	defer srv.Close()

	_, msg := mustCE(t)
	p := NewCloudEventHTTP(nil)
	cfg := CloudEventHTTPConfig{
		SinkURL:    srv.URL,
		EventTypes: []string{"run.yipyap.alert.*"},
	}
	if _, err := p.Send(context.Background(), jobWithCE(msg, cfg)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requests != 1 {
		t.Errorf("expected 1 request, got %d", requests)
	}
}

func TestCloudEventHTTP_EventTypeFilterMonitorMatches(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(200)
	}))
	defer srv.Close()

	_, msg := mustMonitorCE(t)
	p := NewCloudEventHTTP(nil)
	cfg := CloudEventHTTPConfig{
		SinkURL:    srv.URL,
		EventTypes: []string{"run.yipyap.monitor.*"},
	}
	if _, err := p.Send(context.Background(), jobWithCE(msg, cfg)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requests != 1 {
		t.Errorf("expected 1 request, got %d", requests)
	}
}

func TestCloudEventHTTP_CEOverrides(t *testing.T) {
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	_, msg := mustCE(t)
	p := NewCloudEventHTTP(nil)
	cfg := CloudEventHTTPConfig{
		SinkURL:     srv.URL,
		Mode:        "binary",
		CEOverrides: map[string]string{"environment": "prod"},
	}
	if _, err := p.Send(context.Background(), jobWithCE(msg, cfg)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := gotHeaders.Get("Ce-Environment"); got != "prod" {
		t.Errorf("expected Ce-Environment 'prod', got %q (all headers: %v)", got, gotHeaders)
	}
}

func TestCloudEventHTTP_RejectsNonHTTPS(t *testing.T) {
	_, msg := mustCE(t)
	p := NewCloudEventHTTP(nil)
	cfg := CloudEventHTTPConfig{SinkURL: "http://not-localhost.example.com/sink"}
	if _, err := p.Send(context.Background(), jobWithCE(msg, cfg)); err == nil {
		t.Fatal("expected error for http:// non-localhost sink")
	}
}

func TestCloudEventHTTP_AllowsLocalhostHTTP(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// httptest URL uses 127.0.0.1 which is allowed under http://.
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse srv url: %v", err)
	}
	if u.Scheme != "http" {
		t.Skipf("expected httptest to be http, got %s", u.Scheme)
	}

	_, msg := mustCE(t)
	p := NewCloudEventHTTP(nil)
	cfg := CloudEventHTTPConfig{SinkURL: srv.URL}
	if _, err := p.Send(context.Background(), jobWithCE(msg, cfg)); err != nil {
		t.Fatalf("unexpected error for localhost http: %v", err)
	}
	if requests != 1 {
		t.Errorf("expected 1 request, got %d", requests)
	}
}

func TestCloudEventHTTP_RejectsBadMode(t *testing.T) {
	_, msg := mustCE(t)
	p := NewCloudEventHTTP(nil)
	cfg := CloudEventHTTPConfig{SinkURL: "https://example.com/sink", Mode: "weird"}
	if _, err := p.Send(context.Background(), jobWithCE(msg, cfg)); err == nil {
		t.Fatal("expected error for invalid mode")
	}
}

func TestCloudEventHTTP_RejectsBadAuthType(t *testing.T) {
	_, msg := mustCE(t)
	p := NewCloudEventHTTP(nil)
	cfg := CloudEventHTTPConfig{SinkURL: "https://example.com/sink"}
	cfg.Auth.Type = "bogus"
	if _, err := p.Send(context.Background(), jobWithCE(msg, cfg)); err == nil {
		t.Fatal("expected error for unknown auth type")
	}
}

// --- OIDC (Phase 2) ---

// newIdPServer returns an httptest server that simulates an OAuth2
// client_credentials token endpoint. Each successful request increments
// hitCount and returns the supplied access_token. If status != 0, the
// server responds with that status (used for failure-mode tests).
func newIdPServer(t *testing.T, token string, expiresIn int, status int, hitCount *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.PostForm.Get("grant_type") != "client_credentials" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if hitCount != nil {
			*hitCount++
		}
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"access_token": token,
			"token_type":   "bearer",
			"expires_in":   expiresIn,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	return srv
}

func TestCloudEventHTTP_OIDC_AttachesBearer(t *testing.T) {
	hits := 0
	idp := newIdPServer(t, "tok-abc", 3600, 0, &hits)
	defer idp.Close()

	var gotAuth string
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(202)
	}))
	defer sink.Close()

	_, msg := mustCE(t)
	p := NewCloudEventHTTP(nil)
	cfg := CloudEventHTTPConfig{SinkURL: sink.URL, Mode: "binary"}
	cfg.Auth = CloudEventHTTPAuth{
		Type: "oidc",
		OIDC: &CloudEventHTTPOIDC{
			Issuer:       idp.URL,
			TokenURL:     idp.URL + "/token",
			ClientID:     "cid",
			ClientSecret: "csec",
			Audience:     "https://sink",
		},
	}
	if _, err := p.Send(context.Background(), jobWithCE(msg, cfg)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer tok-abc" {
		t.Fatalf("expected Authorization 'Bearer tok-abc', got %q", gotAuth)
	}
	if hits != 1 {
		t.Fatalf("expected 1 IdP hit, got %d", hits)
	}
}

func TestCloudEventHTTP_OIDC_AttachesBearer_Structured(t *testing.T) {
	idp := newIdPServer(t, "tok-xyz", 3600, 0, nil)
	defer idp.Close()

	var gotAuth, gotCT string
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(200)
	}))
	defer sink.Close()

	_, msg := mustCE(t)
	p := NewCloudEventHTTP(nil)
	cfg := CloudEventHTTPConfig{SinkURL: sink.URL, Mode: "structured"}
	cfg.Auth = CloudEventHTTPAuth{
		Type: "oidc",
		OIDC: &CloudEventHTTPOIDC{
			TokenURL:     idp.URL + "/token",
			ClientID:     "cid",
			ClientSecret: "csec",
		},
	}
	if _, err := p.Send(context.Background(), jobWithCE(msg, cfg)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer tok-xyz" {
		t.Fatalf("expected Authorization 'Bearer tok-xyz', got %q", gotAuth)
	}
	if !strings.HasPrefix(gotCT, "application/cloudevents+json") {
		t.Fatalf("expected structured content-type, got %q", gotCT)
	}
}

func TestCloudEventHTTP_OIDC_MissingFields(t *testing.T) {
	_, msg := mustCE(t)
	p := NewCloudEventHTTP(nil)
	cfg := CloudEventHTTPConfig{SinkURL: "https://example.com/sink"}
	cfg.Auth = CloudEventHTTPAuth{Type: "oidc"}
	_, err := p.Send(context.Background(), jobWithCE(msg, cfg))
	if err == nil {
		t.Fatal("expected error for oidc auth with no OIDC config")
	}
	if !strings.Contains(err.Error(), "auth.oidc") {
		t.Fatalf("expected error to mention auth.oidc, got %v", err)
	}
}

func TestCloudEventHTTP_OIDC_MissingTokenURL(t *testing.T) {
	_, msg := mustCE(t)
	p := NewCloudEventHTTP(nil)
	cfg := CloudEventHTTPConfig{SinkURL: "https://example.com/sink"}
	cfg.Auth = CloudEventHTTPAuth{
		Type: "oidc",
		OIDC: &CloudEventHTTPOIDC{
			ClientID:     "cid",
			ClientSecret: "csec",
			// TokenURL missing
		},
	}
	_, err := p.Send(context.Background(), jobWithCE(msg, cfg))
	if err == nil {
		t.Fatal("expected error for oidc auth with missing token_url")
	}
	if !strings.Contains(err.Error(), "token_url") {
		t.Fatalf("expected error to mention token_url, got %v", err)
	}
}

func TestCloudEventHTTP_OIDC_IdPFailure_ReturnsRetryable(t *testing.T) {
	idp := newIdPServer(t, "", 0, 500, nil)
	defer idp.Close()

	sinkHits := 0
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sinkHits++
		w.WriteHeader(202)
	}))
	defer sink.Close()

	_, msg := mustCE(t)
	p := NewCloudEventHTTP(nil)
	cfg := CloudEventHTTPConfig{SinkURL: sink.URL, Mode: "binary"}
	cfg.Auth = CloudEventHTTPAuth{
		Type: "oidc",
		OIDC: &CloudEventHTTPOIDC{
			TokenURL:     idp.URL + "/token",
			ClientID:     "cid",
			ClientSecret: "csec",
		},
	}
	_, err := p.Send(context.Background(), jobWithCE(msg, cfg))
	if err == nil {
		t.Fatal("expected error when IdP fails")
	}
	if !strings.Contains(err.Error(), "retryable") {
		t.Fatalf("expected retryable error classification, got %v", err)
	}
	if sinkHits != 0 {
		t.Fatalf("expected 0 sink hits when IdP fails, got %d", sinkHits)
	}
}

func TestCloudEventHTTP_OIDC_CachedToken_OneIdPHit(t *testing.T) {
	hits := 0
	idp := newIdPServer(t, "cached-tok", 3600, 0, &hits)
	defer idp.Close()

	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(202)
	}))
	defer sink.Close()

	_, msg := mustCE(t)
	p := NewCloudEventHTTP(nil)
	cfg := CloudEventHTTPConfig{SinkURL: sink.URL, Mode: "binary"}
	cfg.Auth = CloudEventHTTPAuth{
		Type: "oidc",
		OIDC: &CloudEventHTTPOIDC{
			TokenURL:     idp.URL + "/token",
			ClientID:     "cid",
			ClientSecret: "csec",
		},
	}
	for i := 0; i < 2; i++ {
		if _, err := p.Send(context.Background(), jobWithCE(msg, cfg)); err != nil {
			t.Fatalf("send %d: unexpected error: %v", i, err)
		}
	}
	if hits != 1 {
		t.Fatalf("expected 1 IdP hit across 2 sends (cache reuse), got %d", hits)
	}
}

func TestCloudEventHTTP_PermanentOn4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(400)
	}))
	defer srv.Close()

	_, msg := mustCE(t)
	p := NewCloudEventHTTP(nil)
	cfg := CloudEventHTTPConfig{SinkURL: srv.URL}
	if _, err := p.Send(context.Background(), jobWithCE(msg, cfg)); err == nil {
		t.Fatal("expected error for 400 response")
	}
}

func TestCloudEventHTTP_RetryableOn5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	_, msg := mustCE(t)
	p := NewCloudEventHTTP(nil)
	cfg := CloudEventHTTPConfig{SinkURL: srv.URL}
	if _, err := p.Send(context.Background(), jobWithCE(msg, cfg)); err == nil {
		t.Fatal("expected error for 503 response")
	}
}

func TestCloudEventHTTP_LogsReplyEvent(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		// Reply with a structured-mode CloudEvent.
		reply := map[string]any{
			"specversion": "1.0",
			"id":          "reply-1",
			"source":      "https://sink.example.com/",
			"type":        "run.example.ack.v1",
			"time":        time.Now().UTC().Format(time.RFC3339),
			"alertref":    "a1",
			"datacontenttype": "application/json",
			"data":        map[string]any{"ok": true},
		}
		body, _ := json.Marshal(reply)
		w.Header().Set("Content-Type", "application/cloudevents+json")
		w.WriteHeader(200)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	_, msg := mustCE(t)
	p := NewCloudEventHTTP(nil)
	cfg := CloudEventHTTPConfig{SinkURL: srv.URL}
	if _, err := p.Send(context.Background(), jobWithCE(msg, cfg)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requests != 1 {
		t.Errorf("expected 1 request, got %d", requests)
	}
}

func TestCloudEventHTTP_RejectsNonCloudEventMessage(t *testing.T) {
	p := NewCloudEventHTTP(nil)
	cfg := CloudEventHTTPConfig{SinkURL: "https://example.com/sink"}
	job := jobWithCE([]byte("not json"), cfg)
	job.Message = "not json"
	if _, err := p.Send(context.Background(), job); err == nil {
		t.Fatal("expected error for invalid CE message")
	}
}
