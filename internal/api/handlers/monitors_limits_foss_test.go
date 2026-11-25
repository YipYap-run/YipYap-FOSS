package handlers_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
)

func withOperatorLimits(t *testing.T, l handlers.OperatorLimits) {
	t.Helper()
	handlers.SetOperatorLimits(l)
	t.Cleanup(func() { handlers.SetOperatorLimits(handlers.OperatorLimits{}) })
}

func createHeartbeat(t *testing.T, url, token string, interval int) (int, string) {
	t.Helper()
	body := map[string]any{"name": "hb", "type": "heartbeat", "enabled": true}
	if interval != 0 {
		body["interval_seconds"] = interval
	}
	resp := doJSONReq(t, http.MethodPost, url+"/api/v1/monitors", token, body)
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// assertCapCopy checks decision 7's wording: name the setting, point at
// the hosted product, never ask for an upgrade.
func assertCapCopy(t *testing.T, body, envVar string) {
	t.Helper()
	if !strings.Contains(body, envVar) {
		t.Errorf("cap message %s does not name %s", body, envVar)
	}
	if !strings.Contains(body, "https://yipyap.run/pricing") {
		t.Errorf("cap message %s does not point at the hosted product", body)
	}
	if strings.Contains(strings.ToLower(body), "upgrade") {
		t.Errorf("cap message %s mentions an upgrade FOSS does not have", body)
	}
}

// TestFOSSMonitorLimits_NoCapByDefault is the mon-14 / decision 7 FOSS
// regression: FOSS used the free plan's 5-monitor cap and 300s floor with
// an "Upgrade" message that led nowhere.
func TestFOSSMonitorLimits_NoCapByDefault(t *testing.T) {
	withOperatorLimits(t, handlers.OperatorLimits{})
	ts, _, _ := setupCloudEventTestServer(t)
	token := ceRegisterAndGetToken(t, ts, "FossOrg", "foss@test.com", "supersecret123")

	for i := 0; i < 8; i++ {
		if code, body := createHeartbeat(t, ts.URL, token, 30); code != http.StatusCreated {
			t.Fatalf("monitor %d: %d %s, want 201 with no cap", i+1, code, body)
		}
	}
	// The default floor is the technical one, not the free plan's 300s.
	if code, body := createHeartbeat(t, ts.URL, token, handlers.DefaultMinCheckIntervalSeconds); code != http.StatusCreated {
		t.Fatalf("create at the default floor: %d %s", code, body)
	}
	code, body := createHeartbeat(t, ts.URL, token, handlers.DefaultMinCheckIntervalSeconds-1)
	if code != http.StatusForbidden {
		t.Fatalf("create below the default floor: %d %s, want 403", code, body)
	}
	assertCapCopy(t, body, "YIPYAP_MIN_CHECK_INTERVAL_SECONDS")

	// An omitted interval gets 60s, not the floor.
	if code, body := createHeartbeat(t, ts.URL, token, 0); code != http.StatusCreated || !strings.Contains(body, `"interval_seconds":60`) {
		t.Fatalf("create without interval: %d %s, want 201 with 60s", code, body)
	}

	// No channel cap either. A private literal URL keeps the check off
	// DNS; FOSS allows private targets by default.
	origPrivate := checker.AllowPrivateTargets
	checker.AllowPrivateTargets = true
	defer func() { checker.AllowPrivateTargets = origPrivate }()
	for i := 0; i < 7; i++ {
		resp := doJSONReq(t, http.MethodPost, ts.URL+"/api/v1/notification-channels", token, map[string]any{
			"name": "hook", "type": "webhook", "config": map[string]any{"url": "http://127.0.0.1:9/hook"},
		})
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("channel %d: %d %s, want 201 with no cap", i+1, resp.StatusCode, raw)
		}
	}
}

// TestFOSSMonitorLimits_OperatorCap checks a configured cap is enforced on
// create and import, and that its message explains the setting and does
// not trigger the SPA's upgrade prompt.
func TestFOSSMonitorLimits_OperatorCap(t *testing.T) {
	withOperatorLimits(t, handlers.OperatorLimits{MaxMonitorsPerOrg: 2, MaxNotificationChannelsPerOrg: 1, MinCheckIntervalSeconds: 120})
	ts, _, _ := setupCloudEventTestServer(t)
	token := ceRegisterAndGetToken(t, ts, "FossCap", "fosscap@test.com", "supersecret123")

	// The SPA reads the caps from meta to set the form floor.
	metaResp := doJSONReq(t, http.MethodGet, ts.URL+"/api/v1/meta", "", nil)
	metaRaw, _ := io.ReadAll(metaResp.Body)
	_ = metaResp.Body.Close()
	if !strings.Contains(string(metaRaw), `"min_check_interval_seconds":120`) || !strings.Contains(string(metaRaw), `"max_monitors_per_org":2`) {
		t.Errorf("meta does not report the operator caps: %s", metaRaw)
	}

	code, body := createHeartbeat(t, ts.URL, token, 60)
	if code != http.StatusForbidden || !strings.Contains(body, "120s") {
		t.Fatalf("create below the operator floor: %d %s, want 403 naming 120s", code, body)
	}
	assertCapCopy(t, body, "YIPYAP_MIN_CHECK_INTERVAL_SECONDS")

	for i := 0; i < 2; i++ {
		if code, body := createHeartbeat(t, ts.URL, token, 120); code != http.StatusCreated {
			t.Fatalf("monitor %d: %d %s", i+1, code, body)
		}
	}
	code, body = createHeartbeat(t, ts.URL, token, 120)
	if code != http.StatusForbidden || !strings.Contains(body, "up to 2 monitors") {
		t.Fatalf("third monitor: %d %s, want 403 naming the cap", code, body)
	}
	assertCapCopy(t, body, "YIPYAP_MAX_MONITORS_PER_ORG")
	if strings.Contains(body, "upgrade_required") {
		t.Errorf("operator cap set upgrade_required: %s", body)
	}

	code, res := postImport(t, ts.URL, token, map[string]any{"monitors": []map[string]any{
		{"name": "imp", "type": "heartbeat", "interval_seconds": 120},
	}})
	if code != http.StatusForbidden || res.UpgradeRequired {
		t.Fatalf("import past the cap: %d %+v, want 403 without upgrade_required", code, res)
	}
	assertCapCopy(t, res.Error, "YIPYAP_MAX_MONITORS_PER_ORG")

	origPrivate := checker.AllowPrivateTargets
	checker.AllowPrivateTargets = true
	defer func() { checker.AllowPrivateTargets = origPrivate }()
	addChannel := func() (int, string) {
		resp := doJSONReq(t, http.MethodPost, ts.URL+"/api/v1/notification-channels", token, map[string]any{
			"name": "hook", "type": "webhook", "config": map[string]any{"url": "http://127.0.0.1:9/hook"},
		})
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}
	if code, body := addChannel(); code != http.StatusCreated {
		t.Fatalf("first channel: %d %s", code, body)
	}
	code, body = addChannel()
	if code != http.StatusForbidden || !strings.Contains(body, "up to 1 notification channels") {
		t.Fatalf("second channel: %d %s, want 403 naming the cap", code, body)
	}
	assertCapCopy(t, body, "YIPYAP_MAX_NOTIFICATION_CHANNELS_PER_ORG")
}
