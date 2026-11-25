package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// secretToken stands in for a bot token that a provider puts in its API
// URL. Go's HTTP client includes the URL in transport errors.
const secretToken = "123456:AAE-very-secret-bot-token"

// switchNotifier fails with a transport error carrying a token URL while
// fail is set, and succeeds otherwise.
type switchNotifier struct {
	fail  atomic.Bool
	calls atomic.Int32
}

func (n *switchNotifier) Channel() string     { return "telegram" }
func (n *switchNotifier) MaxConcurrency() int { return 1 }
func (n *switchNotifier) Send(_ context.Context, _ domain.NotificationJob) (string, error) {
	n.calls.Add(1)
	if n.fail.Load() {
		return "", &url.Error{
			Op:  "Post",
			URL: "https://api.telegram.org/bot" + secretToken + "/sendMessage",
			Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")},
		}
	}
	return "ok", nil
}

type outcomeFixture struct {
	store   store.Store
	bus     bus.Bus
	ch      *domain.NotificationChannel
	alert   *domain.Alert
	metrics *countMetrics
	prov    *switchNotifier
	disp    *Dispatcher
}

func newOutcomeFixture(t *testing.T) *outcomeFixture {
	t.Helper()
	return newOutcomeFixtureOn(t, bus.NewChannel())
}

func newOutcomeFixtureOn(t *testing.T, b bus.Bus) *outcomeFixture {
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
	org := &domain.Org{ID: uuid.NewString(), Name: "o", Slug: "o-" + uuid.NewString()[:8], Plan: domain.PlanFree, CreatedAt: now, UpdatedAt: now}
	if err := s.Orgs().Create(ctx, org); err != nil {
		t.Fatal(err)
	}
	mon := &domain.Monitor{ID: uuid.NewString(), OrgID: org.ID, Name: "api", Type: domain.MonitorHTTP, Config: json.RawMessage(`{}`), IntervalSeconds: 60, TimeoutSeconds: 10, Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := s.Monitors().Create(ctx, mon); err != nil {
		t.Fatal(err)
	}
	alert := &domain.Alert{ID: uuid.NewString(), MonitorID: mon.ID, OrgID: org.ID, Status: domain.AlertFiring, Severity: domain.SeverityCritical, StartedAt: now}
	if err := s.Alerts().Create(ctx, alert); err != nil {
		t.Fatal(err)
	}
	ch := &domain.NotificationChannel{ID: uuid.NewString(), OrgID: org.ID, Type: "telegram", Name: "on-call chat", Config: "{}", Enabled: true}
	if err := s.NotificationChannels().Create(ctx, ch); err != nil {
		t.Fatal(err)
	}

	m := &countMetrics{}
	rec := NewRecorder(s)
	d := NewDispatcher(b, nil, WithMetrics(m), WithRecorder(rec))
	prov := &switchNotifier{}
	d.Register(prov, 8)
	for _, p := range d.Pools() {
		p.SetRetryParams(2, time.Millisecond, time.Millisecond)
	}
	runCtx, cancel := context.WithCancel(ctx)
	if err := d.Start(runCtx); err != nil {
		t.Fatal(err)
	}
	if err := StartDeadLetterConsumer(b, rec); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = d.Stop(context.Background())
		cancel()
	})
	return &outcomeFixture{store: s, bus: b, ch: ch, alert: alert, metrics: m, prov: prov, disp: d}
}

func (f *outcomeFixture) job() domain.NotificationJob {
	id := uuid.NewString()
	return domain.NotificationJob{
		ID: id, AlertID: f.alert.ID, OrgID: f.alert.OrgID, Channel: "telegram",
		ChannelID: f.ch.ID, ChannelName: f.ch.Name, DedupeKey: id,
	}
}

func (f *outcomeFixture) events(t *testing.T, typ domain.AlertEventType) []*domain.AlertEvent {
	t.Helper()
	all, err := f.store.Alerts().ListEvents(context.Background(), f.alert.ID)
	if err != nil {
		t.Fatal(err)
	}
	var out []*domain.AlertEvent
	for _, e := range all {
		if e.EventType == typ {
			out = append(out, e)
		}
	}
	return out
}

func (f *outcomeFixture) channel(t *testing.T) *domain.NotificationChannel {
	t.Helper()
	ch, err := f.store.NotificationChannels().GetByID(context.Background(), f.ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

// A provider error ends up as a notification_failed event on the alert
// timeline and a failing channel, through the dead letter path. The sent
// metric is not touched, and the token in the provider error never reaches
// the database.
func TestOutcome_ProviderErrorRecordsFailure(t *testing.T) {
	f := newOutcomeFixture(t)
	f.prov.fail.Store(true)

	for i := 1; i <= 2; i++ {
		if err := f.disp.Dispatch(f.job()); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "notification_failed event", func() bool { return len(f.events(t, domain.EventNotificationFailed)) == i })
		waitFor(t, "consecutive failure count", func() bool { return f.channel(t).ConsecutiveFailures == i })
	}

	if got := f.metrics.sent.Load(); got != 0 {
		t.Fatalf("sent metric = %d after failures, want 0", got)
	}
	if got := f.metrics.fail.Load(); got != 2 {
		t.Fatalf("fail metric = %d, want 2", got)
	}
	if len(f.events(t, domain.EventNotificationSent)) != 0 {
		t.Fatal("a failed job produced notification_sent")
	}

	ch := f.channel(t)
	h := ch.Health()
	if h.Status != domain.ChannelHealthFailing || h.LastFailureAt == nil {
		t.Fatalf("health = %+v, want failing", h)
	}
	if !strings.Contains(h.LastError, "could not connect") {
		t.Fatalf("last_error = %q, want the sanitized connect failure", h.LastError)
	}

	evt := f.events(t, domain.EventNotificationFailed)[0]
	var detail map[string]string
	if err := json.Unmarshal(evt.Detail, &detail); err != nil {
		t.Fatal(err)
	}
	if detail["channel_id"] != f.ch.ID || detail["channel_name"] != f.ch.Name || detail["reason"] == "" || detail["job_id"] == "" {
		t.Fatalf("failed event detail = %v", detail)
	}
	if evt.Channel != "telegram" {
		t.Fatalf("failed event channel = %q", evt.Channel)
	}

	for _, leak := range []string{secretToken, "AAE-very-secret", "api.telegram.org"} {
		if strings.Contains(ch.LastError, leak) || strings.Contains(string(evt.Detail), leak) {
			t.Fatalf("%q leaked into last_error %q or detail %s", leak, ch.LastError, evt.Detail)
		}
	}
}

// A successful send writes notification_sent and resets the failure count.
func TestOutcome_SuccessResetsFailures(t *testing.T) {
	f := newOutcomeFixture(t)
	f.prov.fail.Store(true)
	if err := f.disp.Dispatch(f.job()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "failure recorded", func() bool { return f.channel(t).ConsecutiveFailures == 1 })

	f.prov.fail.Store(false)
	job := f.job()
	if err := f.disp.Dispatch(job); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "notification_sent event", func() bool { return len(f.events(t, domain.EventNotificationSent)) == 1 })

	ch := f.channel(t)
	if ch.ConsecutiveFailures != 0 || ch.LastSuccessAt == nil || ch.Health().Status != domain.ChannelHealthOK {
		t.Fatalf("channel after success = %+v", ch.Health())
	}
	if got := f.metrics.sent.Load(); got != 1 {
		t.Fatalf("sent metric = %d, want 1", got)
	}
	var detail map[string]string
	_ = json.Unmarshal(f.events(t, domain.EventNotificationSent)[0].Detail, &detail)
	if detail["job_id"] != job.ID || detail["channel_id"] != f.ch.ID {
		t.Fatalf("sent event detail = %v", detail)
	}
}

// Redelivered outcomes (JetStream at-least-once) do not add timeline rows
// or count a failure twice.
func TestRecorder_IdempotentPerJob(t *testing.T) {
	f := newOutcomeFixture(t)
	rec := NewRecorder(f.store)
	ctx := context.Background()
	job := f.job()
	dl := NewDeadLetter(job, errors.New("boom"), time.Now())
	for range 3 {
		if _, err := rec.RecordFailed(ctx, dl); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(f.events(t, domain.EventNotificationFailed)); n != 1 {
		t.Fatalf("failed events = %d, want 1", n)
	}
	if c := f.channel(t).ConsecutiveFailures; c != 1 {
		t.Fatalf("consecutive failures = %d, want 1", c)
	}
	for range 2 {
		if err := rec.RecordSent(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(f.events(t, domain.EventNotificationSent)); n != 1 {
		t.Fatalf("sent events = %d, want 1", n)
	}
}

// Jobs without an alert (CloudEvent deliveries) still update channel
// health.
func TestRecorder_NoAlertStillTracksChannel(t *testing.T) {
	f := newOutcomeFixture(t)
	job := f.job()
	job.AlertID = ""
	if _, err := NewRecorder(f.store).RecordFailed(context.Background(), NewDeadLetter(job, errors.New("x"), time.Now())); err != nil {
		t.Fatal(err)
	}
	if c := f.channel(t).ConsecutiveFailures; c != 1 {
		t.Fatalf("consecutive failures = %d, want 1", c)
	}
}

// The dead letter payload never carries the raw provider error, the
// target config or the message body.
func TestDeadLetterPayloadIsSanitized(t *testing.T) {
	job := domain.NotificationJob{ID: "j", Channel: "telegram", TargetConfig: "c2VjcmV0LWNvbmZpZw==", Message: "body"}
	data, err := json.Marshal(NewDeadLetter(job, errors.New("POST https://x/bot"+secretToken+"/send: 401"), time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{secretToken, "c2VjcmV0LWNvbmZpZw==", "\"body\""} {
		if strings.Contains(string(data), leak) {
			t.Fatalf("dead letter payload leaks %q: %s", leak, data)
		}
	}
}

// Old notify.dead messages ({job, error}) left in the stream across an
// upgrade decode, and their raw error text is dropped.
func TestDecodeLegacyDeadLetter(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"job":   domain.NotificationJob{ID: "j1", AlertID: "a1", Channel: "slack", ChannelID: "c1"},
		"error": "slack returned 403 for token xoxb-" + secretToken,
	})
	dl, err := decodeDeadLetter(raw)
	if err != nil {
		t.Fatal(err)
	}
	if dl.JobID != "j1" || dl.AlertID != "a1" || dl.ChannelID != "c1" {
		t.Fatalf("decoded = %+v", dl)
	}
	if strings.Contains(dl.Reason, secretToken) || dl.Reason == "" {
		t.Fatalf("reason = %q", dl.Reason)
	}
	if _, err := decodeDeadLetter([]byte(`{}`)); err == nil {
		t.Fatal("empty payload should not decode")
	}
}

// The dead letter publish must not reuse the notify.request message ID:
// both subjects share the NOTIFICATIONS stream and its duplicate window.
func TestDeadLetterUsesDistinctMsgID(t *testing.T) {
	b := &msgIDBus{}
	d := NewDispatcher(b, nil)
	d.handleDeadLetter(domain.NotificationJob{ID: "job-1", Channel: "x"}, errors.New("e"))
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subject != subjectNotifyDead || b.msgID == "" || b.msgID == "job-1" {
		t.Fatalf("published %q with msgID %q", b.subject, b.msgID)
	}
}

type msgIDBus struct {
	bus.Bus
	mu      sync.Mutex
	subject string
	msgID   string
}

func (b *msgIDBus) PublishDurable(_ context.Context, subject string, _ []byte, msgID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subject, b.msgID = subject, msgID
	return nil
}
