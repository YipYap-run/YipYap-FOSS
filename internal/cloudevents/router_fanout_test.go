package cloudevents

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/crypto"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// decodeTargetConfig reverses Router.encodeConfig.
func decodeTargetConfig(t *testing.T, env *crypto.Envelope, target string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(target)
	if err != nil || len(raw) == 0 {
		t.Fatalf("TargetConfig %q is not a versioned envelope: %v", target, err)
	}
	switch raw[0] {
	case 0x00:
		return string(raw[1:])
	case 0x01:
		if env == nil {
			t.Fatal("encrypted TargetConfig without an envelope")
		}
		pt, err := env.Decrypt(raw[1:])
		if err != nil {
			t.Fatalf("decrypt TargetConfig: %v", err)
		}
		return string(pt)
	}
	t.Fatalf("unknown TargetConfig version %x", raw[0])
	return ""
}

// One event, two cloudevent_http channels: each job must name its own
// channel and carry that channel's config, with its own job ID and dedupe
// key. Channels in another org and disabled channels get nothing
// (notif-5, notif-18).
func TestRouter_FanOutJobsCarryTheirOwnChannel(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	env, err := crypto.NewEnvelope(key)
	if err != nil {
		t.Fatal(err)
	}
	for name, envelope := range map[string]*crypto.Envelope{"plaintext": nil, "encrypted": env} {
		t.Run(name, func(t *testing.T) {
			s, org := routerTestStore(t)
			b := bus.NewChannel()
			defer func() { _ = b.Close() }()

			a := seedChannel(t, s, org.ID, "cloudevent_http", "sink-a", `{"sink_url":"https://a.example/"}`, true)
			bch := seedChannel(t, s, org.ID, "cloudevent_http", "sink-b", `{"sink_url":"https://b.example/","mode":"binary"}`, true)
			seedChannel(t, s, org.ID, "cloudevent_http", "sink-off", `{"sink_url":"https://off.example/"}`, false)
			seedChannel(t, s, org.ID, "webhook", "hook", `{"url":"https://hook.example/"}`, true)

			now := time.Now().UTC()
			other := &domain.Org{ID: uuid.NewString(), Name: "Other", Slug: "x-" + uuid.NewString()[:8], Plan: domain.PlanFree, CreatedAt: now, UpdatedAt: now}
			if err := s.Orgs().Create(context.Background(), other); err != nil {
				t.Fatal(err)
			}
			seedChannel(t, s, other.ID, "cloudevent_http", "foreign", `{"sink_url":"https://foreign.example/"}`, true)

			cap := &jobCapture{}
			if err := b.Subscribe("notify.request", cap.handler); err != nil {
				t.Fatal(err)
			}
			r := NewRouter(b, s, nil, envelope)
			if err := r.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			ev := buildAlertFired(t, org.ID)
			publishCE(t, b, org.ID, ev)

			waitForJobs(cap, 2, time.Second)
			time.Sleep(100 * time.Millisecond) // let any extra job land
			jobs := cap.snapshot()
			if len(jobs) != 2 {
				t.Fatalf("got %d jobs, want 2: %+v", len(jobs), jobs)
			}

			want := map[string]*domain.NotificationChannel{a.ID: a, bch.ID: bch}
			ids := map[string]bool{}
			for _, j := range jobs {
				ch, ok := want[j.ChannelID]
				if !ok {
					t.Fatalf("job for unexpected channel %q (%s)", j.ChannelID, j.ChannelName)
				}
				delete(want, j.ChannelID)
				if j.OrgID != org.ID || j.Channel != "cloudevent_http" || j.ChannelName != ch.Name {
					t.Errorf("job = org %q channel %q name %q", j.OrgID, j.Channel, j.ChannelName)
				}
				if got := decodeTargetConfig(t, envelope, j.TargetConfig); got != ch.Config {
					t.Errorf("channel %s config = %q, want %q", ch.Name, got, ch.Config)
				}
				if wantKey := "ce:" + org.ID + ":" + ch.ID + ":" + ev.ID(); j.DedupeKey != wantKey {
					t.Errorf("dedupe key = %q, want %q", j.DedupeKey, wantKey)
				}
				if ids[j.ID] {
					t.Errorf("job ID %q used for two channels", j.ID)
				}
				ids[j.ID] = true
			}
			if len(want) != 0 {
				t.Fatalf("channels without a job: %v", want)
			}
		})
	}
}
