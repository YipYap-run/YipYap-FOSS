package reply

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents"
)

// DispatcherConfig carries the per-channel reply configuration. It is
// derived from the channel's encrypted TargetConfig by the caller.
//
// An empty AcceptedReplyTypes list means the channel has not opted in to
// any reply type, Dispatch treats valid replies as silent no-ops in that
// case (accepted=false, err=nil).
type DispatcherConfig struct {
	// AcceptedReplyTypes is a list of path.Match globs over the reply CE
	// type. A reply whose type matches any entry is accepted. Empty = deny.
	AcceptedReplyTypes []string

	// RateLimit is the per-minute cap per (orgID, replyType). Values <=0
	// use the package default (60/min).
	RateLimit int

	// TrustElevated gates the high-blast-radius reply types that perform
	// state mutations a malicious downstream could weaponize: escalated,
	// route, and monitor.deregister. A channel must opt in explicitly.
	TrustElevated bool

	// AllowDeregister is a second gate required (in addition to
	// TrustElevated) for reply.monitor.deregister. Splitting the flag lets
	// an operator trust a channel to escalate/route without also trusting
	// it to disable monitors, the latter is the one that takes the
	// notification loop permanently dark.
	AllowDeregister bool

	// MaxSuppressSeconds caps reply.alert.suppressed duration_seconds.
	// Zero uses the package default (3600).
	MaxSuppressSeconds int

	// MaxRemediationSeconds caps reply.alert.remediation_started
	// expected_duration_seconds. Zero uses the package default (600).
	MaxRemediationSeconds int

	// MaxRouteDuration caps reply.alert.route ExpiresAt (seconds from now).
	// Zero uses the package default (86400, i.e. 24h).
	MaxRouteDuration int
}

// DefaultMaxSuppressSeconds is the package-level cap on
// reply.alert.suppressed duration_seconds when the channel has not set a
// per-channel override. Exported so handlers can consult the helper in
// ClampSuppressSeconds.
const (
	DefaultMaxSuppressSeconds    = 3600
	DefaultMaxRemediationSeconds = 600
	DefaultMaxRouteDuration      = 86400
)

// ClampSuppressSeconds returns min(requested, cap) where cap is
// cfg.MaxSuppressSeconds (falling back to the package default if unset).
// A zero or negative requested value returns the cap.
func (cfg DispatcherConfig) ClampSuppressSeconds(requested int) int {
	cap := cfg.MaxSuppressSeconds
	if cap <= 0 {
		cap = DefaultMaxSuppressSeconds
	}
	if requested <= 0 || requested > cap {
		return cap
	}
	return requested
}

// ClampRemediationSeconds returns min(requested, cap) where cap is
// cfg.MaxRemediationSeconds (falling back to the package default if unset).
func (cfg DispatcherConfig) ClampRemediationSeconds(requested int) int {
	cap := cfg.MaxRemediationSeconds
	if cap <= 0 {
		cap = DefaultMaxRemediationSeconds
	}
	if requested <= 0 || requested > cap {
		return cap
	}
	return requested
}

// ClampRouteDuration returns min(requested, cap) where cap is
// cfg.MaxRouteDuration (falling back to the package default if unset).
func (cfg DispatcherConfig) ClampRouteDuration(requested int) int {
	cap := cfg.MaxRouteDuration
	if cap <= 0 {
		cap = DefaultMaxRouteDuration
	}
	if requested <= 0 || requested > cap {
		return cap
	}
	return requested
}

// configKey is the unexported context key for DispatcherConfig. Using an
// unexported type prevents accidental collisions with other packages that
// also stash values in the request context.
type configKey struct{}

// WithConfig returns a new context carrying cfg. Dispatcher.DispatchForChannel
// (and, for parity, Dispatch) wrap the handler's ctx with this before
// invoking Handle so handlers can pull the cfg without a method-signature
// change that would break Phase-2 handlers.
func WithConfig(ctx context.Context, cfg DispatcherConfig) context.Context {
	return context.WithValue(ctx, configKey{}, cfg)
}

// ConfigFromContext returns the DispatcherConfig previously stashed by
// WithConfig, or the zero value + false when no config is attached. Handlers
// that rely on channel-configurable caps/trust flags should use this; handlers
// that don't (the Phase-2 read-only three) can ignore it.
func ConfigFromContext(ctx context.Context) (DispatcherConfig, bool) {
	v, ok := ctx.Value(configKey{}).(DispatcherConfig)
	return v, ok
}

// Option configures a Dispatcher at construction.
type Option func(*Dispatcher)

// WithAlertrefTTL overrides the default alertref age limit (24h).
func WithAlertrefTTL(ttl time.Duration) Option {
	return func(d *Dispatcher) {
		if ttl > 0 {
			d.alertrefTTL = ttl
		}
	}
}

// WithClock swaps the dispatcher's clock. Intended for tests. The same
// clock is injected into the dispatcher's internal rate limiter. If a
// limiter already exists (because NewDispatcher ran the defaults first)
// it is closed so its sweep goroutine does not leak.
func WithClock(fn func() time.Time) Option {
	return func(d *Dispatcher) {
		if fn != nil {
			d.clock = fn
			if d.limiter != nil {
				d.limiter.Close()
			}
			d.limiter = newRateLimiter(fn)
		}
	}
}

// RejectAudit is the per-rejection callback the dispatcher invokes when a
// reply is denied at dispatchCore (org/channel mismatch, trust flag,
// rate-limit, or unknown alertref). The callback writes a row into the
// reply audit log so the admin reply-activity dashboard surfaces the
// rejection alongside accepted replies.
//
// Implementations MUST be best-effort: log on error, never block the
// dispatcher's primary return. The dispatcher does not pass a nullable
// context, callers should use context.Background() when the parent ctx
// is already cancelled (for example during shutdown) so the audit row
// still lands.
//
// Outcome strings follow the existing handler-layer convention (≤32
// chars to fit MariaDB's outcome column), prefixed with "rejected:":
//
//	rejected:org_mismatch
//	rejected:channel_mismatch
//	rejected:trust_required
//	rejected:rate_limited
//	rejected:unknown_alertref
type RejectAudit struct {
	// ReplyID, ReplyType, ReplySource pull from the inbound CloudEvent.
	ReplyID     string
	ReplyType   string
	ReplySource string
	// OrgID is the channel-owner org's id. For org-mismatch rejections
	// this is the EXPECTED org (the one whose dashboard should surface
	// the attempt), not the registry entry's org.
	OrgID string
	// ChannelID is the channel that surfaced the reply, when known.
	ChannelID string
	// AlertID and MonitorID are populated when the registry entry
	// resolved (org/channel mismatch and trust/rate paths). Empty for
	// unknown-alertref where the dispatcher has no entry to consult.
	AlertID   string
	MonitorID string
	// Outcome is the canonical rejected:<reason> string above.
	Outcome string
}

// RejectAuditFunc is the dispatcher's audit-write callback. It is
// invoked synchronously inside dispatchCore for every terminal rejection
// branch.
type RejectAuditFunc func(ctx context.Context, audit RejectAudit)

// WithRejectAudit installs an audit writer for dispatcher-level
// rejections. Without this option, rejections only emit slog warnings.
// The cmd-wiring layer should pass a closure that wraps a store handle
// so the dispatcher itself stays free of a store dependency. See
// R2-H3 / NEW-H-2.
func WithRejectAudit(fn RejectAuditFunc) Option {
	return func(d *Dispatcher) {
		if fn != nil {
			d.rejectAudit = fn
		}
	}
}

// Dispatcher validates and routes inbound CloudEvent replies.
type Dispatcher struct {
	registry OutboundRegistry

	hmu      sync.RWMutex
	handlers map[string]Handler

	limiter *rateLimiter

	alertrefTTL time.Duration
	clock       func() time.Time

	// rejectAudit is invoked at every terminal rejection branch so the
	// admin dashboard can surface dispatcher-level denials alongside
	// handler-level ones. nil = no audit (slog warnings only).
	rejectAudit RejectAuditFunc
}

// NewDispatcher returns a Dispatcher backed by the given registry.
func NewDispatcher(reg OutboundRegistry, opts ...Option) *Dispatcher {
	d := &Dispatcher{
		registry:    reg,
		handlers:    make(map[string]Handler),
		alertrefTTL: 24 * time.Hour,
		clock:       time.Now,
	}
	d.limiter = newRateLimiter(d.clock)
	for _, opt := range opts {
		if opt != nil {
			opt(d)
		}
	}
	return d
}

// Close stops the dispatcher's internal janitor goroutines. Idempotent.
// Callers that dispose dispatchers at process shutdown (or in tests)
// should call Close so the rate-limiter sweep goroutine exits.
func (d *Dispatcher) Close() {
	if d == nil || d.limiter == nil {
		return
	}
	d.limiter.Close()
}

// Register wires h as the handler for h.Type(). Idempotent, a later call
// with the same type replaces the earlier registration. Safe for concurrent
// use.
func (d *Dispatcher) Register(h Handler) {
	if d == nil || h == nil || h.Type() == "" {
		return
	}
	d.hmu.Lock()
	defer d.hmu.Unlock()
	d.handlers[h.Type()] = h
}

// Dispatch validates and (on success) executes a reply event. See package
// docs for the semantics of the (accepted, err) return tuple:
//
//   - (false, ErrNoAlertref)            missing alertref extension
//   - (false, ErrUnknownAlertref)       alertref not in registry
//   - (false, ErrAlertrefExpired)       registry TTL elapsed
//   - (false, ErrUnknownType)           no handler for ev.Type()
//   - (false, nil)                      opt-out (type not in cfg.AcceptedReplyTypes)
//   - (false, ErrRateLimited)           per-(org, type) limit exceeded
//   - (false, ErrHandler-wrapped)       handler returned error
//   - (true, nil)                       accepted and handled
func (d *Dispatcher) Dispatch(ctx context.Context, ev ce.Event, cfg DispatcherConfig, sub string) (bool, error) {
	return d.dispatchCore(ctx, ev, cfg, sub, "", "")
}

// DispatchForChannel validates and routes a reply with the additional
// constraint that the registry entry for ce_alertref must belong to
// expectedOrgID and (if non-empty) expectedChannelID. This prevents one
// org's authenticated ingest endpoint from replaying another org's
// outbound events.
//
// Callers that know the authenticated org/channel context (e.g. the
// cloudevent_http inbound provider) should prefer this method over
// Dispatch. An empty expectedChannelID skips the channel check (useful
// for org-level ingest endpoints that fan out across channels).
//
// Additional sentinel errors beyond Dispatch:
//
//   - (false, ErrOrgMismatch)     entry.OrgID != expectedOrgID
//   - (false, ErrChannelMismatch) entry.ChannelID != expectedChannelID
func (d *Dispatcher) DispatchForChannel(ctx context.Context, ev ce.Event, cfg DispatcherConfig, expectedOrgID, expectedChannelID, sub string) (bool, error) {
	return d.dispatchCore(ctx, ev, cfg, sub, expectedOrgID, expectedChannelID)
}

// trustRequiredReplyTypes maps reply CE types that require TrustElevated
// (and, for deregister, AllowDeregister too) to their required flag set.
// Centralising the policy at the dispatcher layer, instead of scattering
// it across handlers, ensures a future handler that forgets to check
// still gets blocked.
//
// The TrustElevated bit is the operator's "this sink may mutate alert
// state" toggle: every state-mutating reply (silencer, ownership shift,
// remediation lifecycle, status-page poke) is gated here so a sink that
// the operator has not explicitly trusted with elevated-mutation power
// cannot weaponize the reply channel even if its handler-level check is
// later refactored away. Phase-2 read-only annotations (claimed,
// enriched, linked) stay outside the gate, they only append to the
// alert timeline. monitor.deregister keeps its second flag because
// disabling a monitor takes the notification loop dark, a strictly
// higher blast radius than alert-level mutation.
//
// Keep this list in sync with handler-level checks (defense-in-depth).
var trustRequiredReplyTypes = map[string]struct {
	RequireTrustElevated bool
	RequireDeregister    bool
}{
	"run.yipyap.reply.alert.ack_with_context.v1":    {RequireTrustElevated: true},
	"run.yipyap.reply.alert.suppressed.v1":          {RequireTrustElevated: true},
	"run.yipyap.reply.alert.ownership.v1":           {RequireTrustElevated: true},
	"run.yipyap.reply.alert.remediation_started.v1": {RequireTrustElevated: true},
	"run.yipyap.reply.alert.remediation_result.v1":  {RequireTrustElevated: true},
	"run.yipyap.reply.alert.status_page.v1":         {RequireTrustElevated: true},
	"run.yipyap.reply.alert.escalated.v1":           {RequireTrustElevated: true},
	"run.yipyap.reply.alert.route.v1":               {RequireTrustElevated: true},
	"run.yipyap.reply.monitor.deregister.v1":        {RequireTrustElevated: true, RequireDeregister: true},
}

// Canonical dispatcher-level rejection outcome strings. Kept ≤32 chars so
// MariaDB's outcome VARCHAR(32) (round-1 L6) does not truncate them, and
// prefixed with "rejected:" to match the handler-layer convention.
const (
	rejectOrgMismatch     = "rejected:org_mismatch"     // 21
	rejectChannelMismatch = "rejected:channel_mismatch" // 25
	rejectTrustRequired   = "rejected:trust_required"   // 23
	rejectRateLimited     = "rejected:rate_limited"     // 21
	rejectUnknownAlertref = "rejected:unknown_alertref" // 25
	rejectAlertrefExpired = "rejected:alertref_expired" // 25
	rejectUnknownType     = "rejected:unknown_type"     // 21
)

// auditReject pipes a rejection through the configured audit callback (if
// any). Best-effort: callbacks that panic or block are the caller's
// problem to fix, the dispatcher contract is "synchronous, swallow
// errors, never alter primary return".
func (d *Dispatcher) auditReject(ctx context.Context, ev ce.Event, entry *Entry, expectedOrgID, expectedChannelID, outcome string) {
	if d == nil || d.rejectAudit == nil {
		return
	}
	a := RejectAudit{
		ReplyID:     ev.ID(),
		ReplyType:   ev.Type(),
		ReplySource: ev.Source(),
		Outcome:     outcome,
	}
	if entry != nil {
		// For org-mismatch, the channel-owner org is the expected one;
		// the dashboard query scopes by that org so the reply lands
		// against the right operator. For everything else the entry's
		// own org is correct.
		if outcome == rejectOrgMismatch && expectedOrgID != "" {
			a.OrgID = expectedOrgID
		} else {
			a.OrgID = entry.OrgID
		}
		a.AlertID = entry.AlertID
		a.MonitorID = entry.MonitorID
		a.ChannelID = entry.ChannelID
	}
	if expectedChannelID != "" && a.ChannelID == "" {
		a.ChannelID = expectedChannelID
	}
	d.rejectAudit(ctx, a)
}

// dispatchCore is the shared validation + routing body behind Dispatch and
// DispatchForChannel. Pass expectedOrgID == "" to skip the org check and
// expectedChannelID == "" to skip the channel check.
func (d *Dispatcher) dispatchCore(ctx context.Context, ev ce.Event, cfg DispatcherConfig, sub, expectedOrgID, expectedChannelID string) (bool, error) {
	if d == nil {
		return false, ErrUnknownType
	}

	alertref := extractAlertref(ev)
	if alertref == "" {
		// No alertref, no entry, no org context. Leave audit silent;
		// the no-alertref class is high-volume garbage from misconfigured
		// sinks and would flood the dashboard if recorded.
		return false, ErrNoAlertref
	}

	entry, ok := d.registry.Lookup(ctx, alertref)
	if !ok {
		// Unknown alertref: scope the audit row to the EXPECTED org so
		// the channel operator sees the probe attempt. Without an
		// expected org (Dispatch path), there is nowhere to file it.
		if expectedOrgID != "" && d.rejectAudit != nil {
			d.rejectAudit(ctx, RejectAudit{
				ReplyID:     ev.ID(),
				ReplyType:   ev.Type(),
				ReplySource: ev.Source(),
				OrgID:       expectedOrgID,
				ChannelID:   expectedChannelID,
				Outcome:     rejectUnknownAlertref,
			})
		}
		return false, ErrUnknownAlertref
	}
	if d.alertrefTTL > 0 && d.clock().Sub(entry.EmittedAt) > d.alertrefTTL {
		d.auditReject(ctx, ev, entry, expectedOrgID, expectedChannelID, rejectAlertrefExpired)
		return false, ErrAlertrefExpired
	}

	if expectedOrgID != "" && entry.OrgID != expectedOrgID {
		slog.Warn("reply: org mismatch on dispatch",
			"reply_type", ev.Type(),
			"alertref", alertref,
			"entry_org", entry.OrgID,
			"expected_org", expectedOrgID,
		)
		d.auditReject(ctx, ev, entry, expectedOrgID, expectedChannelID, rejectOrgMismatch)
		return false, ErrOrgMismatch
	}
	if expectedChannelID != "" && entry.ChannelID != expectedChannelID {
		slog.Warn("reply: channel mismatch on dispatch",
			"reply_type", ev.Type(),
			"alertref", alertref,
			"entry_channel", entry.ChannelID,
			"expected_channel", expectedChannelID,
		)
		d.auditReject(ctx, ev, entry, expectedOrgID, expectedChannelID, rejectChannelMismatch)
		return false, ErrChannelMismatch
	}

	d.hmu.RLock()
	handler, ok := d.handlers[ev.Type()]
	d.hmu.RUnlock()
	if !ok {
		slog.Warn("reply: unknown reply type rejected",
			"reply_type", ev.Type(),
			"alertref", alertref,
			"source", ev.Source(),
		)
		d.auditReject(ctx, ev, entry, expectedOrgID, expectedChannelID, rejectUnknownType)
		return false, ErrUnknownType
	}

	if !cloudevents.MatchAnyGlob(ev.Type(), cfg.AcceptedReplyTypes) {
		// Channel has not opted in to this reply type; silent no-op.
		// No audit either, this is the operator's intentional config,
		// not an attack signal.
		return false, nil
	}

	// Enforce trust flags BEFORE consuming a rate-limit slot so that a sink
	// spamming escalated/route/deregister without TrustElevated can't burn
	// the org's (org, type) budget. Handler-level checks remain as
	// defense-in-depth.
	if req, ok := trustRequiredReplyTypes[ev.Type()]; ok {
		if req.RequireTrustElevated && !cfg.TrustElevated {
			slog.Warn("reply: trust flag required",
				"reply_type", ev.Type(),
				"alertref", alertref,
				"org", entry.OrgID,
				"missing", "TrustElevated",
			)
			d.auditReject(ctx, ev, entry, expectedOrgID, expectedChannelID, rejectTrustRequired)
			return false, ErrTrustFlagRequired
		}
		if req.RequireDeregister && !cfg.AllowDeregister {
			slog.Warn("reply: trust flag required",
				"reply_type", ev.Type(),
				"alertref", alertref,
				"org", entry.OrgID,
				"missing", "AllowDeregister",
			)
			d.auditReject(ctx, ev, entry, expectedOrgID, expectedChannelID, rejectTrustRequired)
			return false, ErrTrustFlagRequired
		}
	}

	if !d.limiter.Allow(entry.OrgID, ev.Type(), cfg.RateLimit) {
		d.auditReject(ctx, ev, entry, expectedOrgID, expectedChannelID, rejectRateLimited)
		return false, ErrRateLimited
	}

	ctx = WithConfig(ctx, cfg)
	if err := handler.Handle(ctx, ev, entry, sub); err != nil {
		return false, fmt.Errorf("%w: %v", ErrHandler, err)
	}
	return true, nil
}

// extractAlertref pulls the alertref extension attribute from ev. The
// canonical SDK form is "alertref"; we also accept "ce_alertref" for
// defensive parity with the on-the-wire header name.
func extractAlertref(ev ce.Event) string {
	exts := ev.Extensions()
	if exts == nil {
		return ""
	}
	for _, k := range []string{"alertref", "ce_alertref", "cealertref"} {
		if raw, ok := exts[k]; ok {
			if s, ok := stringExt(raw); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func stringExt(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case fmt.Stringer:
		return x.String(), true
	case nil:
		return "", false
	default:
		return fmt.Sprintf("%v", x), true
	}
}
