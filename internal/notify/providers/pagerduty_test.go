package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func newPagerDutyTestServer(t *testing.T, status int, response string) (*httptest.Server, *pagerDutyEvent) {
	t.Helper()
	var captured pagerDutyEvent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "bad content type", http.StatusBadRequest)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)
	return srv, &captured
}

func TestPagerDuty_TriggerPayload(t *testing.T) {
	srv, captured := newPagerDutyTestServer(t, http.StatusAccepted, `{"status":"success","dedup_key":"a1"}`)

	pd := NewPagerDuty(PagerDutyConfig{RoutingKey: "R0ABCDEF0123456789ABCDEF012345678"}, nil)
	pd.apiURL = srv.URL

	job := domain.NotificationJob{
		ID:          "j1",
		AlertID:     "a1",
		OrgID:       "org1",
		MonitorName: "api",
		Severity:    "critical",
		Message:     "host is down",
		RunbookURL:  "https://example.com/runbook",
		AckURL:      "https://example.com/ack/a1",
		ServiceName: "payments",
	}
	dedup, err := pd.Send(context.Background(), job)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if dedup != "a1" {
		t.Fatalf("dedup_key = %q", dedup)
	}
	if captured.EventAction != "trigger" {
		t.Errorf("event_action = %q", captured.EventAction)
	}
	if captured.RoutingKey != "R0ABCDEF0123456789ABCDEF012345678" {
		t.Errorf("routing_key = %q", captured.RoutingKey)
	}
	if captured.DedupKey != "a1" {
		t.Errorf("dedup_key = %q", captured.DedupKey)
	}
	if captured.Payload == nil {
		t.Fatal("trigger has no payload")
	}
	if captured.Payload.Severity != "critical" || captured.Payload.Source != "yipyap" {
		t.Errorf("payload = %+v", captured.Payload)
	}
	if !strings.Contains(captured.Payload.Summary, "host is down") ||
		!strings.Contains(captured.Payload.Summary, "api") {
		t.Errorf("summary = %q", captured.Payload.Summary)
	}
	if captured.Payload.CustomDetails["runbook_url"] != "https://example.com/runbook" {
		t.Errorf("custom details = %v", captured.Payload.CustomDetails)
	}
	if len(captured.Links) != 2 || captured.Links[0].Text != "Runbook" {
		t.Errorf("links = %+v", captured.Links)
	}
}

func TestPagerDuty_ResolveEvent(t *testing.T) {
	srv, captured := newPagerDutyTestServer(t, http.StatusAccepted, `{"status":"success"}`)

	pd := NewPagerDuty(PagerDutyConfig{RoutingKey: "R0ABCDEF0123456789ABCDEF012345678"}, nil)
	pd.apiURL = srv.URL

	_, err := pd.Send(context.Background(), domain.NotificationJob{
		AlertID:     "a1",
		DedupeKey:   "dedupe-1",
		EventAction: "resolve",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if captured.EventAction != "resolve" {
		t.Errorf("event_action = %q", captured.EventAction)
	}
	if captured.DedupKey != "dedupe-1" {
		t.Errorf("dedup_key = %q", captured.DedupKey)
	}
	if captured.Payload != nil {
		t.Errorf("resolve should not carry a payload: %+v", captured.Payload)
	}
}

func TestPagerDuty_RejectsAPIError(t *testing.T) {
	srv, _ := newPagerDutyTestServer(t, http.StatusBadRequest, `{"status":"invalid event","message":"bad routing key"}`)

	pd := NewPagerDuty(PagerDutyConfig{RoutingKey: "R0ABCDEF0123456789ABCDEF012345678"}, nil)
	pd.apiURL = srv.URL

	if _, err := pd.Send(context.Background(), domain.NotificationJob{AlertID: "a1"}); err == nil {
		t.Fatal("expected an error for a rejected event")
	}
}

func TestPagerDuty_RequiresRoutingKey(t *testing.T) {
	pd := NewPagerDuty(PagerDutyConfig{}, nil)
	if _, err := pd.Send(context.Background(), domain.NotificationJob{AlertID: "a1"}); err == nil {
		t.Fatal("expected an error without a routing key")
	}
}
