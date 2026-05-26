package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
)

// makeReplySinkServer returns a sink that always replies with a
// claimed.v1 CloudEvent referencing the outbound's id as alertref.
func makeReplySinkServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outboundID := r.Header.Get("Ce-Id")
		replyEv := ce.NewEvent()
		replyEv.SetSpecVersion("1.0")
		replyEv.SetID("reply-" + outboundID)
		replyEv.SetSource("https://consumer.example.com/")
		replyEv.SetType("run.yipyap.reply.alert.claimed.v1")
		replyEv.SetTime(time.Now().UTC())
		replyEv.SetExtension("alertref", outboundID)
		_ = replyEv.SetData(ce.ApplicationJSON, map[string]string{
			"alert_id":   "01HXABCFIREDBBBBBBBB",
			"who":        "alice",
			"claimed_at": time.Now().UTC().Format(time.RFC3339),
		})
		b, _ := json.Marshal(replyEv)
		w.Header().Set("Content-Type", "application/cloudevents+json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	}))
}

// TestCloudEventHTTP_ReplyTierResolver_OverridesRateLimit verifies R2-C1:
// when SetReplyTierResolver returns a positive cap, that cap is plumbed
// into DispatcherConfig.RateLimit so the dispatcher's per-(org, type)
// budget reflects the org's plan rather than the channel-config value.
func TestCloudEventHTTP_ReplyTierResolver_OverridesRateLimit(t *testing.T) {
	sink := makeReplySinkServer()
	defer sink.Close()

	cases := []struct {
		name      string
		tierCap   int // returned by the resolver
		channelRL int // value baked into channel config
		wantHits  int // expected handler invocations across N sends
		sendCount int
	}{
		{
			// Pro tier: resolver returns 3, channel config carries the
			// looser 60. Expect dispatcher to apply the 3 cap.
			name:      "pro-tier-overrides-channel",
			tierCap:   3,
			channelRL: 60,
			sendCount: 5,
			wantHits:  3,
		},
		{
			// Enterprise tier: resolver returns 10, channel config carries
			// the same. Expect 5/5 to land.
			name:      "enterprise-tier-allows-all",
			tierCap:   10,
			channelRL: 10,
			sendCount: 5,
			wantHits:  5,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := reply.NewMemoryRegistry(100, time.Hour)
			d := reply.NewDispatcher(reg)
			h := &fakeHandler{typ: "run.yipyap.reply.alert.claimed.v1"}
			d.Register(h)

			p := NewCloudEventHTTP(nil)
			p.SetOutboundRegistry(reg)
			p.SetReplyDispatcher(d)

			var resolverCalls atomic.Int64
			p.SetReplyTierResolver(func(_ context.Context, orgID string) int {
				resolverCalls.Add(1)
				if orgID == "tenant-pro" {
					return tc.tierCap
				}
				return 0
			})

			cfg := CloudEventHTTPConfig{
				SinkURL: sink.URL,
				Mode:    "binary",
				AcceptedReplies: &ReplyConfig{
					Types:     []string{"run.yipyap.reply.alert.*"},
					RateLimit: tc.channelRL,
				},
			}
			cfgJSON, _ := json.Marshal(cfg)
			raw := append([]byte{0x00}, cfgJSON...)
			targetCfg := base64.StdEncoding.EncodeToString(raw)

			for i := 0; i < tc.sendCount; i++ {
				realJob, _ := buildOutboundJob(t, sink.URL, "tenant-pro", "chan-1",
					[]string{"run.yipyap.reply.alert.*"},
				)
				realJob.TargetConfig = targetCfg
				realJob.ID = fmt.Sprintf("job-%s-%d", tc.name, i)
				if _, err := p.Send(context.Background(), realJob); err != nil {
					t.Fatalf("Send: %v", err)
				}
			}

			if got := int(h.calls.Load()); got != tc.wantHits {
				t.Fatalf("handler hits = %d, want %d (cap=%d)",
					got, tc.wantHits, tc.tierCap)
			}
			if resolverCalls.Load() == 0 {
				t.Fatalf("tier resolver was never called")
			}
		})
	}
}

// TestCloudEventHTTP_ReplyTierResolver_NilLeavesChannelConfig verifies that
// a nil resolver (FOSS default) does not perturb the channel-supplied
// RateLimit, the dispatcher sees the value the operator configured.
func TestCloudEventHTTP_ReplyTierResolver_NilLeavesChannelConfig(t *testing.T) {
	sink := makeReplySinkServer()
	defer sink.Close()

	reg := reply.NewMemoryRegistry(100, time.Hour)
	d := reply.NewDispatcher(reg)
	h := &fakeHandler{typ: "run.yipyap.reply.alert.claimed.v1"}
	d.Register(h)

	p := NewCloudEventHTTP(nil)
	p.SetOutboundRegistry(reg)
	p.SetReplyDispatcher(d)
	// resolver intentionally not set (FOSS default).

	// Channel config caps at 2, tight enough that we'd hit it within the
	// loop; this proves the channel-supplied value still flows.
	cfg := CloudEventHTTPConfig{
		SinkURL: sink.URL,
		Mode:    "binary",
		AcceptedReplies: &ReplyConfig{
			Types:     []string{"run.yipyap.reply.alert.*"},
			RateLimit: 2,
		},
	}
	cfgJSON, _ := json.Marshal(cfg)
	raw := append([]byte{0x00}, cfgJSON...)
	targetCfg := base64.StdEncoding.EncodeToString(raw)

	for i := 0; i < 5; i++ {
		realJob, _ := buildOutboundJob(t, sink.URL, "foss-org", "chan-1",
			[]string{"run.yipyap.reply.alert.*"},
		)
		realJob.TargetConfig = targetCfg
		realJob.ID = fmt.Sprintf("job-foss-%d", i)
		if _, err := p.Send(context.Background(), realJob); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}

	if got := int(h.calls.Load()); got != 2 {
		t.Fatalf("handler hits = %d, want 2 (channel cap honoured when resolver is nil)", got)
	}
}
