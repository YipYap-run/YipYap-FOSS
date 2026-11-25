package cloudevents_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// sinkRecorder stands in for the cloudevent_http provider and records
// which channel config each delivered job carried.
type sinkRecorder struct {
	mu   sync.Mutex
	jobs []domain.NotificationJob
}

func (r *sinkRecorder) Channel() string     { return "cloudevent_http" }
func (r *sinkRecorder) MaxConcurrency() int { return 2 }
func (r *sinkRecorder) Send(_ context.Context, job domain.NotificationJob) (string, error) {
	r.mu.Lock()
	r.jobs = append(r.jobs, job)
	r.mu.Unlock()
	return "ok", nil
}

func (r *sinkRecorder) byConfig() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]int{}
	for _, j := range r.jobs {
		out[j.TargetConfig]++
	}
	return out
}

// Two cloudevent_http channels in one org must each receive the event once
// through the real dispatcher (notif-5). The old org-scoped dedupe key let
// only one of them through.
func TestRouter_TwoChannelsEachReceiveOneEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	org := &domain.Org{ID: uuid.NewString(), Name: "Org", Slug: "o-" + uuid.NewString()[:8], Plan: domain.PlanFree, CreatedAt: now, UpdatedAt: now}
	if err := s.Orgs().Create(ctx, org); err != nil {
		t.Fatal(err)
	}
	for _, sink := range []string{"https://a.example/", "https://b.example/"} {
		ch := &domain.NotificationChannel{ID: uuid.NewString(), OrgID: org.ID, Type: "cloudevent_http", Name: sink, Config: `{"sink_url":"` + sink + `"}`, Enabled: true}
		if err := s.NotificationChannels().Create(ctx, ch); err != nil {
			t.Fatal(err)
		}
	}
	b := bus.NewChannel()
	defer func() { _ = b.Close() }()

	rec := &sinkRecorder{}
	d := notify.NewDispatcher(b, s.Dedup())
	d.Register(rec, 16)
	if err := d.Start(ctx); err != nil {
		t.Fatal(err)
	}

	r := cloudevents.NewRouter(b, s, nil, nil)
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	ev, err := cloudevents.NewAlertFiredV1(cloudevents.OrgSource(org.ID), types.AlertFiredV1Data{
		AlertID: "alert-1", MonitorID: "mon-1", Severity: "critical", Reason: "probe failed", FiredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.PublishDurable(ctx, cloudevents.SubjectFor(org.ID, ev.Type()), payload, ev.ID()); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for len(rec.byConfig()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	// Let any extra delivery land before counting.
	time.Sleep(50 * time.Millisecond)
	if err := d.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := rec.byConfig()
	if len(got) != 2 {
		t.Fatalf("channels reached = %d, want 2 (%v)", len(got), got)
	}
	for cfg, n := range got {
		if n != 1 {
			t.Fatalf("channel config %q got %d deliveries, want 1", cfg, n)
		}
	}
}
