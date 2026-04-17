package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func TestPushover_Send(t *testing.T) {
	var gotForm map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("expected Content-Type application/x-www-form-urlencoded, got %s", r.Header.Get("Content-Type"))
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", 400)
			return
		}
		gotForm = make(map[string]string)
		for k := range r.PostForm {
			gotForm[k] = r.PostForm.Get(k)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pushoverResponse{Status: 1, Request: "req-abc-123"})
	}))
	defer srv.Close()

	p := NewPushover(PushoverConfig{
		APIToken: "tok_app",
		UserKey:  "usr_key",
		Sound:    "cosmic",
		Device:   "iphone",
	}, nil)
	p.apiURL = srv.URL

	job := domain.NotificationJob{
		ID:          "j1",
		AlertID:     "a1",
		OrgID:       "org1",
		MonitorName: "api-health",
		Severity:    "warning",
		Message:     "Latency above threshold",
		AckURL:      "https://example.com/ack/a1",
	}

	reqID, err := p.Send(context.Background(), job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reqID != "req-abc-123" {
		t.Errorf("expected request ID req-abc-123, got %s", reqID)
	}

	// Verify form fields.
	checks := map[string]string{
		"token":     "tok_app",
		"user":      "usr_key",
		"title":     "YipYap: api-health",
		"message":   "Latency above threshold",
		"priority":  "1",
		"sound":     "cosmic",
		"device":    "iphone",
		"url":       "https://example.com/ack/a1",
		"url_title": "View in YipYap",
	}
	for k, want := range checks {
		if got := gotForm[k]; got != want {
			t.Errorf("form field %q: want %q, got %q", k, want, got)
		}
	}

	// Warning priority should NOT have retry/expire.
	if _, ok := gotForm["retry"]; ok {
		t.Error("unexpected retry field for warning priority")
	}
}

func TestPushover_SendCritical(t *testing.T) {
	var gotForm map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = make(map[string]string)
		for k := range r.PostForm {
			gotForm[k] = r.PostForm.Get(k)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pushoverResponse{Status: 1, Request: "req-crit"})
	}))
	defer srv.Close()

	p := NewPushover(PushoverConfig{
		APIToken: "tok",
		UserKey:  "usr",
	}, nil)
	p.apiURL = srv.URL

	job := domain.NotificationJob{
		ID:          "j2",
		MonitorName: "db-check",
		Severity:    "critical",
		Message:     "Database unreachable",
	}

	reqID, err := p.Send(context.Background(), job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reqID != "req-crit" {
		t.Errorf("expected request ID req-crit, got %s", reqID)
	}

	// Critical = priority 2 = emergency.
	if gotForm["priority"] != "2" {
		t.Errorf("expected priority 2, got %s", gotForm["priority"])
	}
	if gotForm["retry"] != "60" {
		t.Errorf("expected retry 60, got %s", gotForm["retry"])
	}
	if gotForm["expire"] != "3600" {
		t.Errorf("expected expire 3600, got %s", gotForm["expire"])
	}

	// No ack URL → no url/url_title fields.
	if _, ok := gotForm["url"]; ok {
		t.Error("unexpected url field when AckURL is empty")
	}
	if _, ok := gotForm["url_title"]; ok {
		t.Error("unexpected url_title field when AckURL is empty")
	}

	// No sound/device configured → not sent.
	if _, ok := gotForm["sound"]; ok {
		t.Error("unexpected sound field when not configured")
	}
	if _, ok := gotForm["device"]; ok {
		t.Error("unexpected device field when not configured")
	}
}

func TestPushover_SendError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pushoverResponse{Status: 0, Request: "req-fail"})
	}))
	defer srv.Close()

	p := NewPushover(PushoverConfig{APIToken: "tok", UserKey: "usr"}, nil)
	p.apiURL = srv.URL

	_, err := p.Send(context.Background(), domain.NotificationJob{ID: "j3", Severity: "info"})
	if err == nil {
		t.Fatal("expected error for status 0 response")
	}
}

func TestPushover_SeverityMapping(t *testing.T) {
	tests := []struct {
		severity string
		want     string
	}{
		{"critical", "2"},
		{"warning", "1"},
		{"info", "0"},
		{"", "0"},
		{"unknown", "0"},
	}
	for _, tt := range tests {
		got := severityToPriority(tt.severity)
		if got != tt.want {
			t.Errorf("severityToPriority(%q) = %q, want %q", tt.severity, got, tt.want)
		}
	}
}
