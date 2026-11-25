package notify

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/mailer"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

type sentMail struct{ to, subject, body string }

type fakeMailer struct {
	mu   sync.Mutex
	sent []sentMail
	err  error
}

func (m *fakeMailer) Send(to, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, sentMail{to, subject, body})
	return nil
}

func (m *fakeMailer) all() []sentMail {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]sentMail(nil), m.sent...)
}

func (m *fakeMailer) setErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

type alertsFixture struct {
	store *sqlite.SQLiteStore
	org   *domain.Org
	ch    *domain.NotificationChannel
	mail  *fakeMailer
}

func newAlertsFixture(t *testing.T) *alertsFixture {
	t.Helper()
	ctx := context.Background()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	now := time.Now().UTC().Truncate(time.Second)
	org := &domain.Org{ID: uuid.NewString(), Name: "Acme Ops", Slug: "acme-" + uuid.NewString()[:8], Plan: domain.PlanFree, CreatedAt: now, UpdatedAt: now}
	if err := s.Orgs().Create(ctx, org); err != nil {
		t.Fatal(err)
	}
	users := []struct {
		email    string
		role     domain.UserRole
		disabled bool
	}{
		{"owner1@example.com", domain.RoleOwner, false},
		{"owner2@example.com", domain.RoleOwner, false},
		{"gone@example.com", domain.RoleOwner, true},
		{"admin@example.com", domain.RoleAdmin, false},
		{"member@example.com", domain.RoleMember, false},
	}
	for _, u := range users {
		user := &domain.User{ID: uuid.NewString(), OrgID: org.ID, Email: u.email, Role: u.role, CreatedAt: now, UpdatedAt: now}
		if err := s.Users().Create(ctx, user); err != nil {
			t.Fatal(err)
		}
		if u.disabled {
			if err := s.Users().Disable(ctx, user.ID, now); err != nil {
				t.Fatal(err)
			}
		}
	}
	ch := &domain.NotificationChannel{ID: uuid.NewString(), OrgID: org.ID, Type: "slack", Name: "#on-call", Config: "{}", Enabled: true}
	if err := s.NotificationChannels().Create(ctx, ch); err != nil {
		t.Fatal(err)
	}
	return &alertsFixture{store: s, org: org, ch: ch, mail: &fakeMailer{}}
}

// recorder returns a Recorder with channel alerts that run the recovery
// email inline, standing in for one notifier worker.
func (f *alertsFixture) recorder(t *testing.T) *Recorder {
	t.Helper()
	a := NewChannelAlerts(f.store, f.mail, "https://app.example.com/")
	if a == nil {
		t.Fatal("NewChannelAlerts returned nil for a real mailer")
	}
	a.goAsync = func(fn func()) { fn() }
	return NewRecorder(f.store, WithChannelAlerts(a))
}

func (f *alertsFixture) deadLetter() DeadLetter {
	return DeadLetter{
		JobID: uuid.NewString(), OrgID: f.org.ID, ChannelID: f.ch.ID, ChannelName: f.ch.Name,
		ChannelType: f.ch.Type, Reason: "Delivery failed: the destination returned HTTP 404.",
		FailedAt: time.Date(2026, 11, 24, 9, 30, 0, 0, time.UTC),
	}
}

func (f *alertsFixture) fail(t *testing.T, rec *Recorder, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := rec.RecordFailed(context.Background(), f.deadLetter()); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *alertsFixture) succeed(t *testing.T, rec *Recorder) {
	t.Helper()
	job := domain.NotificationJob{ID: uuid.NewString(), OrgID: f.org.ID, Channel: f.ch.Type, ChannelID: f.ch.ID, ChannelName: f.ch.Name}
	if err := rec.RecordSent(context.Background(), job); err != nil {
		t.Fatal(err)
	}
}

func (f *alertsFixture) channel(t *testing.T) *domain.NotificationChannel {
	t.Helper()
	ch, err := f.store.NotificationChannels().GetByID(context.Background(), f.ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

func recipients(mails []sentMail) []string {
	out := make([]string, 0, len(mails))
	for _, m := range mails {
		out = append(out, m.to)
	}
	return out
}

func assertOwnersOnly(t *testing.T, mails []sentMail) {
	t.Helper()
	got := strings.Join(recipients(mails), ",")
	if got != "owner1@example.com,owner2@example.com" && got != "owner2@example.com,owner1@example.com" {
		t.Fatalf("recipients = %s, want the two active owners once each", got)
	}
}

func TestChannelAlerts_EmailsOwnersOnceAtThreshold(t *testing.T) {
	f := newAlertsFixture(t)
	rec := f.recorder(t)

	f.fail(t, rec, 2)
	if n := len(f.mail.all()); n != 0 {
		t.Fatalf("%d emails below the default threshold of 3", n)
	}
	f.fail(t, rec, 1)
	mails := f.mail.all()
	assertOwnersOnly(t, mails)

	m := mails[0]
	if !strings.Contains(m.subject, `"#on-call" is failing`) {
		t.Errorf("subject = %q", m.subject)
	}
	for _, want := range []string{
		`"#on-call" (slack)`,
		"Acme Ops",
		"failed its last 3 deliveries",
		"Last error: Delivery failed: the destination returned HTTP 404.",
		"Last failure: 2026-11-24 09:30:00 UTC",
		"https://app.example.com/settings/channels?channel=" + f.ch.ID,
	} {
		if !strings.Contains(m.body, want) {
			t.Errorf("body missing %q:\n%s", want, m.body)
		}
	}
	if f.channel(t).FailureNotifiedAt == nil {
		t.Fatal("failure_notified_at not set after the email")
	}

	// The rest of the streak stays quiet.
	f.fail(t, rec, 5)
	if n := len(f.mail.all()); n != 2 {
		t.Fatalf("%d emails after more failures in the same streak, want 2", n)
	}

	// A redelivered dead letter does not count or email again.
	dl := f.deadLetter()
	for i := 0; i < 2; i++ {
		if _, err := rec.RecordFailed(context.Background(), dl); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(f.mail.all()); n != 2 {
		t.Fatalf("%d emails after a redelivered dead letter, want 2", n)
	}
}

// Two notifier workers record the failures that cross the threshold at
// the same time. The owners get one email each.
func TestChannelAlerts_ConcurrentWorkersSendOnce(t *testing.T) {
	for run := 0; run < 20; run++ {
		f := newAlertsFixture(t)
		workerA, workerB := f.recorder(t), f.recorder(t)
		f.fail(t, workerA, 2)

		var wg sync.WaitGroup
		for _, rec := range []*Recorder{workerA, workerB} {
			wg.Add(1)
			go func(rec *Recorder) {
				defer wg.Done()
				if _, err := rec.RecordFailed(context.Background(), f.deadLetter()); err != nil {
					t.Error(err)
				}
			}(rec)
		}
		wg.Wait()
		if got := f.channel(t).ConsecutiveFailures; got != 4 {
			t.Fatalf("run %d: consecutive failures = %d, want 4", run, got)
		}
		assertOwnersOnly(t, f.mail.all())
	}
}

func TestChannelAlerts_SuccessResetsStreakAndSendsRecovery(t *testing.T) {
	f := newAlertsFixture(t)
	rec := f.recorder(t)

	// A success before any email ends the streak quietly.
	f.fail(t, rec, 2)
	f.succeed(t, rec)
	f.fail(t, rec, 2)
	if n := len(f.mail.all()); n != 0 {
		t.Fatalf("%d emails, but no streak reached 3", n)
	}

	f.fail(t, rec, 1)
	if n := len(f.mail.all()); n != 2 {
		t.Fatalf("%d emails at the threshold, want 2", n)
	}

	f.succeed(t, rec)
	mails := f.mail.all()
	if len(mails) != 4 {
		t.Fatalf("%d emails after recovery, want 2 failing + 2 recovered", len(mails))
	}
	assertOwnersOnly(t, mails[2:])
	if !strings.Contains(mails[2].subject, "is delivering again") {
		t.Errorf("recovery subject = %q", mails[2].subject)
	}
	ch := f.channel(t)
	if ch.FailureNotifiedAt != nil || ch.ConsecutiveFailures != 0 {
		t.Fatalf("after success: notified=%v failures=%d", ch.FailureNotifiedAt, ch.ConsecutiveFailures)
	}

	// Further successes do not repeat the recovery note.
	f.succeed(t, rec)
	if n := len(f.mail.all()); n != 4 {
		t.Fatalf("%d emails after a second success, want 4", n)
	}

	// A new streak emails again.
	f.fail(t, rec, 3)
	if n := len(f.mail.all()); n != 6 {
		t.Fatalf("%d emails after a second streak, want 6", n)
	}
}

func TestChannelAlerts_UsesOrgThreshold(t *testing.T) {
	f := newAlertsFixture(t)
	rec := f.recorder(t)
	if err := f.store.OrgSettings().Set(context.Background(), f.org.ID, domain.SettingChannelFailureAlertThreshold, "1"); err != nil {
		t.Fatal(err)
	}
	f.fail(t, rec, 1)
	assertOwnersOnly(t, f.mail.all())

	g := newAlertsFixture(t)
	rec = g.recorder(t)
	if err := g.store.OrgSettings().Set(context.Background(), g.org.ID, domain.SettingChannelFailureAlertThreshold, "5"); err != nil {
		t.Fatal(err)
	}
	g.fail(t, rec, 4)
	if n := len(g.mail.all()); n != 0 {
		t.Fatalf("%d emails below a threshold of 5", n)
	}
	g.fail(t, rec, 1)
	assertOwnersOnly(t, g.mail.all())
}

// When no owner could be emailed, the claim is given back so the next
// failure in the streak tries again.
func TestChannelAlerts_SendFailureReleasesClaim(t *testing.T) {
	f := newAlertsFixture(t)
	rec := f.recorder(t)
	f.mail.setErr(errors.New("smtp down"))
	f.fail(t, rec, 3)
	if ch := f.channel(t); ch.FailureNotifiedAt != nil {
		t.Fatal("claim kept although no email was sent")
	}
	f.mail.setErr(nil)
	f.fail(t, rec, 1)
	assertOwnersOnly(t, f.mail.all())
}

func TestChannelAlerts_NilMailerIsNoop(t *testing.T) {
	f := newAlertsFixture(t)
	var typedNil *mailer.Mailer // what mailer.New returns without SMTP config
	if a := NewChannelAlerts(f.store, typedNil, "https://app.example.com"); a != nil {
		t.Fatal("typed nil mailer gave a non-nil ChannelAlerts")
	}
	if a := NewChannelAlerts(f.store, nil, ""); a != nil {
		t.Fatal("nil mailer gave a non-nil ChannelAlerts")
	}
	rec := NewRecorder(f.store, WithChannelAlerts(NewChannelAlerts(f.store, typedNil, "")))
	f.fail(t, rec, 5)
	f.succeed(t, rec)
	if ch := f.channel(t); ch.FailureNotifiedAt != nil {
		t.Fatal("a recorder without a mailer claimed the failure notice")
	}
}

// Without a public base URL the email still tells owners where to look.
func TestChannelAlerts_NoBaseURL(t *testing.T) {
	f := newAlertsFixture(t)
	a := NewChannelAlerts(f.store, f.mail, "")
	rec := NewRecorder(f.store, WithChannelAlerts(a))
	f.fail(t, rec, 3)
	mails := f.mail.all()
	if len(mails) == 0 || !strings.Contains(mails[0].body, "Open Settings, then Channels") {
		t.Fatalf("emails = %+v", mails)
	}
}

var _ store.Store = (*sqlite.SQLiteStore)(nil)
