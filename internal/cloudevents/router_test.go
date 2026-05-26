package cloudevents

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sync"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// routerTestStore seeds an in-memory sqlite store with migrations applied
// and a single seed org.
func routerTestStore(t *testing.T) (*sqlite.SQLiteStore, *domain.Org) {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	now := time.Now().UTC().Truncate(time.Second)
	org := &domain.Org{
		ID: uuid.New().String(), Name: "Org", Slug: "o-" + uuid.New().String()[:8],
		Plan: domain.PlanFree, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Orgs().Create(context.Background(), org); err != nil {
		t.Fatal(err)
	}
	return s, org
}

// jobCapture collects NotificationJobs published to notify.request.
type jobCapture struct {
	mu   sync.Mutex
	jobs []domain.NotificationJob
}

func (c *jobCapture) handler(_ context.Context, _ string, data []byte) error {
	var job domain.NotificationJob
	if err := json.Unmarshal(data, &job); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.jobs = append(c.jobs, job)
	return nil
}

func (c *jobCapture) snapshot() []domain.NotificationJob {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]domain.NotificationJob, len(c.jobs))
	copy(out, c.jobs)
	return out
}

// waitForJobs polls the capture until it has at least n jobs or timeout.
func waitForJobs(c *jobCapture, n int, timeout time.Duration) []domain.NotificationJob {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got := c.snapshot(); len(got) >= n {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	return c.snapshot()
}

// seedChannel inserts a NotificationChannel.
func seedChannel(t *testing.T, s store.Store, orgID, chType, name, cfg string, enabled bool) *domain.NotificationChannel {
	t.Helper()
	ch := &domain.NotificationChannel{
		ID:      uuid.New().String(),
		OrgID:   orgID,
		Type:    chType,
		Name:    name,
		Config:  cfg,
		Enabled: enabled,
	}
	if err := s.NotificationChannels().Create(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
	return ch
}

// publishCE serializes a built CloudEvent onto the bus under the canonical
// egress subject. Returns the subject used.
func publishCE(t *testing.T, b bus.Bus, orgID string, ev ce.Event) string {
	t.Helper()
	payload, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	subj := SubjectFor(orgID, ev.Type())
	if err := b.PublishDurable(context.Background(), subj, payload, ev.ID()); err != nil {
		t.Fatalf("publish: %v", err)
	}
	return subj
}

func buildAlertFired(t *testing.T, orgID string) ce.Event {
	t.Helper()
	ev, err := NewAlertFiredV1(OrgSource(orgID), types.AlertFiredV1Data{
		AlertID:   "alert-1",
		MonitorID: "mon-1",
		Severity:  "critical",
		Reason:    "probe failed",
		FiredAt:   time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("build event: %v", err)
	}
	return ev
}

func TestRouter_FansOutToMatchingChannels(t *testing.T) {
	s, org := routerTestStore(t)
	b := bus.NewChannel()
	defer func() { _ = b.Close() }()

	// Two enabled cloudevent_http channels; one with a filter, one without.
	seedChannel(t, s, org.ID, "cloudevent_http", "a",
		`{"sink_url":"https://a.example/","mode":"structured"}`, true)
	seedChannel(t, s, org.ID, "cloudevent_http", "b",
		`{"sink_url":"https://b.example/","mode":"structured","event_types":["run.yipyap.alert.*"]}`, true)

	cap := &jobCapture{}
	if err := b.Subscribe("notify.request", cap.handler); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	r := NewRouter(b, s, nil, nil)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("start router: %v", err)
	}

	publishCE(t, b, org.ID, buildAlertFired(t, org.ID))

	jobs := waitForJobs(cap, 2, time.Second)
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs, want 2: %+v", len(jobs), jobs)
	}
	for _, j := range jobs {
		if j.Channel != "cloudevent_http" {
			t.Errorf("job.Channel = %q, want cloudevent_http", j.Channel)
		}
		if j.OrgID != org.ID {
			t.Errorf("job.OrgID = %q, want %q", j.OrgID, org.ID)
		}
	}
}

func TestRouter_SkipsDisabledChannels(t *testing.T) {
	s, org := routerTestStore(t)
	b := bus.NewChannel()
	defer func() { _ = b.Close() }()

	seedChannel(t, s, org.ID, "cloudevent_http", "enabled",
		`{"sink_url":"https://a.example/"}`, true)
	seedChannel(t, s, org.ID, "cloudevent_http", "disabled",
		`{"sink_url":"https://b.example/"}`, false)

	cap := &jobCapture{}
	_ = b.Subscribe("notify.request", cap.handler)

	r := NewRouter(b, s, nil, nil)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("start router: %v", err)
	}

	publishCE(t, b, org.ID, buildAlertFired(t, org.ID))

	jobs := waitForJobs(cap, 1, 500*time.Millisecond)
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1", len(jobs))
	}
}

func TestRouter_SkipsWrongChannelType(t *testing.T) {
	s, org := routerTestStore(t)
	b := bus.NewChannel()
	defer func() { _ = b.Close() }()

	seedChannel(t, s, org.ID, "webhook", "hook",
		`{"url":"https://example.com/"}`, true)

	cap := &jobCapture{}
	_ = b.Subscribe("notify.request", cap.handler)

	r := NewRouter(b, s, nil, nil)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("start router: %v", err)
	}

	publishCE(t, b, org.ID, buildAlertFired(t, org.ID))

	// Wait a little to ensure nothing comes through.
	time.Sleep(200 * time.Millisecond)
	if got := cap.snapshot(); len(got) != 0 {
		t.Fatalf("expected 0 jobs, got %d", len(got))
	}
}

func TestRouter_MalformedSubject_NoCrash(t *testing.T) {
	s, _ := routerTestStore(t)
	b := bus.NewChannel()
	defer func() { _ = b.Close() }()

	cap := &jobCapture{}
	_ = b.Subscribe("notify.request", cap.handler)

	r := NewRouter(b, s, nil, nil)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("start router: %v", err)
	}

	// Publish on a subject that matches "events.cloudevents.out.>" only if
	// there is a trailing token. Use a minimally-tokenized "events.cloudevents.out.x"
	// to simulate a malformed event where the org segment is missing a type.
	// Then also test missing-org by publishing directly to the prefix, the
	// wildcard ">" requires at least one trailing segment, so this is
	// indirectly exercising router robustness by feeding a truncated subject
	// through a broadcast subscribe that also grabs it.
	//
	// Note: NATS "events.cloudevents.out.>" does not match
	// "events.cloudevents.out" exactly; we verify the router does not crash
	// and emits nothing.
	_ = b.Publish(context.Background(), "events.cloudevents.out", []byte(`{"malformed":true}`))

	// Also send a single-segment-after-prefix case that WILL match the
	// wildcard. This has an orgID but no type; router must gracefully
	// handle it (empty type).
	_ = b.Publish(context.Background(), "events.cloudevents.out.only-org", []byte(`not-json-either`))

	time.Sleep(200 * time.Millisecond)
	if got := cap.snapshot(); len(got) != 0 {
		t.Fatalf("expected 0 jobs on malformed subject, got %d", len(got))
	}
}

func TestRouter_JobMessageContainsFullEnvelope(t *testing.T) {
	s, org := routerTestStore(t)
	b := bus.NewChannel()
	defer func() { _ = b.Close() }()

	seedChannel(t, s, org.ID, "cloudevent_http", "sink",
		`{"sink_url":"https://a.example/"}`, true)

	cap := &jobCapture{}
	_ = b.Subscribe("notify.request", cap.handler)

	r := NewRouter(b, s, nil, nil)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("start router: %v", err)
	}

	ev := buildAlertFired(t, org.ID)
	wantID := ev.ID()
	wantType := ev.Type()
	publishCE(t, b, org.ID, ev)

	jobs := waitForJobs(cap, 1, time.Second)
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1", len(jobs))
	}
	job := jobs[0]

	// job.Message is the CloudEvent structured-mode JSON envelope.
	var roundtrip ce.Event
	if err := json.Unmarshal([]byte(job.Message), &roundtrip); err != nil {
		t.Fatalf("unmarshal CE envelope from job.Message: %v", err)
	}
	if roundtrip.ID() != wantID {
		t.Errorf("roundtrip ID = %q, want %q", roundtrip.ID(), wantID)
	}
	if roundtrip.Type() != wantType {
		t.Errorf("roundtrip Type = %q, want %q", roundtrip.Type(), wantType)
	}

	// DedupeKey is org-prefixed with the event ID.
	wantDedupe := org.ID + "-" + wantID
	if job.DedupeKey != wantDedupe {
		t.Errorf("job.DedupeKey = %q, want %q", job.DedupeKey, wantDedupe)
	}

	// TargetConfig is base64-encoded with a 0x00 version byte (envelope is nil).
	raw, err := base64.StdEncoding.DecodeString(job.TargetConfig)
	if err != nil {
		t.Fatalf("decode TargetConfig: %v", err)
	}
	if len(raw) == 0 || raw[0] != 0x00 {
		t.Fatalf("TargetConfig version byte = %x, want 0x00", raw)
	}
}
