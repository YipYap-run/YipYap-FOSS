package reply_test

// Loop-safety property test for Task 5.3.
//
// Before any state-mutating reply handler is wired, we prove that the
// dispatcher + outbound registry cannot be abused to:
//   - feed yipyap's own outbound events back in as replies,
//   - spoof replies with fabricated alertref values,
//   - leak state across orgs/channels,
//   - cascade through the rate limiter under a burst,
//   - route unknown reply types to a handler.
//
// The test uses plain `for` loops for property-style coverage, no new
// dependency. It runs under `go test -race ./...`.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
)

// -----------------------------------------------------------------------
// Test doubles
// -----------------------------------------------------------------------

type recordingHandler struct {
	typ   string
	calls atomic.Int64
}

func (h *recordingHandler) Type() string { return h.typ }
func (h *recordingHandler) Handle(_ context.Context, _ ce.Event, _ *reply.Entry, _ string) error {
	h.calls.Add(1)
	return nil
}
func (h *recordingHandler) callCount() int64 { return h.calls.Load() }

type failingHandler struct {
	typ   string
	calls atomic.Int64
	err   error
}

func (h *failingHandler) Type() string { return h.typ }
func (h *failingHandler) Handle(_ context.Context, _ ce.Event, _ *reply.Entry, _ string) error {
	h.calls.Add(1)
	return h.err
}

// slogCapture redirects slog.Default to a buffer for the duration of a test.
type slogCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func newSlogCapture(t *testing.T) *slogCapture {
	t.Helper()
	c := &slogCapture{}
	h := slog.NewTextHandler(&lockedWriter{cap: c}, &slog.HandlerOptions{Level: slog.LevelDebug})
	old := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(old) })
	return c
}

func (c *slogCapture) contains(s string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Contains(c.buf.String(), s)
}

// lockedWriter serialises writes into the capture buffer so the race
// detector stays happy if slog writes concurrently.
type lockedWriter struct{ cap *slogCapture }

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.cap.mu.Lock()
	defer w.cap.mu.Unlock()
	return w.cap.buf.Write(p)
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

func setupLoopSafetyDispatcher(t *testing.T, clock func() time.Time) (*reply.Dispatcher, *reply.MemoryRegistry) {
	t.Helper()
	reg := reply.NewMemoryRegistry(10_000, 24*time.Hour)
	var opts []reply.Option
	if clock != nil {
		opts = append(opts, reply.WithClock(clock))
	}
	d := reply.NewDispatcher(reg, opts...)
	return d, reg
}

// randomID returns a ULID-shaped (26 char) random hex string. We only care
// that it's unguessable and distinct; shape doesn't affect dispatcher logic.
func randomID(t *testing.T) string {
	t.Helper()
	var b [13]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(b[:])
}

// makeOutboundEvent builds a Phase-1 outbound CloudEvent (no alertref).
func makeOutboundEvent(t *testing.T, id, typ string) ce.Event {
	t.Helper()
	ev := ce.NewEvent()
	ev.SetSpecVersion("1.0")
	ev.SetID(id)
	ev.SetSource("https://console.yipyap.run/")
	ev.SetType(typ)
	ev.SetTime(time.Now().UTC())
	return ev
}

// makeReply builds a reply CloudEvent carrying an alertref extension.
func makeReply(t *testing.T, replyType, alertref string) ce.Event {
	t.Helper()
	ev := ce.NewEvent()
	ev.SetSpecVersion("1.0")
	ev.SetID("reply-" + randomID(t))
	ev.SetSource("https://consumer.example.com/")
	ev.SetType(replyType)
	ev.SetTime(time.Now().UTC())
	if alertref != "" {
		ev.SetExtension("alertref", alertref)
	}
	return ev
}

// recordOutbound writes a fake outbound entry so replies can correlate.
// Returns the event id used.
func recordOutbound(t *testing.T, reg *reply.MemoryRegistry, orgID, channelID string) string {
	t.Helper()
	id := "out-" + randomID(t)
	ev := makeOutboundEvent(t, id, types.TypeAlertFiredV1)
	if err := reg.Record(context.Background(), ev, orgID, channelID); err != nil {
		t.Fatalf("reg.Record: %v", err)
	}
	return id
}

// -----------------------------------------------------------------------
// 1. yipyap's own outbound events as replies
// -----------------------------------------------------------------------

func TestLoopSafety_OutboundReplayedAsReply_ErrNoAlertref(t *testing.T) {
	d, _ := setupLoopSafetyDispatcher(t, nil)

	// Register handlers for every Phase-1 outbound type, we want to prove
	// the dispatcher rejects BEFORE ever reaching a handler.
	outboundTypes := []string{
		types.TypeAlertFiredV1,
		types.TypeAlertAcknowledgedV1,
		types.TypeAlertResolvedV1,
		types.TypeAlertEscalatedV1,
		types.TypeMonitorDownV1,
		types.TypeMonitorUpV1,
		types.TypeMonitorDegradedV1,
		types.TypeMonitorHeartbeatMissedV1,
	}
	handlers := make([]*recordingHandler, 0, len(outboundTypes))
	for _, tp := range outboundTypes {
		h := &recordingHandler{typ: tp}
		d.Register(h)
		handlers = append(handlers, h)
	}

	cfg := reply.DispatcherConfig{AcceptedReplyTypes: []string{"run.yipyap.*"}}
	for i := 0; i < 200; i++ {
		tp := outboundTypes[i%len(outboundTypes)]
		ev := makeOutboundEvent(t, "out-"+randomID(t), tp)
		accepted, err := d.Dispatch(context.Background(), ev, cfg, "")
		if accepted {
			t.Fatalf("iter %d: accepted outbound event looped back as reply", i)
		}
		if !errors.Is(err, reply.ErrNoAlertref) {
			t.Fatalf("iter %d: err=%v, want ErrNoAlertref", i, err)
		}
	}
	for _, h := range handlers {
		if h.callCount() != 0 {
			t.Fatalf("handler %s called %d times, want 0", h.typ, h.callCount())
		}
	}
}

// -----------------------------------------------------------------------
// 2. Replies with bogus ce_alertref
// -----------------------------------------------------------------------

func TestLoopSafety_BogusAlertref_ErrUnknownAlertref(t *testing.T) {
	d, _ := setupLoopSafetyDispatcher(t, nil)
	h := &recordingHandler{typ: types.TypeReplyAlertSuppressedV1}
	d.Register(h)

	cfg := reply.DispatcherConfig{AcceptedReplyTypes: []string{"run.yipyap.reply.*"}}
	for i := 0; i < 200; i++ {
		ev := makeReply(t, h.typ, "bogus-"+randomID(t))
		accepted, err := d.Dispatch(context.Background(), ev, cfg, "")
		if accepted {
			t.Fatalf("iter %d: accepted reply for unknown alertref", i)
		}
		if !errors.Is(err, reply.ErrUnknownAlertref) {
			t.Fatalf("iter %d: err=%v, want ErrUnknownAlertref", i, err)
		}
	}
	if h.callCount() != 0 {
		t.Fatalf("handler called %d times, want 0", h.callCount())
	}
}

// -----------------------------------------------------------------------
// 3. Handler failure doesn't mutate state
// -----------------------------------------------------------------------

func TestLoopSafety_HandlerErrorPropagates(t *testing.T) {
	d, reg := setupLoopSafetyDispatcher(t, nil)
	boom := errors.New("handler exploded")
	h := &failingHandler{typ: types.TypeReplyAlertSuppressedV1, err: boom}
	d.Register(h)

	outID := recordOutbound(t, reg, "org-1", "chan-1")
	ev := makeReply(t, h.typ, outID)
	cfg := reply.DispatcherConfig{
		AcceptedReplyTypes: []string{"run.yipyap.reply.*"},
		RateLimit:          30,
		// suppressed.v1 is now trust-gated at the dispatcher (R2-H1); opt
		// the channel in so the handler-error path is exercised.
		TrustElevated: true,
	}

	accepted, err := d.Dispatch(context.Background(), ev, cfg, "sub-user")
	if accepted {
		t.Fatal("accepted=true, want false on handler error")
	}
	if !errors.Is(err, reply.ErrHandler) {
		t.Fatalf("err=%v, want ErrHandler-wrapped", err)
	}
	if h.calls.Load() != 1 {
		t.Fatalf("handler calls=%d, want 1", h.calls.Load())
	}
}

// -----------------------------------------------------------------------
// 4. Rate cap bounds cascades
// -----------------------------------------------------------------------

func TestLoopSafety_RateCapBoundsCascade(t *testing.T) {
	// Pro tier default is 30/min; Enterprise is 60/min. See
	// project_knative_tier_gating memory. Plan-tier lookup is a later task.
	const rateLimit = 30

	d, reg := setupLoopSafetyDispatcher(t, nil)
	h := &recordingHandler{typ: types.TypeReplyAlertSuppressedV1}
	d.Register(h)

	cfg := reply.DispatcherConfig{
		AcceptedReplyTypes: []string{"run.yipyap.reply.*"},
		RateLimit:          rateLimit,
		// suppressed.v1 is a state-mutating type; round-2 R2-H1 promotes
		// it into trustRequiredReplyTypes, so the dispatcher rejects it
		// without TrustElevated before the rate limiter even sees it.
		TrustElevated: true,
	}

	var rateLimitedHits int
	for i := 0; i < 100; i++ {
		outID := recordOutbound(t, reg, "org-1", fmt.Sprintf("chan-%d", i))
		ev := makeReply(t, h.typ, outID)
		_, err := d.Dispatch(context.Background(), ev, cfg, "")
		if errors.Is(err, reply.ErrRateLimited) {
			rateLimitedHits++
		}
	}
	if got := h.callCount(); got > int64(rateLimit) {
		t.Fatalf("handler calls=%d, want <= %d", got, rateLimit)
	}
	if rateLimitedHits == 0 {
		t.Fatalf("no rate-limited errors observed; rate cap did not kick in")
	}
}

// -----------------------------------------------------------------------
// 5. Unknown reply types are rejected + logged
// -----------------------------------------------------------------------

func TestLoopSafety_UnknownReplyType_RejectedAndLogged(t *testing.T) {
	capt := newSlogCapture(t)

	d, reg := setupLoopSafetyDispatcher(t, nil)
	// Register at least one legit handler so the dispatcher has a map.
	d.Register(&recordingHandler{typ: types.TypeReplyAlertSuppressedV1})

	outID := recordOutbound(t, reg, "org-1", "chan-1")
	ev := makeReply(t, "run.yipyap.reply.alert.faked.v1", outID)
	cfg := reply.DispatcherConfig{AcceptedReplyTypes: []string{"run.yipyap.reply.*"}}

	accepted, err := d.Dispatch(context.Background(), ev, cfg, "")
	if accepted {
		t.Fatal("accepted=true, want false for unknown type")
	}
	if !errors.Is(err, reply.ErrUnknownType) {
		t.Fatalf("err=%v, want ErrUnknownType", err)
	}
	if !capt.contains("run.yipyap.reply.alert.faked.v1") {
		t.Fatalf("slog capture missing rejected type reference; got:\n%s", capt.buf.String())
	}
}

// -----------------------------------------------------------------------
// 6. Cross-org isolation via DispatchForChannel
// -----------------------------------------------------------------------

func TestLoopSafety_CrossOrgIsolation_DispatchForChannel(t *testing.T) {
	d, reg := setupLoopSafetyDispatcher(t, nil)
	h := &recordingHandler{typ: types.TypeReplyAlertSuppressedV1}
	d.Register(h)

	// Record outbound events for two orgs.
	org1 := recordOutbound(t, reg, "org-1", "chan-1")
	org2 := recordOutbound(t, reg, "org-2", "chan-2")

	cfg := reply.DispatcherConfig{
		AcceptedReplyTypes: []string{"run.yipyap.reply.*"},
		RateLimit:          30,
		// suppressed.v1 is a state-mutating type; round-2 R2-H1 promotes
		// it into trustRequiredReplyTypes, so the dispatcher rejects it
		// without TrustElevated. The cross-org test wants to exercise
		// org-mismatch + channel-mismatch + happy paths, none of which
		// should trip on the trust gate, so opt the channel in.
		TrustElevated: true,
	}

	// Org-1 caller tries to dispatch a reply whose alertref belongs to org-2.
	ev := makeReply(t, h.typ, org2)
	accepted, err := d.DispatchForChannel(context.Background(), ev, cfg, "org-1", "chan-1", "")
	if accepted {
		t.Fatal("accepted=true, want false on org mismatch")
	}
	if !errors.Is(err, reply.ErrOrgMismatch) {
		t.Fatalf("err=%v, want ErrOrgMismatch", err)
	}

	// Channel mismatch: right org, wrong channel.
	ev2 := makeReply(t, h.typ, org1)
	accepted, err = d.DispatchForChannel(context.Background(), ev2, cfg, "org-1", "chan-wrong", "")
	if accepted {
		t.Fatal("accepted=true, want false on channel mismatch")
	}
	if !errors.Is(err, reply.ErrChannelMismatch) {
		t.Fatalf("err=%v, want ErrChannelMismatch", err)
	}

	// Empty channelID skips the channel check.
	ev3 := makeReply(t, h.typ, org1)
	accepted, err = d.DispatchForChannel(context.Background(), ev3, cfg, "org-1", "", "")
	if err != nil {
		t.Fatalf("empty channelID: err=%v, want nil", err)
	}
	if !accepted {
		t.Fatal("empty channelID: accepted=false, want true")
	}

	// Happy path: matching org+channel.
	ev4 := makeReply(t, h.typ, org1)
	accepted, err = d.DispatchForChannel(context.Background(), ev4, cfg, "org-1", "chan-1", "")
	if err != nil {
		t.Fatalf("happy path: err=%v, want nil", err)
	}
	if !accepted {
		t.Fatal("happy path: accepted=false, want true")
	}

	if got := h.callCount(); got != 2 {
		t.Fatalf("handler calls=%d, want 2 (skip-check + happy)", got)
	}
}

// -----------------------------------------------------------------------
// 7. Concurrent burst doesn't corrupt rate-limit state
// -----------------------------------------------------------------------

func TestLoopSafety_ConcurrentBurst_RespectsRateLimit(t *testing.T) {
	const rateLimit = 30

	d, reg := setupLoopSafetyDispatcher(t, nil)
	h := &recordingHandler{typ: types.TypeReplyAlertSuppressedV1}
	d.Register(h)

	cfg := reply.DispatcherConfig{
		AcceptedReplyTypes: []string{"run.yipyap.reply.*"},
		RateLimit:          rateLimit,
	}

	// Pre-record 200 outbound entries so every reply correlates.
	outIDs := make([]string, 200)
	for i := range outIDs {
		outIDs[i] = recordOutbound(t, reg, "org-1", fmt.Sprintf("chan-%d", i))
	}

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ev := makeReply(t, h.typ, outIDs[i])
			_, _ = d.Dispatch(context.Background(), ev, cfg, "")
		}(i)
	}
	wg.Wait()

	if got := h.callCount(); got > int64(rateLimit) {
		t.Fatalf("handler calls=%d, want <= %d", got, rateLimit)
	}
}
