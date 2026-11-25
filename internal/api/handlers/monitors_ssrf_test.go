package handlers_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
)

// TestMonitorCreate_UnresolvableTargetRejected is the mon-4 save-time
// regression: a target name that does not resolve used to pass SSRF
// validation, so it could be pointed at an internal address later. .invalid
// is reserved (RFC 6761) and never resolves.
func TestMonitorCreate_UnresolvableTargetRejected(t *testing.T) {
	orig := checker.AllowPrivateTargets
	checker.AllowPrivateTargets = false
	defer func() { checker.AllowPrivateTargets = orig }()

	ts, s, _ := setupCloudEventTestServer(t)
	token := ceRegisterAndGetToken(t, ts, "SSRFOrg", "ssrf@test.com", "supersecret123")
	ceBumpToProByEmail(t, s, "ssrf@test.com")

	create := func(typ string, cfg map[string]any) (int, string) {
		resp := doJSONReq(t, http.MethodPost, ts.URL+"/api/v1/monitors", token, map[string]any{
			"name":             typ + " target",
			"type":             typ,
			"config":           cfg,
			"interval_seconds": 300,
			"timeout_seconds":  10,
			"enabled":          true,
		})
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	rejected := []struct {
		typ string
		cfg map[string]any
	}{
		{"http", map[string]any{"url": "https://does-not-exist.invalid/health"}},
		{"tcp", map[string]any{"host": "does-not-exist.invalid", "port": 443}},
		{"ping", map[string]any{"host": "does-not-exist.invalid"}},
		{"dns", map[string]any{"hostname": "example.com", "record_type": "A", "nameserver": "does-not-exist.invalid"}},
	}
	for _, tc := range rejected {
		code, body := create(tc.typ, tc.cfg)
		if code != http.StatusBadRequest {
			t.Errorf("%s: status = %d (%s), want 400", tc.typ, code, body)
			continue
		}
		if !strings.Contains(body, "does not resolve") {
			t.Errorf("%s: body = %s, want a does-not-resolve error", tc.typ, body)
		}
	}

	// Private literals are still refused at save time.
	if code, body := create("http", map[string]any{"url": "http://127.0.0.1:8080/"}); code != http.StatusBadRequest {
		t.Errorf("loopback http: status = %d (%s), want 400", code, body)
	}

	// A DNS monitor's query name is looked up, never dialed, so a name
	// without address records is accepted.
	if code, body := create("dns", map[string]any{"hostname": "does-not-exist.invalid", "record_type": "TXT"}); code != http.StatusCreated {
		t.Errorf("dns query name: status = %d (%s), want 201", code, body)
	}
}

// TestMonitorTargetFieldErrors checks the 400 shape the monitor form relies
// on: "field" and "fields" name the config input that was refused, so the
// form can mark it, and the text says what to do. Covers create and update.
func TestMonitorTargetFieldErrors(t *testing.T) {
	orig := checker.AllowPrivateTargets
	checker.AllowPrivateTargets = false
	defer func() { checker.AllowPrivateTargets = orig }()

	ts, s, _ := setupCloudEventTestServer(t)
	token := ceRegisterAndGetToken(t, ts, "SSRFFieldOrg", "ssrf-field@test.com", "supersecret123")
	ceBumpToProByEmail(t, s, "ssrf-field@test.com")

	send := func(method, path string, body map[string]any) (int, map[string]any) {
		resp := doJSONReq(t, method, ts.URL+path, token, body)
		defer func() { _ = resp.Body.Close() }()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	monitor := func(typ string, cfg map[string]any) map[string]any {
		return map[string]any{
			"name": typ + " target", "type": typ, "config": cfg,
			"interval_seconds": 300, "timeout_seconds": 10, "enabled": true,
		}
	}

	cases := []struct {
		name, typ, field, want string
		cfg                    map[string]any
	}{
		{"http unresolved", "http", "config.url", "does-not-exist.invalid does not resolve", map[string]any{"url": "https://does-not-exist.invalid/"}},
		{"http loopback", "http", "config.url", "public internet", map[string]any{"url": "http://127.0.0.1:8080/"}},
		{"tcp unresolved", "tcp", "config.host", "does not resolve", map[string]any{"host": "does-not-exist.invalid", "port": 443}},
		{"ping private", "ping", "config.host", "private or internal", map[string]any{"host": "10.0.0.5"}},
		{"dns nameserver", "dns", "config.nameserver", "does not resolve", map[string]any{"hostname": "example.com", "record_type": "A", "nameserver": "ns.does-not-exist.invalid"}},
		{"dns query name private", "dns", "config.hostname", "private or internal", map[string]any{"hostname": "localhost", "record_type": "A"}},
		{"bad scheme", "http", "config.url", "Unsupported URL scheme", map[string]any{"url": "ftp://example.com/"}},
	}
	for _, tc := range cases {
		code, out := send(http.MethodPost, "/api/v1/monitors", monitor(tc.typ, tc.cfg))
		if code != http.StatusBadRequest {
			t.Errorf("%s: status = %d (%v), want 400", tc.name, code, out)
			continue
		}
		fields, _ := out["fields"].(map[string]any)
		msg, _ := fields[tc.field].(string)
		if out["field"] != tc.field || !strings.Contains(msg, tc.want) {
			t.Errorf("%s: field = %v fields = %v, want %s containing %q", tc.name, out["field"], fields, tc.field, tc.want)
		}
		if out["error"] != msg {
			t.Errorf("%s: error = %v, want it to match the field message %q", tc.name, out["error"], msg)
		}
	}

	// Update reports the same shape.
	code, out := send(http.MethodPost, "/api/v1/monitors", monitor("tcp", map[string]any{"host": "1.1.1.1", "port": 443}))
	if code != http.StatusCreated {
		t.Fatalf("create: status = %d (%v)", code, out)
	}
	id, _ := out["id"].(string)
	if id == "" {
		if m, ok := out["monitor"].(map[string]any); ok {
			id, _ = m["id"].(string)
		}
	}
	code, out = send(http.MethodPatch, "/api/v1/monitors/"+id, map[string]any{"config": map[string]any{"host": "127.0.0.1", "port": 22}})
	if code != http.StatusBadRequest || out["field"] != "config.host" {
		t.Errorf("update: status = %d field = %v (%v), want 400 on config.host", code, out["field"], out)
	}
}
