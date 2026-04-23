package bus

import "context"

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

	// PullSubscribe creates a durable pull-based consumer on the given
	// subject. The handler receives messages that must be explicitly
	// acknowledged. On non-durable buses this falls back to
	// QueueSubscribe with automatic ack after successful handler return.
	PullSubscribe(subject, consumer string, handler AckHandler, opts ...PullOpt) error

	// Close releases resources and clears all subscriptions.
	Close() error
}
