package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/crypto"
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

// setupChannelTestServer spins up an API server with registration enabled
// and an injected capturingTestSender.
func setupChannelTestServer(t *testing.T) (*httptest.Server, *sqlite.SQLiteStore, *capturingTestSender) {
	t.Helper()
	return setupChannelTestServerWith(t, nil)
}

// setupChannelTestServerWith is setupChannelTestServer with an
// optional notification envelope, needed to store contact method secrets.
func setupChannelTestServerWith(t *testing.T, env *crypto.Envelope) (*httptest.Server, *sqlite.SQLiteStore, *capturingTestSender) {
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
		Envelope:            env,
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return srv, s, ts
}

// signupAndGetToken registers a new org+user and returns the bearer token.
func signupAndGetToken(t *testing.T, ts *httptest.Server, orgName, email, password string) string {
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
