package handlers

import (
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// RegisterAll constructs and registers all reply handlers against the given
// dispatcher. Phase-2 provides the three read-only handlers (claimed,
// enriched, linked); Phase-5 adds ten state-mutating handlers (suppressed,
// deduplicated, escalated, remediation_started, remediation_result,
// ownership, ack_with_context, route, status_page, monitor.deregister).
// Returns the handler instances so tests can assert on them; callers in
// production do not need to retain them.
//
// If either d or s is nil, RegisterAll is a no-op and returns nil.
func RegisterAll(d *reply.Dispatcher, s store.Store) []reply.Handler {
	if d == nil || s == nil {
		return nil
	}
	hs := []reply.Handler{
		// Phase-2 read-only.
		NewClaimedHandler(s),
		NewEnrichedHandler(s),
		NewLinkedHandler(s),

		// Phase-5 state-mutating.
		NewSuppressedHandler(s),
		NewDeduplicatedHandler(s),
		NewEscalatedHandler(s),
		NewRemediationStartedHandler(s),
		NewRemediationResultHandler(s),
		NewOwnershipHandler(s),
		NewAckWithContextHandler(s),
		NewRouteHandler(s),
		NewStatusPageHandler(s),
		NewMonitorDeregisterHandler(s),
	}
	for _, h := range hs {
		d.Register(h)
	}
	return hs
}
