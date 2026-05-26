// Package reply implements validation and routing of inbound CloudEvents
// received as replies to outbound events emitted by yipyap. It does not
// perform the side effects of the reply itself, those are handled by
// Handler implementations registered with a Dispatcher.
//
// Two concerns live in this package:
//
//   - The OutboundRegistry records recently-emitted outbound events so that
//     reply events can be correlated back to them via the ce_alertref
//     extension attribute. The default implementation is an in-process
//     LRU with a TTL; a JetStream KV-backed implementation can be layered
//     on later when cross-replica correlation becomes necessary.
//
//   - The Dispatcher validates a reply's ce_alertref against the registry,
//     enforces per-channel opt-in (glob-matched reply types), rate-limits
//     per (org, reply type), and routes valid replies to the registered
//     Handler for that type. Handler invocations are the only place a
//     reply mutates system state.
package reply

import "errors"

// Sentinel errors returned by Dispatcher.Dispatch.
var (
	// ErrNoAlertref is returned when the reply event lacks the alertref
	// extension attribute (spelled either "alertref", SDK-native, or
	// "ce_alertref" for defensive parity with the on-the-wire header form).
	ErrNoAlertref = errors.New("reply: missing alertref extension")

	// ErrUnknownAlertref is returned when the reply references an event ID
	// that is not present in the outbound registry.
	ErrUnknownAlertref = errors.New("reply: alertref does not match any recent outbound event")

	// ErrAlertrefExpired is returned when the referenced outbound event is
	// older than the dispatcher's alertref TTL.
	ErrAlertrefExpired = errors.New("reply: alertref has expired")

	// ErrChannelOptOut is reserved, the current dispatcher reports opt-out
	// via (accepted=false, err=nil). Kept for callers that prefer an
	// explicit error when distinguishing opt-out from accepted-no-op.
	ErrChannelOptOut = errors.New("reply: channel has not opted in to this reply type")

	// ErrRateLimited is returned when the per (org, reply type) per-minute
	// rate limit is exceeded.
	ErrRateLimited = errors.New("reply: rate limited")

	// ErrUnknownType is returned when no Handler is registered for the
	// reply's CE type.
	ErrUnknownType = errors.New("reply: no handler registered for reply type")

	// ErrHandler wraps errors returned by a Handler.Handle call.
	ErrHandler = errors.New("reply: handler error")

	// ErrOrgMismatch is returned by DispatchForChannel when the registry
	// entry for the reply's alertref belongs to a different org than the
	// caller's authenticated org context.
	ErrOrgMismatch = errors.New("reply: entry belongs to a different org")

	// ErrChannelMismatch is returned by DispatchForChannel when the registry
	// entry for the reply's alertref was emitted by a different channel than
	// the one currently accepting the reply.
	ErrChannelMismatch = errors.New("reply: entry belongs to a different channel")

	// ErrTrustFlagRequired is returned by handlers for high-blast-radius
	// reply types (escalated, route, monitor.deregister) when the channel
	// config has not opted in via TrustElevated (and, for deregister,
	// AllowDeregister as well).
	ErrTrustFlagRequired = errors.New("reply: channel config missing trust flag for this reply type")

	// ErrAlertScopeMismatch is returned by alert-mutating reply handlers when
	// the registry entry is NOT alert-scoped (entry.AlertID == "") but the
	// handler requires an authenticated alert binding. This closes a
	// monitor-level-alertref hijack where a sink in org A could mutate an
	// alert in org B by responding to a monitor.* outbound with an
	// attacker-chosen data.alert_id.
	ErrAlertScopeMismatch = errors.New("reply: alert-scoped handler requires entry.AlertID")

)
