package handlers_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
)

func strictPrivateTargets(t *testing.T) {
	t.Helper()
	orig := checker.AllowPrivateTargets
	checker.AllowPrivateTargets = false
	t.Cleanup(func() { checker.AllowPrivateTargets = orig })
}

func TestChannelURLs_RejectPrivateTargets(t *testing.T) {
	strictPrivateTargets(t)
	srv, _, _ := setupChannelTestServer(t)
	token := signupAndGetToken(t, srv, "SSRFOrg", "ssrf@test.com", "supersecret123")

	create := func(typ string, cfg map[string]interface{}) (int, string) {
		resp := doJSONReq(t, http.MethodPost, srv.URL+"/api/v1/notification-channels", token, map[string]interface{}{
			"name": typ, "type": typ, "enabled": true, "config": cfg,
		})
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}

	for _, tc := range []struct {
		typ   string
		field string
		cfg   map[string]interface{}
	}{
		{"ntfy", "server_url", map[string]interface{}{"server_url": "http://169.254.169.254", "topic": "t"}},
		{"ntfy", "server_url", map[string]interface{}{"server_url": "http://10.0.0.5:8080", "topic": "t"}},
		{"ntfy", "server_url", map[string]interface{}{"server_url": "http://127.0.0.1", "topic": "t"}},
		{"webhook", "url", map[string]interface{}{"url": "http://10.0.0.5/hook"}},
		{"webhook", "url", map[string]interface{}{"url": "http://[::1]/hook"}},
	} {
		code, body := create(tc.typ, tc.cfg)
		if code != http.StatusBadRequest {
			t.Errorf("%s %v: status %d (%s), want 400", tc.typ, tc.cfg, code, body)
			continue
		}
		// The SPA maps this exact text onto the field
		// (web/src/pages/settings/channel-config-logic.mjs).
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal([]byte(body), &e)
		if want := tc.field + " cannot point to a private or internal address"; e.Error != want {
			t.Errorf("%s %v: error %q, want %q", tc.typ, tc.cfg, e.Error, want)
		}
	}

	// Update path: a public ntfy channel cannot be moved to a private server.
	code, body := create("ntfy", map[string]interface{}{"server_url": "https://ntfy.sh", "topic": "t"})
	if code != http.StatusCreated {
		t.Fatalf("create public ntfy: status %d (%s)", code, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(body), &created)
	resp := doJSONReq(t, http.MethodPatch, srv.URL+"/api/v1/notification-channels/"+created.ID, token, map[string]interface{}{
		"config": map[string]interface{}{"server_url": "http://169.254.169.254", "topic": "t"},
	})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("update ntfy to metadata address: status %d, want 400", resp.StatusCode)
	}
}

func TestChannelTest_ReturnsGenericError(t *testing.T) {
	srv, _, sender := setupChannelTestServer(t)
	token := signupAndGetToken(t, srv, "TestErrOrg", "testerr@test.com", "supersecret123")

	resp := doJSONReq(t, http.MethodPost, srv.URL+"/api/v1/notification-channels", token, map[string]interface{}{
		"name": "hook", "type": "webhook", "enabled": true, "config": map[string]interface{}{"url": "https://example.com/x"},
	})
	var created struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&created)
	_ = resp.Body.Close()
	if created.ID == "" {
		t.Fatal("channel not created")
	}

	cases := []struct {
		err  error
		want string
	}{
		{fmt.Errorf("webhook: %w", &notify.StatusError{Provider: "webhook", StatusCode: 503, Detail: "<h1>admin panel at 10.0.0.7</h1>"}), "HTTP 5xx"},
		{&url.Error{Op: "Post", URL: "http://10.0.0.7:9200/", Err: &net.OpError{Op: "dial", Net: "tcp", Err: fmt.Errorf("connect: connection refused")}}, "could not connect"},
		{fmt.Errorf("webhook POST: %w", fmt.Errorf("%w: 10.0.0.7 (rfc1918)", checker.ErrSSRFBlocked)), "not allowed"},
	}
	for _, tc := range cases {
		sender.mu.Lock()
		sender.err = tc.err
		sender.mu.Unlock()

		resp := doJSONReq(t, http.MethodPost, srv.URL+"/api/v1/notification-channels/"+created.ID+"/test", token, nil)
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		body := string(raw)
		if resp.StatusCode != http.StatusBadGateway {
			t.Errorf("status %d, want 502", resp.StatusCode)
		}
		if !strings.Contains(body, tc.want) {
			t.Errorf("body %s, want it to contain %q", body, tc.want)
		}
		for _, leak := range []string{"10.0.0.7", "admin panel", "9200", "rfc1918"} {
			if strings.Contains(body, leak) {
				t.Errorf("body %s leaks %q", body, leak)
			}
		}
	}
}
