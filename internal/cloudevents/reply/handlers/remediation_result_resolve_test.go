package handlers

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/contactmethod"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/escalation"
)

type resolveCapture struct {
	mu   sync.Mutex
	jobs []domain.NotificationJob
}

func (c *resolveCapture) handle(_ context.Context, _ string, data []byte) error {
	var job domain.NotificationJob
	if err := json.Unmarshal(data, &job); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.jobs = append(c.jobs, job)
	return nil
}

func (c *resolveCapture) snapshot() []domain.NotificationJob {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]domain.NotificationJob(nil), c.jobs...)
}

func TestRemediationResultAutoResolveSendsOneResolvePerPagedTarget(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	seedRemediation(t, s, alert.ID, "rem-resolve")
	if err := s.Checks().Create(context.Background(), &domain.MonitorCheck{
		ID:        uuid.New().String(),
		MonitorID: alert.MonitorID,
		Status:    domain.StatusUp,
		CheckedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	methodIDs := map[string]bool{}
	for _, addr := range []string{"one@example.com", "two@example.com"} {
		u := seedUser(t, s, alert.OrgID)
		m := &domain.ContactMethod{
			ID: uuid.New().String(), OrgID: alert.OrgID, UserID: u.ID,
			Type: domain.ContactEmail, Label: "Email",
			Config: `{"address":"` + addr + `"}`, VerifiedAt: &now, CreatedAt: now, UpdatedAt: now,
		}
		if err := s.ContactMethods().Create(context.Background(), m); err != nil {
			t.Fatal(err)
		}
		if err := s.Alerts().RecordPagedTarget(context.Background(), &domain.AlertPagedTarget{
			AlertID: alert.ID, TargetKey: "method:" + m.ID, Kind: domain.PagedTargetMethod,
			UserID: u.ID, MethodID: m.ID, CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		methodIDs[m.ID] = true
	}

	capture := &resolveCapture{}
	b := bus.NewChannel()
	if err := b.Subscribe("notify.request", capture.handle); err != nil {
		t.Fatal(err)
	}
	h := NewRemediationResultHandler(s, escalation.NewResolveFanout(s, b, contactmethod.Cipher{}))

	ev := newReplyEvent(t, types.TypeReplyAlertRemediationResultV1, types.ReplyAlertRemediationResultV1Data{
		AlertID:       alert.ID,
		RemediationID: "rem-resolve",
		Outcome:       "success",
		Message:       "fixed",
	})
	entry := &reply.Entry{AlertID: alert.ID, OrgID: alert.OrgID}

	if err := h.Handle(context.Background(), ev, entry, ""); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var jobs []domain.NotificationJob
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if jobs = capture.snapshot(); len(jobs) == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(jobs) != 2 {
		t.Fatalf("resolve jobs = %d, want 2", len(jobs))
	}
	for _, job := range jobs {
		if job.EventAction != domain.EventActionResolve {
			t.Errorf("action = %q, want resolve", job.EventAction)
		}
		if job.Channel != "smtp" || !methodIDs[job.ContactMethodID] {
			t.Errorf("job = %s/%s", job.Channel, job.ContactMethodID)
		}
	}

	if err := h.Handle(context.Background(), ev, entry, ""); err == nil {
		t.Fatal("expected the replayed reply to be rejected")
	}
	time.Sleep(50 * time.Millisecond)
	if got := capture.snapshot(); len(got) != 2 {
		t.Fatalf("replay sent %d extra jobs", len(got)-2)
	}

	left, err := s.Alerts().ListPagedTargets(context.Background(), alert.ID)
	if err != nil || len(left) != 0 {
		t.Fatalf("paged targets left = %d, %v", len(left), err)
	}
}
