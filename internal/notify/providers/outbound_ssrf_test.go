package providers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
)

// strictTargets turns the monitor-target allow toggle off for one test,
// which is the SaaS default.
func strictTargets(t *testing.T) {
	t.Helper()
	orig := checker.AllowPrivateTargets
	checker.AllowPrivateTargets = false
	t.Cleanup(func() { checker.AllowPrivateTargets = orig })
}

func rawJob(cfg string) domain.NotificationJob {
	return domain.NotificationJob{ID: "j", AlertID: "a", MonitorName: "m", Severity: "critical", Message: "down", TargetConfig: cfg}
}

func TestWebhook_RefusesPrivateDestinations(t *testing.T) {
	strictTargets(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	defer srv.Close()

	wh := NewWebhook(WebhookConfig{}, nil)
	for _, u := range []string{
		srv.URL,
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/hook",
		"http://localhost:8080/hook",
	} {
		_, err := wh.Send(context.Background(), rawJob(`{"url":"`+u+`"}`))
		if !errors.Is(err, checker.ErrSSRFBlocked) {
			t.Errorf("Send(%s) err = %v, want ErrSSRFBlocked", u, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("loopback server was reached %d times", hits.Load())
	}
}

func TestWebhook_DoesNotFollowRedirects(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { targetHits.Add(1) }))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	wh := NewWebhook(WebhookConfig{}, nil)
	_, err := wh.Send(context.Background(), rawJob(`{"url":"`+redirector.URL+`","headers":{"Authorization":"Bearer secret"}}`))
	var se *notify.StatusError
	if !errors.As(err, &se) || se.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("err = %v, want a 307 StatusError", err)
	}
	if targetHits.Load() != 0 {
		t.Fatal("redirect was followed")
	}
}

func TestNtfy_RefusesPrivateServer(t *testing.T) {
	strictTargets(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	defer srv.Close()

	n := NewNtfy(NtfyConfig{}, nil)
	for _, u := range []string{srv.URL, "http://169.254.169.254", "http://192.168.1.10"} {
		_, err := n.Send(context.Background(), rawJob(`{"server_url":"`+u+`","topic":"t"}`))
		if !errors.Is(err, checker.ErrSSRFBlocked) {
			t.Errorf("Send(%s) err = %v, want ErrSSRFBlocked", u, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("loopback ntfy server was reached")
	}
}

func TestNtfy_DoesNotFollowRedirects(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { targetHits.Add(1) }))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/t", http.StatusFound)
	}))
	defer redirector.Close()

	_, err := NewNtfy(NtfyConfig{}, nil).Send(context.Background(), rawJob(`{"server_url":"`+redirector.URL+`","topic":"t"}`))
	if err == nil || targetHits.Load() != 0 {
		t.Fatalf("err = %v, target hits = %d; want an error and no follow", err, targetHits.Load())
	}
}

func TestWebhook_StatusErrorKeepsBodyOutOfPublicMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("internal-secret-banner 10.0.0.7"))
	}))
	defer srv.Close()

	_, err := NewWebhook(WebhookConfig{}, nil).Send(context.Background(), rawJob(`{"url":"`+srv.URL+`"}`))
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := notify.PublicDeliveryError(err)
	if strings.Contains(msg, "internal-secret-banner") || strings.Contains(msg, "10.0.0.7") || strings.Contains(msg, "127.0.0.1") {
		t.Fatalf("public message leaks detail: %q", msg)
	}
	if !strings.Contains(msg, "5xx") {
		t.Fatalf("public message = %q, want the 5xx class", msg)
	}
}
