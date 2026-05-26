package handlers_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/api"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// capturingTestSender records the last NotificationJob passed to TestSend.
type capturingTestSender struct {
	mu   sync.Mutex
	job  domain.NotificationJob
	seen bool
	err  error
}

func (c *capturingTestSender) TestSend(_ context.Context, job domain.NotificationJob) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.job = job
	c.seen = true
	return "ok", c.err
}

// setupCloudEventTestServer spins up an API server with registration enabled
// and an injected capturingTestSender.
func setupCloudEventTestServer(t *testing.T) (*httptest.Server, *sqlite.SQLiteStore, *capturingTestSender) {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ts := &capturingTestSender{}

	jwt := auth.NewJWTIssuer([]byte("test-secret-key-for-testing"), 1*time.Hour)
	handler := api.NewServer(s, jwt, bus.NewChannel(), nil, nil, "", "", api.ServerOptions{
		RegistrationEnabled: true,
		TestSender:          handlers.TestSender(ts),
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return srv, s, ts
}

// ceRegisterAndGetToken registers a new org+user and returns the bearer token.
func ceRegisterAndGetToken(t *testing.T, ts *httptest.Server, orgName, email, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"org_name": orgName,
		"email":    email,
		"password": password,
	})
	resp, err := http.Post(ts.URL+"/api/v1/auth/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: expected 201, got %d", resp.StatusCode)
	}
	var result struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Token == "" {
		t.Fatal("register: empty token")
	}
	return result.Token
}

// doJSONReq is a helper to do an authenticated JSON request and return the response.
func doJSONReq(t *testing.T, method, url, token string, body interface{}) *http.Response {
	t.Helper()
	var r *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, url, r)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestCreate_CloudEventHTTP_StoresChannel(t *testing.T) {
	ts, s, _ := setupCloudEventTestServer(t)
	token := ceRegisterAndGetToken(t, ts, "CEOrg1", "ce1@test.com", "supersecret123")
	ceBumpToProByEmail(t, s, "ce1@test.com")

	body := map[string]interface{}{
		"name": "broker-sink",
		"type": "cloudevent_http",
		"config": map[string]interface{}{
			"sink_url":     "https://broker.example.com/ns/default",
			"mode":         "binary",
			"event_types":  []string{"run.yipyap.alert.*"},
			"ce_overrides": map[string]string{"environment": "prod"},
		},
		"enabled": true,
	}

	resp := doJSONReq(t, http.MethodPost, ts.URL+"/api/v1/notification-channels", token, body)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d", resp.StatusCode)
	}
	var created struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Type != "cloudevent_http" {
		t.Fatalf("expected type=cloudevent_http, got %q", created.Type)
	}
	if created.ID == "" {
		t.Fatal("expected id to be set")
	}

	// List and assert it's present.
	resp2 := doJSONReq(t, http.MethodGet, ts.URL+"/api/v1/notification-channels", token, nil)
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", resp2.StatusCode)
	}
	var list []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ch := range list {
		if ch.ID == created.ID && ch.Type == "cloudevent_http" {
			found = true
		}
	}
	if !found {
		t.Fatalf("created cloudevent_http channel not found in list: %+v", list)
	}
}

func TestCreate_CloudEventHTTP_RejectsPrivateSinkURL(t *testing.T) {
	ts, s, _ := setupCloudEventTestServer(t)
	token := ceRegisterAndGetToken(t, ts, "CEOrg2", "ce2@test.com", "supersecret123")
	ceBumpToProByEmail(t, s, "ce2@test.com")

	body := map[string]interface{}{
		"name": "private-sink",
		"type": "cloudevent_http",
		"config": map[string]interface{}{
			"sink_url": "http://10.0.0.1/",
			"mode":     "binary",
		},
		"enabled": true,
	}

	resp := doJSONReq(t, http.MethodPost, ts.URL+"/api/v1/notification-channels", token, body)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for private sink_url, got %d", resp.StatusCode)
	}
}

// TestCreate_CloudEventHTTP_RejectsPrivateOIDCTokenURL exercises C-3: the
// OIDC token_url must pass the same SSRF check as sink_url. Without the
// fix, an attacker with channel-create permission pointed token_url at
// http://169.254.169.254/... and had yipyap POST client_credentials
// (including the configured client_secret) to the metadata endpoint on
// every reply-eligible event.
func TestCreate_CloudEventHTTP_RejectsPrivateOIDCTokenURL(t *testing.T) {
	ts, s, _ := setupCloudEventTestServer(t)
	token := ceRegisterAndGetToken(t, ts, "CEOrgC3TokenURL", "cec3tokenurl@test.com", "supersecret123")
	ceBumpToProByEmail(t, s, "cec3tokenurl@test.com")

	body := map[string]interface{}{
		"name": "oidc-sink-ssrf",
		"type": "cloudevent_http",
		"config": map[string]interface{}{
			"sink_url": "https://broker.example.com/ns/default",
			"mode":     "binary",
			"auth": map[string]interface{}{
				"type": "oidc",
				"oidc": map[string]interface{}{
					"issuer":        "https://issuer.example.com",
					"token_url":     "http://169.254.169.254/latest/api/token",
					"client_id":     "my-client-id",
					"client_secret": "super-secret-value",
					"audience":      "https://sink.example.com",
				},
			},
		},
		"enabled": true,
	}

	resp := doJSONReq(t, http.MethodPost, ts.URL+"/api/v1/notification-channels", token, body)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for private token_url, got %d", resp.StatusCode)
	}
}

// TestCreate_CloudEventHTTP_RejectsPrivateOIDCIssuer exercises C-3 on the
// issuer field. Issuer ends up in the cache key (client.go cacheKey) and
// in the error-path identifier; operators have been known to enter an
// internal identifier (http://10.0.0.5/) which would be a latent SSRF
// vector once a future handler uses issuer for OIDC discovery.
func TestCreate_CloudEventHTTP_RejectsPrivateOIDCIssuer(t *testing.T) {
	ts, s, _ := setupCloudEventTestServer(t)
	token := ceRegisterAndGetToken(t, ts, "CEOrgC3Issuer", "cec3issuer@test.com", "supersecret123")
	ceBumpToProByEmail(t, s, "cec3issuer@test.com")

	body := map[string]interface{}{
		"name": "oidc-sink-ssrf-issuer",
		"type": "cloudevent_http",
		"config": map[string]interface{}{
			"sink_url": "https://broker.example.com/ns/default",
			"mode":     "binary",
			"auth": map[string]interface{}{
				"type": "oidc",
				"oidc": map[string]interface{}{
					"issuer":        "http://10.0.0.5/",
					"token_url":     "https://issuer.example.com/token",
					"client_id":     "my-client-id",
					"client_secret": "super-secret-value",
					"audience":      "https://sink.example.com",
				},
			},
		},
		"enabled": true,
	}

	resp := doJSONReq(t, http.MethodPost, ts.URL+"/api/v1/notification-channels", token, body)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for private issuer, got %d", resp.StatusCode)
	}
}

// TestUpdate_CloudEventHTTP_RejectsPrivateOIDCTokenURL confirms the same
// check runs on PATCH. Without it an attacker could create a channel with
// a benign token_url and then update to a private one, bypassing the
// create-time block.
func TestUpdate_CloudEventHTTP_RejectsPrivateOIDCTokenURL(t *testing.T) {
	ts, s, _ := setupCloudEventTestServer(t)
	token := ceRegisterAndGetToken(t, ts, "CEOrgC3Update", "cec3update@test.com", "supersecret123")
	ceBumpToProByEmail(t, s, "cec3update@test.com")

	// Create with valid URLs.
	createBody := map[string]interface{}{
		"name": "oidc-sink-update",
		"type": "cloudevent_http",
		"config": map[string]interface{}{
			"sink_url": "https://broker.example.com/ns/default",
			"mode":     "binary",
			"auth": map[string]interface{}{
				"type": "oidc",
				"oidc": map[string]interface{}{
					"issuer":        "https://issuer.example.com",
					"token_url":     "https://issuer.example.com/token",
					"client_id":     "my-client-id",
					"client_secret": "original-secret",
					"audience":      "https://sink.example.com",
				},
			},
		},
		"enabled": true,
	}
	resp := doJSONReq(t, http.MethodPost, ts.URL+"/api/v1/notification-channels", token, createBody)
	if resp.StatusCode != http.StatusCreated {
		b := new(bytes.Buffer)
		_, _ = b.ReadFrom(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("create: expected 201, got %d: %s", resp.StatusCode, b.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&created)
	_ = resp.Body.Close()

	// PATCH to a private token_url, must be rejected.
	patchBody := map[string]interface{}{
		"config": map[string]interface{}{
			"sink_url": "https://broker.example.com/ns/default",
			"mode":     "binary",
			"auth": map[string]interface{}{
				"type": "oidc",
				"oidc": map[string]interface{}{
					"issuer":        "https://issuer.example.com",
					"token_url":     "http://169.254.169.254/latest/api/token",
					"client_id":     "my-client-id",
					"client_secret": "••••••••",
					"audience":      "https://sink.example.com",
				},
			},
		},
	}
	respU := doJSONReq(t, http.MethodPatch, ts.URL+"/api/v1/notification-channels/"+created.ID, token, patchBody)
	defer func() { _ = respU.Body.Close() }()
	if respU.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for private token_url on update, got %d", respU.StatusCode)
	}
}

func TestCreate_CloudEventHTTP_AcceptsHTTPSURL(t *testing.T) {
	ts, s, _ := setupCloudEventTestServer(t)
	token := ceRegisterAndGetToken(t, ts, "CEOrg3", "ce3@test.com", "supersecret123")
	ceBumpToProByEmail(t, s, "ce3@test.com")

	body := map[string]interface{}{
		"name": "public-sink",
		"type": "cloudevent_http",
		"config": map[string]interface{}{
			"sink_url": "https://broker.example.com/ns/default",
			"mode":     "binary",
		},
		"enabled": true,
	}

	resp := doJSONReq(t, http.MethodPost, ts.URL+"/api/v1/notification-channels", token, body)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 for valid https sink_url, got %d", resp.StatusCode)
	}
}

func TestCreate_CloudEventHTTP_OIDC_RedactsClientSecretOnRead(t *testing.T) {
	ts, s, _ := setupCloudEventTestServer(t)
	token := ceRegisterAndGetToken(t, ts, "CEOrgRedact", "ceredact@test.com", "supersecret123")
	ceBumpToProByEmail(t, s, "ceredact@test.com")

	body := map[string]interface{}{
		"name": "oidc-sink",
		"type": "cloudevent_http",
		"config": map[string]interface{}{
			"sink_url": "https://broker.example.com/ns/default",
			"mode":     "binary",
			"auth": map[string]interface{}{
				"type": "oidc",
				"oidc": map[string]interface{}{
					"issuer":        "https://issuer.example.com",
					"token_url":     "https://issuer.example.com/token",
					"client_id":     "my-client-id",
					"client_secret": "super-secret-value",
					"audience":      "https://sink.example.com",
				},
			},
		},
		"enabled": true,
	}

	resp := doJSONReq(t, http.MethodPost, ts.URL+"/api/v1/notification-channels", token, body)
	if resp.StatusCode != http.StatusCreated {
		b := new(bytes.Buffer)
		_, _ = b.ReadFrom(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("create: expected 201, got %d: %s", resp.StatusCode, b.String())
	}
	var created struct {
		ID     string          `json:"id"`
		Config json.RawMessage `json:"config"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&created)
	_ = resp.Body.Close()
	if created.ID == "" {
		t.Fatal("empty channel id")
	}

	// GET the channel back and confirm redaction on read.
	resp2 := doJSONReq(t, http.MethodGet, ts.URL+"/api/v1/notification-channels/"+created.ID, token, nil)
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("get: expected 200, got %d", resp2.StatusCode)
	}
	var got struct {
		Config map[string]interface{} `json:"config"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	auth, _ := got.Config["auth"].(map[string]interface{})
	if auth == nil {
		t.Fatalf("expected auth in config, got %+v", got.Config)
	}
	oidc, _ := auth["oidc"].(map[string]interface{})
	if oidc == nil {
		t.Fatalf("expected auth.oidc in config, got %+v", auth)
	}
	if sec, _ := oidc["client_secret"].(string); sec != "••••••••" {
		t.Errorf("expected client_secret redacted, got %q", sec)
	}
	if iss, _ := oidc["issuer"].(string); iss != "https://issuer.example.com" {
		t.Errorf("expected issuer verbatim, got %q", iss)
	}
	if tu, _ := oidc["token_url"].(string); tu != "https://issuer.example.com/token" {
		t.Errorf("expected token_url verbatim, got %q", tu)
	}
	if cid, _ := oidc["client_id"].(string); cid != "my-client-id" {
		t.Errorf("expected client_id verbatim, got %q", cid)
	}
	if aud, _ := oidc["audience"].(string); aud != "https://sink.example.com" {
		t.Errorf("expected audience verbatim, got %q", aud)
	}
}

func TestUpdate_CloudEventHTTP_OIDC_PreservesClientSecretWhenSentinel(t *testing.T) {
	ts, store, _ := setupCloudEventTestServer(t)
	token := ceRegisterAndGetToken(t, ts, "CEOrgPreserve", "cepreserve@test.com", "supersecret123")
	ceBumpToProByEmail(t, store, "cepreserve@test.com")

	body := map[string]interface{}{
		"name": "oidc-sink",
		"type": "cloudevent_http",
		"config": map[string]interface{}{
			"sink_url": "https://broker.example.com/ns/default",
			"mode":     "binary",
			"auth": map[string]interface{}{
				"type": "oidc",
				"oidc": map[string]interface{}{
					"issuer":        "https://issuer.example.com",
					"token_url":     "https://issuer.example.com/token",
					"client_id":     "my-client-id",
					"client_secret": "original-secret",
					"audience":      "https://sink.example.com",
				},
			},
		},
		"enabled": true,
	}

	resp := doJSONReq(t, http.MethodPost, ts.URL+"/api/v1/notification-channels", token, body)
	if resp.StatusCode != http.StatusCreated {
		b := new(bytes.Buffer)
		_, _ = b.ReadFrom(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("create: expected 201, got %d: %s", resp.StatusCode, b.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&created)
	_ = resp.Body.Close()

	// PATCH with sentinel for client_secret; expect the existing secret preserved.
	patchBody := map[string]interface{}{
		"config": map[string]interface{}{
			"sink_url": "https://broker.example.com/ns/default",
			"mode":     "binary",
			"auth": map[string]interface{}{
				"type": "oidc",
				"oidc": map[string]interface{}{
					"issuer":        "https://issuer.example.com",
					"token_url":     "https://issuer.example.com/token",
					"client_id":     "my-client-id",
					"client_secret": "••••••••",
					"audience":      "https://sink.example.com",
				},
			},
		},
	}
	respU := doJSONReq(t, http.MethodPatch, ts.URL+"/api/v1/notification-channels/"+created.ID, token, patchBody)
	defer func() { _ = respU.Body.Close() }()
	if respU.StatusCode != http.StatusOK {
		b := new(bytes.Buffer)
		_, _ = b.ReadFrom(respU.Body)
		t.Fatalf("update: expected 200, got %d: %s", respU.StatusCode, b.String())
	}

	// Read back the stored config directly from the store (not via redacted API).
	ch, err := store.NotificationChannels().GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get stored channel: %v", err)
	}
	var stored map[string]interface{}
	if err := json.Unmarshal([]byte(ch.Config), &stored); err != nil {
		t.Fatalf("unmarshal stored config: %v", err)
	}
	auth, _ := stored["auth"].(map[string]interface{})
	oidcM, _ := auth["oidc"].(map[string]interface{})
	if sec, _ := oidcM["client_secret"].(string); sec != "original-secret" {
		t.Fatalf("expected client_secret preserved as 'original-secret', got %q", sec)
	}
}

func TestTest_CloudEventHTTP_BuildsValidCloudEventMessage(t *testing.T) {
	ts, s, sender := setupCloudEventTestServer(t)
	token := ceRegisterAndGetToken(t, ts, "CEOrg4", "ce4@test.com", "supersecret123")
	ceBumpToProByEmail(t, s, "ce4@test.com")

	// Create a cloudevent_http channel.
	createBody := map[string]interface{}{
		"name": "ce-test",
		"type": "cloudevent_http",
		"config": map[string]interface{}{
			"sink_url": "https://broker.example.com/ns/default",
			"mode":     "binary",
		},
		"enabled": true,
	}
	resp := doJSONReq(t, http.MethodPost, ts.URL+"/api/v1/notification-channels", token, createBody)
	if resp.StatusCode != http.StatusCreated {
		b := new(bytes.Buffer)
		_, _ = b.ReadFrom(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("create channel: expected 201, got %d: %s", resp.StatusCode, b.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&created)
	_ = resp.Body.Close()
	if created.ID == "" {
		t.Fatal("empty channel id")
	}

	// Invoke Test.
	resp2 := doJSONReq(t, http.MethodPost, ts.URL+"/api/v1/notification-channels/"+created.ID+"/test", token, map[string]interface{}{})
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		b := new(bytes.Buffer)
		_, _ = b.ReadFrom(resp2.Body)
		t.Fatalf("test: expected 200, got %d: %s", resp2.StatusCode, b.String())
	}

	// The TestSender should have received a job with a CloudEvent in Message.
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if !sender.seen {
		t.Fatal("TestSender.TestSend was not called")
	}
	if sender.job.Channel != "cloudevent_http" {
		t.Fatalf("expected job.Channel=cloudevent_http, got %q", sender.job.Channel)
	}
	if sender.job.Message == "" {
		t.Fatal("expected job.Message to contain CloudEvent JSON")
	}

	// Unmarshal job.Message as a CloudEvent and assert envelope.
	var ev ce.Event
	if err := json.Unmarshal([]byte(sender.job.Message), &ev); err != nil {
		t.Fatalf("job.Message is not valid CloudEvent JSON: %v (message=%q)", err, sender.job.Message)
	}
	if err := ev.Validate(); err != nil {
		t.Fatalf("CloudEvent validation failed: %v", err)
	}
	if ev.Type() != "run.yipyap.alert.fired.v1" {
		t.Fatalf("expected type=run.yipyap.alert.fired.v1, got %q", ev.Type())
	}
	if ev.SpecVersion() != "1.0" {
		t.Fatalf("expected specversion=1.0, got %q", ev.SpecVersion())
	}

	// Sanity-check that the job's TargetConfig is the channel config (base64 with 0x00 prefix).
	if sender.job.TargetConfig == "" {
		t.Fatal("expected TargetConfig to be set")
	}
	raw, err := base64.StdEncoding.DecodeString(sender.job.TargetConfig)
	if err != nil {
		t.Fatalf("TargetConfig base64 decode: %v", err)
	}
	if len(raw) < 2 || raw[0] != 0x00 {
		t.Fatalf("expected plaintext version byte 0x00 prefix, got first byte %x", raw[0])
	}
	if !strings.Contains(string(raw[1:]), "broker.example.com") {
		t.Fatalf("expected channel config to contain sink_url host, got %q", string(raw[1:]))
	}
}
