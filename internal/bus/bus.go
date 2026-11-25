package bus

import "context"

// AlertBroadcastPrefix is prepended to an alert subject to form the core
// broadcast subject that PublishAlert also publishes on. It must stay outside
// every JetStream stream's subjects: a stream captures plain core publishes on
// its subjects too, which stored each alert twice before. (core-16)
const AlertBroadcastPrefix = "ws."

// AlertBroadcastSubject returns the broadcast subject for an alert subject,
// e.g. "alert.trigger" becomes "ws.alert.trigger". Broadcast listeners (the
// WebSocket hub) subscribe here instead of on the durable subject.
func AlertBroadcastSubject(subject string) string {
	return AlertBroadcastPrefix + subject
}

type subscription struct {
	handler Handler
	queue   string // empty for broadcast subscribers
}

// Handler is a callback invoked when a message arrives on a subject.
type Handler func(ctx context.Context, subject string, data []byte) error

// Bus is the message bus abstraction. In single-box mode an in-process
// embedded implementation is used; in scaled mode a NATS-backed
// implementation can be swapped in transparently.
type Bus interface {
	// Publish sends data to the given subject.
	Publish(ctx context.Context, subject string, data []byte) error

	// Subscribe registers a broadcast handler  - every subscriber receives
	// every message published to the subject.
	Subscribe(subject string, handler Handler) error

	// QueueSubscribe registers a handler in a named queue group. For each
	// published message only one handler in the group is invoked.
	QueueSubscribe(subject, queue string, handler Handler) error

	// PublishDurable publishes a message with a deduplication ID to a
	// durable stream. On buses that do not support durable messaging
	// (e.g. the embedded in-process bus), this falls back to a plain
	// Publish and the msgID is ignored.
	PublishDurable(ctx context.Context, subject string, data []byte, msgID string) error

	// PublishAlert publishes an alert lifecycle event (alert.trigger,
	// alert.recover, alert.ack) on its subject for the escalation engine's
	// pull consumer, and once more on AlertBroadcastSubject(subject) for
	// broadcast listeners such as the WebSocket hub. The broadcast subject
	// sits outside the ALERTS stream, so a durable bus stores exactly one
	// copy of each event. (core-16)
	PublishAlert(ctx context.Context, subject string, data []byte, msgID string) error

	// PullSubscribe creates a durable pull-based consumer on the given
	// subject. The handler receives messages that must be explicitly
	// acknowledged. On non-durable buses this falls back to
	// QueueSubscribe with automatic ack after successful handler return.
	PullSubscribe(subject, consumer string, handler AckHandler, opts ...PullOpt) error

	// Unsubscribe removes the queue-group subscription registered for
	// (subject, queue). It is a no-op (and returns nil) when no matching
	// subscription exists. Implementations MUST also delete any durable
	// consumer state on backends that keep server-side state (JetStream
	// pull consumers).
	//
	// After Unsubscribe returns, the bus must drop messages destined for
	// that handler so an evicted consumer's closure can be GC'd. R2-H6.
	Unsubscribe(subject, queue string) error

	// Close releases resources and clears all subscriptions.
	Close() error
}
