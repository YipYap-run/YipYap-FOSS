package handlers_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/contactmethod"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/escalation"
)

func waitCapture(t *testing.T, c *jobCapture, n int) []domain.NotificationJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := c.snapshot(); len(got) >= n {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d jobs", n)
	return nil
}

func TestManualResolveSendsOneResolvePerPagedTarget(t *testing.T) {
	s, alert, user := setupAlertHandlerTest(t)
	m := recordEmailPagedTarget(t, s, alert, user.ID)

	capture := &jobCapture{}
	b := bus.NewChannel()
	if err := b.Subscribe("notify.request", capture.handler); err != nil {
		t.Fatal(err)
	}
	h := handlers.NewAlertHandler(s)
	h.SetBus(b)

	if w := doAlertAction(t, h, "resolve", alert, user.ID); w.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", w.Code, w.Body.String())
	}
	jobs := waitCapture(t, capture, 1)
	if len(jobs) != 1 {
		t.Fatalf("resolve jobs = %d, want 1", len(jobs))
	}
	job := jobs[0]
	if job.EventAction != domain.EventActionResolve {
		t.Errorf("action = %q, want resolve", job.EventAction)
	}
	if job.Channel != "smtp" || job.ContactMethodID != m.ID {
		t.Errorf("job = %s/%s, want smtp/%s", job.Channel, job.ContactMethodID, m.ID)
	}
	if !strings.HasPrefix(job.DedupeKey, "resolve-"+alert.ID+"-") {
		t.Errorf("dedupe key = %q", job.DedupeKey)
	}

	if w := doAlertAction(t, h, "resolve", alert, user.ID); w.Code != http.StatusConflict {
		t.Fatalf("second resolve: %d %s", w.Code, w.Body.String())
	}
	time.Sleep(50 * time.Millisecond)
	if got := capture.snapshot(); len(got) != 1 {
		t.Fatalf("resolving twice sent %d jobs", len(got))
	}
}

func TestManualResolveAfterAutoResolveSendsNothing(t *testing.T) {
	s, alert, user := setupAlertHandlerTest(t)
	recordEmailPagedTarget(t, s, alert, user.ID)

	capture := &jobCapture{}
	b := bus.NewChannel()
	if err := b.Subscribe("notify.request", capture.handler); err != nil {
		t.Fatal(err)
	}

	escalation.SendResolves(context.Background(), s, b, contactmethod.Cipher{}, alert, time.Now().UTC())
	if got := waitCapture(t, capture, 1); len(got) != 1 || got[0].EventAction != domain.EventActionResolve {
		t.Fatalf("auto resolve jobs = %+v", got)
	}

	now := time.Now().UTC()
	alert.Status = domain.AlertResolved
	alert.ResolvedAt = &now
	if err := s.Alerts().Update(context.Background(), alert); err != nil {
		t.Fatal(err)
	}
	capture.reset()

	h := handlers.NewAlertHandler(s)
	h.SetBus(b)
	if w := doAlertAction(t, h, "resolve", alert, user.ID); w.Code != http.StatusConflict {
		t.Fatalf("manual resolve after auto: %d %s", w.Code, w.Body.String())
	}
	time.Sleep(50 * time.Millisecond)
	if got := capture.snapshot(); len(got) != 0 {
		t.Fatalf("manual resolve after auto sent %d jobs", len(got))
	}
}
