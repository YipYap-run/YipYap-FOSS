package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// fakeHandler records each Handle invocation.
type fakeHandler struct {
	typ   string
	calls atomic.Int64
	last  struct {
		ev    ce.Event
		entry *reply.Entry
		sub   string
	}
}

func (h *fakeHandler) Type() string { return h.typ }

func (h *fakeHandler) Handle(_ context.Context, ev ce.Event, entry *reply.Entry, sub string) error {
	h.calls.Add(1)
	h.last.ev = ev
	h.last.entry = entry
	h.last.sub = sub
	return nil
}

// buildOutboundJob wraps a freshly-constructed outbound CloudEvent into the
// NotificationJob shape the dispatcher expects, with plaintext channel config.
func buildOutboundJob(t *testing.T, sinkURL, orgID, channelID string, acceptedTypes []string) (domain.NotificationJob, ce.Event) {
	t.Helper()
	ev := ce.NewEvent()
	ev.SetSpecVersion("1.0")
	ev.SetID("out-" + orgID + "-" + channelID)
	ev.SetSource("https://console.yipyap.run/orgs/" + orgID)
	ev.SetType("run.yipyap.alert.fired.v1")
	ev.SetTime(time.Now().UTC())
	_ = ev.SetData(ce.ApplicationJSON, map[string]any{
		"alert_id":   "01HX" + orgID + "FIREDBBBBB",
		"monitor_id": "01HX" + orgID + "MONITORBBB",
		"severity":   "critical",
		"reason":     "503 from /health",
		"fired_at":   time.Now().UTC().Format(time.RFC3339Nano),
	})
	evJSON, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}

	cfg := CloudEventHTTPConfig{
		SinkURL: sinkURL,
		Mode:    "binary",
		AcceptedReplies: &ReplyConfig{
			Types: acceptedTypes,
		},
	}
	cfgJSON, _ := json.Marshal(cfg)

	// prepend version byte 0x00 (plaintext envelope) per the existing pattern.
	raw := append([]byte{0x00}, cfgJSON...)
	return domain.NotificationJob{
		ID:           "job-" + channelID,
		OrgID:        orgID,
		Channel:      channelID,
		TargetConfig: base64.StdEncoding.EncodeToString(raw),
		Message:      string(evJSON),
	}, ev
}

func TestCloudEventHTTP_RecordsOutboundToRegistry(t *testing.T) {
	// Sink that just returns 200.
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer sink.Close()

	reg := reply.NewMemoryRegistry(100, time.Hour)
	p := NewCloudEventHTTP(nil)
	p.SetOutboundRegistry(reg)

	job, ev := buildOutboundJob(t, sink.URL, "org-1", "chan-1", nil)
	if _, err := p.Send(context.Background(), job); err != nil {
		t.Fatalf("Send: %v", err)
	}

	entry, ok := reg.Lookup(context.Background(), ev.ID())
	if !ok {
		t.Fatal("outbound registry missing the event id after Send")
	}
	if entry.OrgID != "org-1" || entry.ChannelID != "chan-1" {
		t.Errorf("entry=%+v, want OrgID=org-1 ChannelID=chan-1", entry)
	}
}

func TestCloudEventHTTP_ReplyDispatched(t *testing.T) {
	// Sink that responds with a structured-mode CloudEvent reply.
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	defer sink.Close()

	reg := reply.NewMemoryRegistry(100, time.Hour)
	d := reply.NewDispatcher(reg)
	handler := &fakeHandler{typ: "run.yipyap.reply.alert.claimed.v1"}
	d.Register(handler)

	p := NewCloudEventHTTP(nil)
	p.SetOutboundRegistry(reg)
	p.SetReplyDispatcher(d)

	job, ev := buildOutboundJob(t, sink.URL, "org-1", "chan-1",
		[]string{"run.yipyap.reply.alert.*"},
	)
	if _, err := p.Send(context.Background(), job); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if handler.calls.Load() != 1 {
		t.Fatalf("handler calls=%d, want 1", handler.calls.Load())
	}
	if handler.last.entry == nil || handler.last.entry.EventID != ev.ID() {
		t.Errorf("handler entry mismatch: %+v (want EventID=%s)", handler.last.entry, ev.ID())
	}
}

func TestCloudEventHTTP_ReplyWithoutDispatcher_StillLogs(t *testing.T) {
	// Sink replies with a CloudEvent; provider has no dispatcher, should
	// still succeed without error (Phase-1 log-only behaviour preserved).
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/cloudevents+json")
		w.Header().Set("Ce-Id", "reply-x")
		w.Header().Set("Ce-Type", "run.yipyap.reply.alert.claimed.v1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"specversion":"1.0","id":"reply-x","type":"run.yipyap.reply.alert.claimed.v1","source":"x","alertref":"out-org-1-chan-1"}`))
	}))
	defer sink.Close()

	p := NewCloudEventHTTP(nil)
	// No registry, no dispatcher.

	job, _ := buildOutboundJob(t, sink.URL, "org-1", "chan-1", nil)
	if _, err := p.Send(context.Background(), job); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

// TestCloudEventHTTP_ReplyDispatch_RejectsCrossOrgAlertref proves that the
// production reply path calls DispatchForChannel (not the unscoped Dispatch),
// by pre-seeding a registry entry whose OrgID/ChannelID differ from the
// outbound job's. A reply correlating to that entry must NOT invoke the
// handler, the dispatcher returns ErrOrgMismatch / ErrChannelMismatch.
func TestCloudEventHTTP_ReplyDispatch_RejectsCrossOrgAlertref(t *testing.T) {
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sink replies with a CloudEvent whose alertref is a STRANGER's
		// outbound id (different org / channel).
		replyEv := ce.NewEvent()
		replyEv.SetSpecVersion("1.0")
		replyEv.SetID("reply-stranger")
		replyEv.SetSource("https://consumer.example.com/")
		replyEv.SetType("run.yipyap.reply.alert.claimed.v1")
		replyEv.SetTime(time.Now().UTC())
		replyEv.SetExtension("alertref", "outbound-stranger-chan")
		b, _ := json.Marshal(replyEv)
		w.Header().Set("Content-Type", "application/cloudevents+json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	}))
	defer sink.Close()

	reg := reply.NewMemoryRegistry(100, time.Hour)

	// Pre-seed an entry from ORG-B / CHAN-B that the attacker references.
	strangerEv := ce.NewEvent()
	strangerEv.SetSpecVersion("1.0")
	strangerEv.SetID("outbound-stranger-chan")
	strangerEv.SetSource("https://console.yipyap.run/orgs/org-B")
	strangerEv.SetType("run.yipyap.alert.fired.v1")
	strangerEv.SetTime(time.Now().UTC())
	if err := reg.Record(context.Background(), strangerEv, "org-B", "chan-B"); err != nil {
		t.Fatalf("Record stranger: %v", err)
	}

	d := reply.NewDispatcher(reg)
	handler := &fakeHandler{typ: "run.yipyap.reply.alert.claimed.v1"}
	d.Register(handler)

	p := NewCloudEventHTTP(nil)
	p.SetOutboundRegistry(reg)
	p.SetReplyDispatcher(d)

	// Outbound job is from ORG-A / CHAN-A; attacker tries to co-opt org-B's
	// alertref on the reply path. DispatchForChannel must reject.
	job, _ := buildOutboundJob(t, sink.URL, "org-A", "chan-A",
		[]string{"run.yipyap.reply.alert.*"},
	)
	if _, err := p.Send(context.Background(), job); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if handler.calls.Load() != 0 {
		t.Fatalf("handler calls=%d, want 0 (cross-org reply must be rejected)", handler.calls.Load())
	}
}

func TestCloudEventHTTP_ReplyChannelOptOut_NoHandlerCall(t *testing.T) {
	// Sink replies; channel opts in to `linked` only, so `claimed` is a
	// silent no-op, handler is NOT called.
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outboundID := r.Header.Get("Ce-Id")
		replyEv := ce.NewEvent()
		replyEv.SetSpecVersion("1.0")
		replyEv.SetID("reply-" + outboundID)
		replyEv.SetSource("https://consumer.example.com/")
		replyEv.SetType("run.yipyap.reply.alert.claimed.v1")
		replyEv.SetTime(time.Now().UTC())
		replyEv.SetExtension("alertref", outboundID)
		b, _ := json.Marshal(replyEv)
		w.Header().Set("Content-Type", "application/cloudevents+json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	}))
	defer sink.Close()

	reg := reply.NewMemoryRegistry(100, time.Hour)
	d := reply.NewDispatcher(reg)
	handler := &fakeHandler{typ: "run.yipyap.reply.alert.claimed.v1"}
	d.Register(handler)

	p := NewCloudEventHTTP(nil)
	p.SetOutboundRegistry(reg)
	p.SetReplyDispatcher(d)

	// Channel opts in to `linked.v1` only, `claimed.v1` is silently dropped.
	job, _ := buildOutboundJob(t, sink.URL, "org-1", "chan-1",
		[]string{"run.yipyap.reply.alert.linked.v1"},
	)
	if _, err := p.Send(context.Background(), job); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if handler.calls.Load() != 0 {
		t.Fatalf("handler calls=%d, want 0 (opted out)", handler.calls.Load())
	}
}
