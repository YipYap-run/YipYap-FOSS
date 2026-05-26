package reply

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
)

// mockHandler captures Handle invocations for assertions.
type mockHandler struct {
	typ       string
	calls     atomic.Int64
	lastEvent ce.Event
	lastEntry *Entry
	lastSub   string
	returnErr error
}

func (m *mockHandler) Type() string { return m.typ }

func (m *mockHandler) Handle(ctx context.Context, ev ce.Event, entry *Entry, sub string) error {
	m.calls.Add(1)
	m.lastEvent = ev
	m.lastEntry = entry
	m.lastSub = sub
	return m.returnErr
}

// buildReply constructs a CloudEvent whose alertref extension points at the
// given outbound event id.
func buildReply(t *testing.T, replyType, alertref string) ce.Event {
	t.Helper()
	ev := ce.NewEvent()
	ev.SetSpecVersion("1.0")
	ev.SetID("reply-" + alertref)
	ev.SetSource("https://consumer.example.com/")
	ev.SetType(replyType)
	ev.SetTime(time.Now().UTC())
	if alertref != "" {
		ev.SetExtension("alertref", alertref)
	}
	return ev
}

// recordOutbound inserts a fake outbound entry into the registry so a reply
// can correlate against it. Returns the event id used.
func recordOutbound(t *testing.T, reg OutboundRegistry, orgID, channelID string) string {
	t.Helper()
	ev := ce.NewEvent()
	ev.SetSpecVersion("1.0")
	ev.SetID("outbound-" + orgID + "-" + channelID)
	ev.SetSource("https://console.yipyap.run/orgs/" + orgID)
	ev.SetType("run.yipyap.alert.fired.v1")
	ev.SetTime(time.Now().UTC())
	if err := reg.Record(context.Background(), ev, orgID, channelID); err != nil {
		t.Fatalf("registry.Record: %v", err)
	}
	return ev.ID()
}

func TestDispatcher_NoAlertref(t *testing.T) {
	reg := NewMemoryRegistry(100, time.Hour)
	d := NewDispatcher(reg)
	h := &mockHandler{typ: "run.yipyap.reply.alert.claimed.v1"}
	d.Register(h)

	ev := buildReply(t, h.typ, "")
	cfg := DispatcherConfig{AcceptedReplyTypes: []string{"run.yipyap.reply.*"}}

	accepted, err := d.Dispatch(context.Background(), ev, cfg, "")
	if accepted {
		t.Fatal("accepted=true, want false")
	}
	if !errors.Is(err, ErrNoAlertref) {
		t.Fatalf("err=%v, want ErrNoAlertref", err)
	}
	if h.calls.Load() != 0 {
		t.Fatalf("handler calls=%d, want 0", h.calls.Load())
	}
}

func TestDispatcher_UnknownAlertref(t *testing.T) {
	reg := NewMemoryRegistry(100, time.Hour)
	d := NewDispatcher(reg)
	h := &mockHandler{typ: "run.yipyap.reply.alert.claimed.v1"}
	d.Register(h)

	ev := buildReply(t, h.typ, "never-recorded")
	cfg := DispatcherConfig{AcceptedReplyTypes: []string{"run.yipyap.reply.*"}}

	accepted, err := d.Dispatch(context.Background(), ev, cfg, "")
	if accepted {
		t.Fatal("accepted=true, want false")
	}
	if !errors.Is(err, ErrUnknownAlertref) {
		t.Fatalf("err=%v, want ErrUnknownAlertref", err)
	}
}

func TestDispatcher_AlertrefExpired(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	reg := NewMemoryRegistry(100, 72*time.Hour) // registry TTL generous
	d := NewDispatcher(reg, WithAlertrefTTL(time.Hour), WithClock(clock.Now))
	h := &mockHandler{typ: "run.yipyap.reply.alert.claimed.v1"}
	d.Register(h)

	outID := recordOutbound(t, reg, "org-1", "chan-1")
	// advance 2h, past the dispatcher's alertref TTL but within registry TTL.
	clock.Advance(2 * time.Hour)

	ev := buildReply(t, h.typ, outID)
	cfg := DispatcherConfig{AcceptedReplyTypes: []string{"run.yipyap.reply.*"}}
	accepted, err := d.Dispatch(context.Background(), ev, cfg, "")
	if accepted {
		t.Fatal("accepted=true, want false")
	}
	if !errors.Is(err, ErrAlertrefExpired) {
		t.Fatalf("err=%v, want ErrAlertrefExpired", err)
	}
}

func TestDispatcher_UnknownType(t *testing.T) {
	reg := NewMemoryRegistry(100, time.Hour)
	d := NewDispatcher(reg)
	// no Register call

	outID := recordOutbound(t, reg, "org-1", "chan-1")
	ev := buildReply(t, "run.yipyap.reply.alert.claimed.v1", outID)
	cfg := DispatcherConfig{AcceptedReplyTypes: []string{"run.yipyap.reply.*"}}
	accepted, err := d.Dispatch(context.Background(), ev, cfg, "")
	if accepted {
		t.Fatal("accepted=true, want false")
	}
	if !errors.Is(err, ErrUnknownType) {
		t.Fatalf("err=%v, want ErrUnknownType", err)
	}
}

func TestDispatcher_ChannelOptOut(t *testing.T) {
	reg := NewMemoryRegistry(100, time.Hour)
	d := NewDispatcher(reg)
	h := &mockHandler{typ: "run.yipyap.reply.alert.claimed.v1"}
	d.Register(h)

	outID := recordOutbound(t, reg, "org-1", "chan-1")
	ev := buildReply(t, h.typ, outID)
	// Channel opts in to linked but NOT claimed.
	cfg := DispatcherConfig{AcceptedReplyTypes: []string{"run.yipyap.reply.alert.linked.v1"}}

	accepted, err := d.Dispatch(context.Background(), ev, cfg, "")
	if accepted {
		t.Fatal("accepted=true, want false")
	}
	if err != nil {
		t.Fatalf("err=%v, want nil (silent opt-out)", err)
	}
	if h.calls.Load() != 0 {
		t.Fatalf("handler calls=%d, want 0 (opted out)", h.calls.Load())
	}
}

func TestDispatcher_Accepted_CallsHandler(t *testing.T) {
	reg := NewMemoryRegistry(100, time.Hour)
	d := NewDispatcher(reg)
	h := &mockHandler{typ: "run.yipyap.reply.alert.claimed.v1"}
	d.Register(h)

	outID := recordOutbound(t, reg, "org-1", "chan-1")
	ev := buildReply(t, h.typ, outID)
	cfg := DispatcherConfig{AcceptedReplyTypes: []string{"run.yipyap.reply.*"}}

	accepted, err := d.Dispatch(context.Background(), ev, cfg, "sub-user-42")
	if err != nil {
		t.Fatalf("err=%v, want nil", err)
	}
	if !accepted {
		t.Fatal("accepted=false, want true")
	}
	if h.calls.Load() != 1 {
		t.Fatalf("handler calls=%d, want 1", h.calls.Load())
	}
	if h.lastSub != "sub-user-42" {
		t.Errorf("sub=%q, want sub-user-42", h.lastSub)
	}
	if h.lastEntry == nil || h.lastEntry.OrgID != "org-1" {
		t.Errorf("entry=%+v, want OrgID=org-1", h.lastEntry)
	}
	if h.lastEntry.EventID != outID {
		t.Errorf("entry.EventID=%q, want %q", h.lastEntry.EventID, outID)
	}
}

func TestDispatcher_HandlerError(t *testing.T) {
	reg := NewMemoryRegistry(100, time.Hour)
	d := NewDispatcher(reg)
	boom := fmt.Errorf("boom")
	h := &mockHandler{typ: "run.yipyap.reply.alert.claimed.v1", returnErr: boom}
	d.Register(h)

	outID := recordOutbound(t, reg, "org-1", "chan-1")
	ev := buildReply(t, h.typ, outID)
	cfg := DispatcherConfig{AcceptedReplyTypes: []string{"run.yipyap.reply.*"}}

	accepted, err := d.Dispatch(context.Background(), ev, cfg, "")
	if accepted {
		t.Fatal("accepted=true, want false")
	}
	if !errors.Is(err, ErrHandler) {
		t.Fatalf("err=%v, want ErrHandler-wrapped", err)
	}
}

func TestDispatcher_RateLimited(t *testing.T) {
	reg := NewMemoryRegistry(100, time.Hour)
	d := NewDispatcher(reg)
	h := &mockHandler{typ: "run.yipyap.reply.alert.claimed.v1"}
	d.Register(h)

	cfg := DispatcherConfig{
		AcceptedReplyTypes: []string{"run.yipyap.reply.*"},
		RateLimit:          2,
	}

	for i := 0; i < 2; i++ {
		outID := recordOutbound(t, reg, "org-1", fmt.Sprintf("chan-%d", i))
		ev := buildReply(t, h.typ, outID)
		accepted, err := d.Dispatch(context.Background(), ev, cfg, "")
		if err != nil || !accepted {
			t.Fatalf("iter %d: accepted=%v err=%v", i, accepted, err)
		}
	}
	// Third call exceeds rate limit.
	outID := recordOutbound(t, reg, "org-1", "chan-3")
	ev := buildReply(t, h.typ, outID)
	accepted, err := d.Dispatch(context.Background(), ev, cfg, "")
	if accepted {
		t.Fatal("accepted=true, want false on rate-limited")
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err=%v, want ErrRateLimited", err)
	}
}

func TestDispatcher_GlobMatching(t *testing.T) {
	reg := NewMemoryRegistry(100, time.Hour)
	d := NewDispatcher(reg)
	claimed := &mockHandler{typ: "run.yipyap.reply.alert.claimed.v1"}
	enriched := &mockHandler{typ: "run.yipyap.reply.alert.enriched.v1"}
	linked := &mockHandler{typ: "run.yipyap.reply.alert.linked.v1"}
	d.Register(claimed)
	d.Register(enriched)
	d.Register(linked)

	// Channel opts in to claimed + enriched via explicit list. linked is
	// excluded, opt-out is silent.
	cfg := DispatcherConfig{AcceptedReplyTypes: []string{
		"run.yipyap.reply.alert.claimed.v1",
		"run.yipyap.reply.alert.enriched.v1",
	}}

	for _, tc := range []struct {
		name    string
		h       *mockHandler
		wantOK  bool
		wantErr error
	}{
		{"claimed-accepted", claimed, true, nil},
		{"enriched-accepted", enriched, true, nil},
		{"linked-optout", linked, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outID := recordOutbound(t, reg, "org-1", tc.name)
			ev := buildReply(t, tc.h.typ, outID)
			accepted, err := d.Dispatch(context.Background(), ev, cfg, "")
			if accepted != tc.wantOK {
				t.Errorf("accepted=%v, want %v", accepted, tc.wantOK)
			}
			if !errors.Is(err, tc.wantErr) && (err != nil || tc.wantErr != nil) {
				t.Errorf("err=%v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestDispatcher_GlobWildcard(t *testing.T) {
	reg := NewMemoryRegistry(100, time.Hour)
	d := NewDispatcher(reg)
	h := &mockHandler{typ: "run.yipyap.reply.alert.claimed.v1"}
	d.Register(h)

	// Wildcard glob must match.
	cfg := DispatcherConfig{AcceptedReplyTypes: []string{"run.yipyap.reply.*"}}
	outID := recordOutbound(t, reg, "org-1", "chan-1")
	ev := buildReply(t, h.typ, outID)
	accepted, err := d.Dispatch(context.Background(), ev, cfg, "")
	if !accepted || err != nil {
		t.Fatalf("accepted=%v err=%v, want true/nil", accepted, err)
	}
}

// TestDispatcher_TrustFlagRejectedBeforeRateLimit verifies H-23: a
// high-blast-radius reply type is rejected at the dispatcher when the
// channel has not opted in via TrustElevated, AND the rate-limit bucket
// is NOT consumed. Without this, a sink spamming un-opted-in escalated
// replies could burn the org's rate budget for escalated, locking out a
// future legitimate escalation for up to a minute.
func TestDispatcher_TrustFlagRejectedBeforeRateLimit(t *testing.T) {
	reg := NewMemoryRegistry(100, time.Hour)
	d := NewDispatcher(reg)
	h := &mockHandler{typ: "run.yipyap.reply.alert.escalated.v1"}
	d.Register(h)

	cfg := DispatcherConfig{
		AcceptedReplyTypes: []string{"run.yipyap.reply.*"},
		RateLimit:          2,
		// TrustElevated: false, the handler-level gate would have caught
		// this too, but the dispatcher must reject earlier.
	}

	// Drive 5 attempts; every one should return ErrTrustFlagRequired.
	for i := 0; i < 5; i++ {
		outID := recordOutbound(t, reg, "org-1", fmt.Sprintf("chan-%d", i))
		ev := buildReply(t, h.typ, outID)
		accepted, err := d.Dispatch(context.Background(), ev, cfg, "")
		if accepted {
			t.Fatalf("iter %d: accepted=true, want false", i)
		}
		if !errors.Is(err, ErrTrustFlagRequired) {
			t.Fatalf("iter %d: err=%v, want ErrTrustFlagRequired", i, err)
		}
	}

	// Rate-limit budget preserved: now opt the channel in and verify two
	// accepted dispatches land (cap=2).
	cfg.TrustElevated = true
	for i := 0; i < 2; i++ {
		outID := recordOutbound(t, reg, "org-1", fmt.Sprintf("legit-%d", i))
		ev := buildReply(t, h.typ, outID)
		accepted, err := d.Dispatch(context.Background(), ev, cfg, "")
		if err != nil || !accepted {
			t.Fatalf("legit %d: accepted=%v err=%v, want true/nil", i, accepted, err)
		}
	}
	if got := h.calls.Load(); got != 2 {
		t.Fatalf("handler calls=%d, want 2 (rejected attempts must NOT invoke handler)", got)
	}
}

// TestDispatcher_TrustGateCoversEveryStateMutatingType verifies R2-H1:
// the dispatcher's trustRequiredReplyTypes table now covers every
// state-mutating reply type (not just escalated/route/deregister), so a
// channel without TrustElevated cannot weaponize the reply path even if
// the handler-level check is later refactored away.
func TestDispatcher_TrustGateCoversEveryStateMutatingType(t *testing.T) {
	mutatingTypes := []string{
		"run.yipyap.reply.alert.ack_with_context.v1",
		"run.yipyap.reply.alert.suppressed.v1",
		"run.yipyap.reply.alert.ownership.v1",
		"run.yipyap.reply.alert.remediation_started.v1",
		"run.yipyap.reply.alert.remediation_result.v1",
		"run.yipyap.reply.alert.status_page.v1",
		"run.yipyap.reply.alert.escalated.v1",
		"run.yipyap.reply.alert.route.v1",
	}

	for _, replyType := range mutatingTypes {
		t.Run(replyType, func(t *testing.T) {
			reg := NewMemoryRegistry(100, time.Hour)
			d := NewDispatcher(reg)
			h := &mockHandler{typ: replyType}
			d.Register(h)

			outID := recordOutbound(t, reg, "org-1", "chan-1")
			ev := buildReply(t, replyType, outID)

			// TrustElevated: false, should be rejected at the dispatcher
			// before the handler is invoked.
			cfg := DispatcherConfig{
				AcceptedReplyTypes: []string{"run.yipyap.reply.*"},
				RateLimit:          5,
			}
			accepted, err := d.Dispatch(context.Background(), ev, cfg, "")
			if accepted {
				t.Fatalf("accepted=true, want false (TrustElevated missing)")
			}
			if !errors.Is(err, ErrTrustFlagRequired) {
				t.Fatalf("err=%v, want ErrTrustFlagRequired", err)
			}
			if got := h.calls.Load(); got != 0 {
				t.Fatalf("handler calls=%d, want 0 (gated by dispatcher)", got)
			}

			// Opt in via TrustElevated; reply now flows through.
			cfg.TrustElevated = true
			outID2 := recordOutbound(t, reg, "org-1", "chan-2")
			ev2 := buildReply(t, replyType, outID2)
			accepted, err = d.Dispatch(context.Background(), ev2, cfg, "")
			if err != nil {
				t.Fatalf("err with TrustElevated=true: %v", err)
			}
			if !accepted {
				t.Fatal("accepted=false with TrustElevated=true, want true")
			}
			if got := h.calls.Load(); got != 1 {
				t.Fatalf("handler calls=%d, want 1", got)
			}
		})
	}
}

// TestDispatcher_AllowDeregisterRequiredForMonitorDeregister verifies
// that monitor.deregister needs BOTH TrustElevated AND AllowDeregister.
func TestDispatcher_AllowDeregisterRequiredForMonitorDeregister(t *testing.T) {
	reg := NewMemoryRegistry(100, time.Hour)
	d := NewDispatcher(reg)
	h := &mockHandler{typ: "run.yipyap.reply.monitor.deregister.v1"}
	d.Register(h)

	outID := recordOutbound(t, reg, "org-1", "chan-1")
	ev := buildReply(t, h.typ, outID)

	// TrustElevated only, still rejected (AllowDeregister missing).
	cfg := DispatcherConfig{
		AcceptedReplyTypes: []string{"run.yipyap.reply.*"},
		TrustElevated:      true,
	}
	accepted, err := d.Dispatch(context.Background(), ev, cfg, "")
	if accepted || !errors.Is(err, ErrTrustFlagRequired) {
		t.Fatalf("accepted=%v err=%v, want ErrTrustFlagRequired (AllowDeregister missing)", accepted, err)
	}

	// Both flags, dispatcher lets the handler run.
	cfg.AllowDeregister = true
	out2 := recordOutbound(t, reg, "org-1", "chan-2")
	ev2 := buildReply(t, h.typ, out2)
	if _, err := d.Dispatch(context.Background(), ev2, cfg, ""); err != nil {
		t.Fatalf("unexpected err with both flags: %v", err)
	}
	if got := h.calls.Load(); got != 1 {
		t.Fatalf("handler calls=%d, want 1", got)
	}
}

// fakeClock is a monotonic test clock used by TestDispatcher_AlertrefExpired
// and similar time-sensitive tests. It is safe for single-threaded use.
type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time         { return f.t }
func (f *fakeClock) Advance(d time.Duration) { f.t = f.t.Add(d) }
