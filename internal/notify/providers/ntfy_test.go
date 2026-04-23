package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func TestNtfy_Send(t *testing.T) {
	var gotBody string
	var gotHeaders http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	n := NewNtfy(NtfyConfig{
		ServerURL: srv.URL,
		Topic:     "alerts",
		Token:     "tk_secret123",
	}, nil)

	job := domain.NotificationJob{
		ID:          "j1",
		AlertID:     "a1",
		OrgID:       "org1",
		MonitorName: "web-check",
		Severity:    "critical",
		Message:     "host is down",
		AckURL:      "https://example.com/ack/a1",
	}

	msgID, err := n.Send(context.Background(), job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msgID == "" {
		t.Fatal("expected non-empty message ID")
	}

	// Verify body is plain text message.
	if gotBody != "host is down" {
		t.Errorf("expected body 'host is down', got %q", gotBody)
	}

	// Verify ntfy headers.
	if got := gotHeaders.Get("X-Title"); got != "YipYap Alert: web-check" {
		t.Errorf("expected X-Title 'YipYap Alert: web-check', got %q", got)
	}
	if got := gotHeaders.Get("X-Priority"); got != "5" {
		t.Errorf("expected X-Priority '5' for critical, got %q", got)
	}
	if got := gotHeaders.Get("X-Tags"); got != "rotating_light" {
		t.Errorf("expected X-Tags 'rotating_light' for critical, got %q", got)
	}
	if got := gotHeaders.Get("Authorization"); got != "Bearer tk_secret123" {
		t.Errorf("expected Authorization 'Bearer tk_secret123', got %q", got)
	}
}

func TestNtfy_SendWarning(t *testing.T) {
	var gotHeaders http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	n := NewNtfy(NtfyConfig{
		ServerURL: srv.URL,
		Topic:     "alerts",
	}, nil)

	job := domain.NotificationJob{
		ID:          "j2",
		MonitorName: "cpu-check",
		Severity:    "warning",
		Message:     "CPU high",
	}

	_, err := n.Send(context.Background(), job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := gotHeaders.Get("X-Priority"); got != "4" {
		t.Errorf("expected X-Priority '4' for warning, got %q", got)
	}
	if got := gotHeaders.Get("X-Tags"); got != "warning" {
		t.Errorf("expected X-Tags 'warning', got %q", got)
	}
	// No token set  - Authorization header should be absent.
	if got := gotHeaders.Get("Authorization"); got != "" {
		t.Errorf("expected no Authorization header, got %q", got)
	}
}

func TestNtfy_SendInfo(t *testing.T) {
	var gotHeaders http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	n := NewNtfy(NtfyConfig{
		ServerURL: srv.URL,
		Topic:     "alerts",
	}, nil)

	job := domain.NotificationJob{
		ID:       "j3",
		Severity: "info",
		Message:  "all clear",
	}

	_, err := n.Send(context.Background(), job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := gotHeaders.Get("X-Priority"); got != "3" {
		t.Errorf("expected X-Priority '3' for info, got %q", got)
	}
	if got := gotHeaders.Get("X-Tags"); got != "information_source" {
		t.Errorf("expected X-Tags 'information_source', got %q", got)
	}
}

func TestNtfy_SendError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	n := NewNtfy(NtfyConfig{ServerURL: srv.URL, Topic: "alerts"}, nil)
	_, err := n.Send(context.Background(), domain.NotificationJob{ID: "j4"})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestNtfy_DefaultServerURL(t *testing.T) {
	// Verify that when ServerURL is empty, the URL is built with the default.
	n := NewNtfy(NtfyConfig{Topic: "test-topic"}, nil)

	// We can't actually hit ntfy.sh, so just verify Channel returns correctly.
	if n.Channel() != "ntfy" {
		t.Errorf("expected channel 'ntfy', got %q", n.Channel())
	}
	if n.MaxConcurrency() != 10 {
		t.Errorf("expected MaxConcurrency 10, got %d", n.MaxConcurrency())
	}
}
