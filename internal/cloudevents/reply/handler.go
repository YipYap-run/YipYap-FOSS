package reply

import (
	"context"

	ce "github.com/cloudevents/sdk-go/v2"
)

// Handler processes a specific reply type. The dispatcher has already
// verified that the reply's ce_alertref matches an entry in the outbound
// registry, so implementations can assume the referenced alert or monitor
// is real.
//
// Implementations live in Task 2.9+.
type Handler interface {
	// Type returns the CloudEvent type this handler processes,
	// e.g. "run.yipyap.reply.alert.claimed.v1".
	Type() string

	// Handle executes the reply's effect. `original` is the registry entry
	// describing the outbound event being replied to. `sub` is the
	// OIDC-verified subject of the replying party (empty when the ingest
	// endpoint is unauthenticated).
	Handle(ctx context.Context, ev ce.Event, original *Entry, sub string) error
}
