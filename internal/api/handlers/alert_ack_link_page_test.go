package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// The confirm page names the alert in plain words and links to it.
func TestAckLinkPageShowsReadableDetailsAndAlertLink(t *testing.T) {
	f := setupAckLink(t, domain.AlertFiring)
	_, _, body := ackRequest(t, http.MethodGet, f.baseURL+"/ack/"+f.token(t))
	for _, want := range []string{
		"<dd>Critical</dd>",
		"<dd>Firing</dd>",
		" UTC</dd>",
		`href="/alerts/` + f.alert.ID + `"`,
		"Open this alert in YipYap",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q:\n%s", want, body)
		}
	}
}

// After a successful ack the page must not still say the alert is firing.
func TestAckLinkPostShowsAcknowledgedStatus(t *testing.T) {
	f := setupAckLink(t, domain.AlertFiring)
	code, _, body := ackRequest(t, http.MethodPost, f.baseURL+"/ack/"+f.token(t))
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if !strings.Contains(body, "<dd>Acknowledged</dd>") || strings.Contains(body, "<dd>Firing</dd>") {
		t.Fatalf("status row wrong:\n%s", body)
	}
	if strings.Contains(body, "<form") {
		t.Error("success page must not offer the form again")
	}
}

// A token that does not name a usable alert shows no alert details and
// links to the app root only.
func TestAckLinkErrorPagesRevealNothing(t *testing.T) {
	f := setupAckLink(t, domain.AlertFiring)
	expired, _ := ackTestIssuer.IssueAlertAck(f.alert.ID, f.alert.OrgID, f.channel.ID, f.user.ID, -time.Minute)
	for name, tok := range map[string]string{"expired": expired, "garbage": "not-a-token"} {
		_, _, body := ackRequest(t, http.MethodGet, f.baseURL+"/ack/"+tok)
		if strings.Contains(body, "Test Monitor") || strings.Contains(body, "/alerts/") || strings.Contains(body, "<dl>") {
			t.Errorf("%s: page leaks alert details:\n%s", name, body)
		}
		if !strings.Contains(body, `href="/"`) || !strings.Contains(body, "Open YipYap") {
			t.Errorf("%s: missing app link:\n%s", name, body)
		}
	}
}

type failingAlertBus struct{ bus.Bus }

func (failingAlertBus) PublishAlert(context.Context, string, []byte, string) error {
	return errors.New("bus down")
}

// When the ack cannot be published the page says the alert is still
// firing and offers a retry button that posts again.
func TestAckLinkPublishFailureOffersRetry(t *testing.T) {
	f := setupAckLink(t, domain.AlertFiring)
	h := handlers.NewAckLinkHandler(f.store, ackTestIssuer, failingAlertBus{f.bus}, "https://app.example.com/")
	r := chi.NewRouter()
	r.Post("/ack/{token}", h.Confirm)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/ack/"+f.token(t), strings.NewReader("")))
	body := rec.Body.String()
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(body, `<form method="post"><button type="submit">Try again</button></form>`) {
		t.Errorf("no retry form:\n%s", body)
	}
	if !strings.Contains(body, "still firing") {
		t.Errorf("page does not say the alert is still firing:\n%s", body)
	}
	if !strings.Contains(body, `href="https://app.example.com/alerts/`+f.alert.ID+`"`) {
		t.Errorf("alert link not absolute:\n%s", body)
	}
}

// The alert detail API names who acknowledged, so the SPA can show it
// next to the status (a link ack from a person's own channel included).
func TestAlertGetIncludesAcknowledgerName(t *testing.T) {
	f := setupAckLink(t, domain.AlertFiring)
	ctx := context.Background()
	now := time.Now().UTC()
	f.alert.Status = domain.AlertAcknowledged
	f.alert.AcknowledgedBy = f.user.ID
	f.alert.AcknowledgedAt = &now
	if err := f.store.Alerts().Update(ctx, f.alert); err != nil {
		t.Fatal(err)
	}
	session := sessionFor(t, f.store, ackTestIssuer, f.user)
	req, _ := http.NewRequest(http.MethodGet, f.baseURL+"/api/v1/alerts/"+f.alert.ID, nil)
	req.Header.Set("Authorization", "Bearer "+session)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got struct {
		AcknowledgedBy     string `json:"acknowledged_by"`
		AcknowledgedByName string `json:"acknowledged_by_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("status %d: %v", resp.StatusCode, err)
	}
	if got.AcknowledgedBy != f.user.ID || got.AcknowledgedByName != f.user.Email {
		t.Fatalf("got %+v, want name %q", got, f.user.Email)
	}
}
