package handlers_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type patchedMonitor struct {
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	Enabled            bool              `json:"enabled"`
	EscalationPolicyID string            `json:"escalation_policy_id"`
	IntervalSeconds    int               `json:"interval_seconds"`
	TimeoutSeconds     int               `json:"timeout_seconds"`
	Description        string            `json:"description"`
	AutoResolve        bool              `json:"auto_resolve"`
	RunbookURL         string            `json:"runbook_url"`
	LatencyWarningMS   int               `json:"latency_warning_ms"`
	Labels             map[string]string `json:"labels"`
}

func patchMonitor(t *testing.T, url, token, id string, body any) (int, patchedMonitor, string) {
	t.Helper()
	resp := doJSONReq(t, http.MethodPatch, url+"/api/v1/monitors/"+id, token, body)
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var m patchedMonitor
	_ = json.Unmarshal(raw, &m)
	return resp.StatusCode, m, string(raw)
}

func getMonitor(t *testing.T, url, token, id string) patchedMonitor {
	t.Helper()
	resp := doJSONReq(t, http.MethodGet, url+"/api/v1/monitors/"+id, token, nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get monitor: %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	var m patchedMonitor
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode monitor: %v (%q)", err, raw)
	}
	return m
}

// createPatchTestPolicy creates an escalation policy. createPolicyHTTP is
// SaaS-only, and this file runs in both builds.
func createPatchTestPolicy(t *testing.T, url, token string) string {
	t.Helper()
	resp := doJSONReq(t, http.MethodPost, url+"/api/v1/escalation-policies", token, map[string]any{"name": "Primary"})
	defer func() { _ = resp.Body.Close() }()
	var p struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil || resp.StatusCode != http.StatusCreated || p.ID == "" {
		t.Fatalf("create policy: %d %v", resp.StatusCode, err)
	}
	return p.ID
}

// TestMonitorPatch_OmittedFieldsKeepValues is the mon-9 regression: PATCH
// decoded into domain.Monitor, so a body with only a name set enabled to
// false and cleared the escalation policy, description and runbook.
func TestMonitorPatch_OmittedFieldsKeepValues(t *testing.T) {
	ts, s, _ := setupChannelTestServer(t)
	token := signupAndGetToken(t, ts, "PatchOrg", "patch@test.com", "supersecret123")
	bumpToProByEmail(t, s, "patch@test.com")
	policyID := createPatchTestPolicy(t, ts.URL, token)

	resp := doJSONReq(t, http.MethodPost, ts.URL+"/api/v1/monitors", token, map[string]any{
		"name":                 "api health",
		"type":                 "heartbeat",
		"interval_seconds":     300,
		"timeout_seconds":      10,
		"enabled":              true,
		"auto_resolve":         true,
		"description":          "checks the api",
		"runbook_url":          "https://wiki.example.com/runbook",
		"latency_warning_ms":   500,
		"escalation_policy_id": policyID,
		"labels":               map[string]string{"env": "prod"},
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create monitor: %d %s", resp.StatusCode, raw)
	}
	var created patchedMonitor
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	code, _, body := patchMonitor(t, ts.URL, token, created.ID, map[string]any{"name": "api health renamed"})
	if code != http.StatusOK {
		t.Fatalf("name-only patch: %d %s", code, body)
	}
	got := getMonitor(t, ts.URL, token, created.ID)
	if got.Name != "api health renamed" {
		t.Errorf("name = %q, want the new name", got.Name)
	}
	if !got.Enabled {
		t.Error("name-only PATCH disabled the monitor")
	}
	if got.EscalationPolicyID != policyID {
		t.Errorf("name-only PATCH changed escalation_policy_id to %q, want %q", got.EscalationPolicyID, policyID)
	}
	if got.IntervalSeconds != 300 || got.TimeoutSeconds != 10 {
		t.Errorf("interval/timeout = %d/%d, want 300/10", got.IntervalSeconds, got.TimeoutSeconds)
	}
	if !got.AutoResolve || got.Description != "checks the api" || got.RunbookURL == "" || got.LatencyWarningMS != 500 {
		t.Errorf("name-only PATCH cleared other fields: %+v", got)
	}
	if got.Labels["env"] != "prod" {
		t.Errorf("name-only PATCH dropped labels: %+v", got.Labels)
	}

	// Explicit values still apply: false disables, "" clears the policy.
	code, _, body = patchMonitor(t, ts.URL, token, created.ID, map[string]any{
		"enabled":              false,
		"escalation_policy_id": "",
		"latency_warning_ms":   0,
		"labels":               map[string]string{},
	})
	if code != http.StatusOK {
		t.Fatalf("explicit patch: %d %s", code, body)
	}
	got = getMonitor(t, ts.URL, token, created.ID)
	if got.Enabled || got.EscalationPolicyID != "" || got.LatencyWarningMS != 0 || len(got.Labels) != 0 {
		t.Errorf("explicit values not applied: %+v", got)
	}
	if got.Name != "api health renamed" || got.Description != "checks the api" {
		t.Errorf("explicit patch touched fields it did not send: %+v", got)
	}
}

// TestMonitorPatch_RejectsBadValues checks the 400s the pointer struct
// makes possible: a sent-but-invalid value is an error, not a no-op.
func TestMonitorPatch_RejectsBadValues(t *testing.T) {
	ts, s, _ := setupChannelTestServer(t)
	token := signupAndGetToken(t, ts, "PatchBad", "patchbad@test.com", "supersecret123")
	bumpToProByEmail(t, s, "patchbad@test.com")

	resp := doJSONReq(t, http.MethodPost, ts.URL+"/api/v1/monitors", token, map[string]any{
		"name": "hb", "type": "heartbeat", "interval_seconds": 300, "enabled": true,
	})
	defer func() { _ = resp.Body.Close() }()
	var created patchedMonitor
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil || created.ID == "" {
		t.Fatalf("create monitor: %d %v", resp.StatusCode, err)
	}

	cases := []struct {
		body map[string]any
		want string
	}{
		{map[string]any{"name": ""}, "name cannot be empty"},
		{map[string]any{"type": "http"}, "type cannot be changed"},
		{map[string]any{"interval_seconds": 0}, "interval_seconds must be greater than 0"},
		{map[string]any{"timeout_seconds": -1}, "timeout_seconds must be greater than 0"},
		{map[string]any{"latency_critical_ms": -5}, "cannot be negative"},
		{map[string]any{"escalation_policy_id": "not-a-policy"}, "escalation_policy_id"},
	}
	for _, tc := range cases {
		code, _, body := patchMonitor(t, ts.URL, token, created.ID, tc.body)
		if code != http.StatusBadRequest || !strings.Contains(body, tc.want) {
			t.Errorf("PATCH %v: %d %s, want 400 containing %q", tc.body, code, body, tc.want)
		}
	}

	// Sending the current type is allowed (the SPA sends the full form).
	if code, _, body := patchMonitor(t, ts.URL, token, created.ID, map[string]any{"type": "heartbeat", "name": "hb2"}); code != http.StatusOK {
		t.Errorf("same-type patch: %d %s", code, body)
	}
	if got := getMonitor(t, ts.URL, token, created.ID); !got.Enabled || got.Name != "hb2" {
		t.Errorf("after rejected patches: %+v, want enabled and renamed", got)
	}
}
