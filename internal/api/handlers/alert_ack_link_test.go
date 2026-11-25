package handlers_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/escalation"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// The events test server signs sessions with this secret.
var ackTestIssuer = auth.NewJWTIssuer([]byte("test-secret-key"), time.Hour)

type ackFixture struct {
	baseURL string
	store   *sqlite.SQLiteStore
	bus     bus.Bus
	alert   *domain.Alert
	user    *domain.User
	channel *domain.NotificationChannel
	acks    chan domain.AckEvent
}

func setupAckLink(t *testing.T, status domain.AlertStatus) *ackFixture {
	t.Helper()
	ts, s, b := setupEventsTestServer(t)
	createMonitorWithIntegrationKey(t, s, "ik-ack-link")
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	user := &domain.User{ID: "user-ack-1", OrgID: "org-test-1", Email: "oncall@example.com",
		PasswordHash: "x", Role: domain.RoleMember, CreatedAt: now, UpdatedAt: now}
	if err := s.Users().Create(ctx, user); err != nil {
		t.Fatal(err)
	}
	ch := &domain.NotificationChannel{ID: "chan-ack-1", OrgID: "org-test-1", Type: "smtp",
		Name: "Pager email", Config: `{}`, Enabled: true}
	if err := s.NotificationChannels().Create(ctx, ch); err != nil {
		t.Fatal(err)
	}
	alert := &domain.Alert{ID: "alert-ack-link-1", MonitorID: "mon-test-1", OrgID: "org-test-1",
		Status: status, Severity: domain.SeverityCritical, StartedAt: now}
	if status == domain.AlertResolved {
		alert.ResolvedAt = &now
	}
	if err := s.Alerts().Create(ctx, alert); err != nil {
		t.Fatal(err)
	}
	acks := make(chan domain.AckEvent, 4)
	_ = b.Subscribe("alert.ack", func(_ context.Context, _ string, data []byte) error {
		var evt domain.AckEvent
		_ = json.Unmarshal(data, &evt)
		acks <- evt
		return nil
	})
	return &ackFixture{baseURL: ts.URL, store: s, bus: b, alert: alert, user: user, channel: ch, acks: acks}
}

func (f *ackFixture) token(t *testing.T) string {
	t.Helper()
	tok, err := ackTestIssuer.IssueAlertAck(f.alert.ID, f.alert.OrgID, f.channel.ID, f.user.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func ackRequest(t *testing.T, method, u string) (int, http.Header, string) {
	t.Helper()
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader(url.Values{}.Encode())
	}
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		t.Fatal(err)
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(b)
}

func (f *ackFixture) expectNoAck(t *testing.T) {
	t.Helper()
	select {
	case evt := <-f.acks:
		t.Fatalf("unexpected alert.ack: %+v", evt)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestAckLinkGetShowsConfirmAndDoesNotAck(t *testing.T) {
	f := setupAckLink(t, domain.AlertFiring)
	code, hdr, body := ackRequest(t, http.MethodGet, f.baseURL+"/ack/"+f.token(t))
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, body)
	}
	if !strings.Contains(body, `<form method="post">`) || !strings.Contains(body, "Acknowledge alert</button>") {
		t.Fatalf("confirm form missing: %s", body)
	}
	if !strings.Contains(body, "Test Monitor") {
		t.Error("page does not name the monitor")
	}
	if strings.Contains(body, "<script") {
		t.Error("page must not contain script (CSP script-src 'self')")
	}
	if hdr.Get("Cache-Control") != "no-store" || hdr.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("headers = %v", hdr)
	}
	if !strings.Contains(hdr.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Error("strict CSP missing")
	}
	f.expectNoAck(t)
	a, _ := f.store.Alerts().GetByID(context.Background(), f.alert.ID)
	if a.Status != domain.AlertFiring {
		t.Fatalf("GET changed status to %s", a.Status)
	}
}

func TestAckLinkPostAcks(t *testing.T) {
	f := setupAckLink(t, domain.AlertFiring)
	code, _, body := ackRequest(t, http.MethodPost, f.baseURL+"/ack/"+f.token(t))
	if code != http.StatusOK || !strings.Contains(body, "Alert acknowledged") {
		t.Fatalf("status = %d: %s", code, body)
	}
	select {
	case evt := <-f.acks:
		want := domain.AckEvent{AlertID: f.alert.ID, UserID: f.user.ID, Channel: "link",
			Via: domain.AckViaLink, ChannelID: f.channel.ID, ChannelName: f.channel.Name}
		if evt != want {
			t.Fatalf("ack = %+v, want %+v", evt, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no alert.ack published")
	}
}

func TestAckLinkExpired(t *testing.T) {
	f := setupAckLink(t, domain.AlertFiring)
	tok, _ := ackTestIssuer.IssueAlertAck(f.alert.ID, f.alert.OrgID, f.channel.ID, f.user.ID, -time.Minute)
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		code, _, body := ackRequest(t, m, f.baseURL+"/ack/"+tok)
		if code != http.StatusGone || !strings.Contains(body, "expired") {
			t.Fatalf("%s status = %d: %s", m, code, body)
		}
	}
	f.expectNoAck(t)
}

func TestAckLinkWrongAudience(t *testing.T) {
	f := setupAckLink(t, domain.AlertFiring)
	session := sessionFor(t, f.store, ackTestIssuer, f.user)
	survey, _ := ackTestIssuer.IssueSurvey(f.alert.ID, f.user.ID, f.alert.OrgID, time.Hour)
	other := auth.NewJWTIssuer([]byte("other"), time.Hour)
	forged, _ := other.IssueAlertAck(f.alert.ID, f.alert.OrgID, f.channel.ID, f.user.ID, 0)
	for name, tok := range map[string]string{"session": session, "survey": survey, "other-secret": forged, "garbage": "not-a-token"} {
		code, _, _ := ackRequest(t, http.MethodPost, f.baseURL+"/ack/"+tok)
		if code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", name, code)
		}
	}
	f.expectNoAck(t)
}

func TestAckLinkResolvedAlert(t *testing.T) {
	f := setupAckLink(t, domain.AlertResolved)
	tok := f.token(t)
	code, _, body := ackRequest(t, http.MethodGet, f.baseURL+"/ack/"+tok)
	if code != http.StatusOK || !strings.Contains(body, "already resolved") || strings.Contains(body, "<form") {
		t.Fatalf("GET status = %d: %s", code, body)
	}
	code, _, _ = ackRequest(t, http.MethodPost, f.baseURL+"/ack/"+tok)
	if code != http.StatusConflict {
		t.Fatalf("POST status = %d, want 409", code)
	}
	f.expectNoAck(t)
}

func TestAckLinkAlreadyAcknowledged(t *testing.T) {
	f := setupAckLink(t, domain.AlertAcknowledged)
	code, _, body := ackRequest(t, http.MethodPost, f.baseURL+"/ack/"+f.token(t))
	if code != http.StatusOK || !strings.Contains(body, "already acknowledged") {
		t.Fatalf("status = %d: %s", code, body)
	}
	f.expectNoAck(t)
}

func TestAckLinkOtherOrgAlert(t *testing.T) {
	f := setupAckLink(t, domain.AlertFiring)
	tok, _ := ackTestIssuer.IssueAlertAck(f.alert.ID, "org-someone-else", "", "", 0)
	code, _, _ := ackRequest(t, http.MethodPost, f.baseURL+"/ack/"+tok)
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
	f.expectNoAck(t)
}

func TestAckLinkRevokedForDisabledUserOrDeletedChannel(t *testing.T) {
	f := setupAckLink(t, domain.AlertFiring)
	ctx := context.Background()
	tok := f.token(t)

	if err := f.store.Users().Disable(ctx, f.user.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := ackRequest(t, http.MethodPost, f.baseURL+"/ack/"+tok); code != http.StatusForbidden {
		t.Fatalf("disabled user: status = %d, want 403", code)
	}

	chTok, _ := ackTestIssuer.IssueAlertAck(f.alert.ID, f.alert.OrgID, f.channel.ID, "", 0)
	if err := f.store.NotificationChannels().Delete(ctx, f.channel.ID); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := ackRequest(t, http.MethodPost, f.baseURL+"/ack/"+chTok); code != http.StatusForbidden {
		t.Fatalf("deleted channel: status = %d, want 403", code)
	}
	f.expectNoAck(t)
}

// End to end: the POST goes through the escalation engine, which stores
// the ack and writes an "acknowledged via link" timeline entry.
func TestAckLinkThroughEngine(t *testing.T) {
	f := setupAckLink(t, domain.AlertFiring)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := escalation.NewEngine(f.store, f.bus, nil, nil)
	if err := engine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer engine.Stop()

	if code, _, body := ackRequest(t, http.MethodPost, f.baseURL+"/ack/"+f.token(t)); code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a, _ := f.store.Alerts().GetByID(ctx, f.alert.ID)
		if a != nil && a.Status == domain.AlertAcknowledged {
			if a.AcknowledgedBy != f.user.ID {
				t.Fatalf("acknowledged_by = %q", a.AcknowledgedBy)
			}
			events, _ := f.store.Alerts().ListEvents(ctx, f.alert.ID)
			for _, e := range events {
				if e.EventType == domain.EventAck && strings.Contains(string(e.Detail), `"via":"link"`) {
					return
				}
			}
			t.Fatalf("no ack event with via=link: %+v", events)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("alert was not acknowledged")
}
