package bus

import (
	"context"
	"time"
)

// Msg represents a durable message received via PullSubscribe.
// For non-durable buses, Ack and Nak are no-ops.
type Msg struct {
	Subject string
	Data    []byte
	Headers map[string]string
	MsgID   string
	Ack     func() error
	Nak     func() error
	// NakWithDelay asks the server to redeliver no sooner than the given
	// delay. Nil on buses without delayed redelivery; use NakDelay, which
	// falls back to Nak.
	NakWithDelay func(time.Duration) error
}

// NakDelay negatively acknowledges the message with a redelivery delay when
// the bus supports it, and falls back to a plain Nak otherwise. It is a
// no-op when the message carries neither.
func (m *Msg) NakDelay(d time.Duration) error {
	if m.NakWithDelay != nil {
		return m.NakWithDelay(d)
	}
	if m.Nak != nil {
		return m.Nak()
	}
	return nil
}

// AckHandler is a callback for durable pull subscriptions. The handler
// is responsible for calling msg.Ack() on success or msg.Nak() on failure.
type AckHandler func(ctx context.Context, msg *Msg) error

// PullOpt is a functional option for configuring pull subscriptions.
type PullOpt func(*PullConfig)

// PullConfig holds configuration for durable pull subscriptions.
type PullConfig struct {
	// MaxDeliver is the maximum number of delivery attempts per message.
	MaxDeliver int
	// AckWait is how long the server waits for an ack before redelivery.
	AckWait time.Duration
	// BatchSize is the number of messages fetched per pull request.
	BatchSize int
	// DeliverNew starts a newly created durable consumer at the next
	// message published instead of the oldest one retained by the stream.
	// Only matters on Limits-retention streams, which keep acked messages.
	DeliverNew bool
	// InactiveThreshold lets the server delete a durable consumer that has
	// seen no pull requests for this long. Zero keeps it until deleted.
	InactiveThreshold time.Duration
}

// DefaultPullConfig returns a PullConfig with sensible defaults, modified
// by any supplied options.
func DefaultPullConfig(opts ...PullOpt) PullConfig {
	cfg := PullConfig{
		MaxDeliver: 5,
		AckWait:    30 * time.Second,
		BatchSize:  10,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// WithMaxDeliver sets the maximum number of delivery attempts.
func WithMaxDeliver(n int) PullOpt {
	return func(c *PullConfig) {
		c.MaxDeliver = n
	}
}

// WithAckWait sets the ack wait duration.
func WithAckWait(d time.Duration) PullOpt {
	return func(c *PullConfig) {
		c.AckWait = d
	}
}

// WithBatchSize sets the pull batch size.
func WithBatchSize(n int) PullOpt {
	return func(c *PullConfig) {
		c.BatchSize = n
	}
}

// ConsumerStopper is implemented by buses whose pull consumers fetch in a
// background loop (JetStream). StopConsuming stops those loops and waits
// for the handlers they are running to return, while keeping the
// connection open so messages handed to workers can still be acked or
// naked. Durable consumer state is kept, so other replicas continue.
type ConsumerStopper interface {
	StopConsuming()
}

// StopConsuming stops pull-consumer fetch loops on b when it supports it.
// It is a no-op for buses that deliver in-process (the outbox covers
// anything published to them after shutdown begins).
func StopConsuming(b Bus) {
	if cs, ok := b.(ConsumerStopper); ok {
		cs.StopConsuming()
	}
}

// WithDeliverNew makes a newly created durable consumer start at new
// messages rather than replaying the stream's retained history.
func WithDeliverNew() PullOpt {
	return func(c *PullConfig) {
		c.DeliverNew = true
	}
}

// WithInactiveThreshold lets the server reap the durable consumer after d
// without pull requests, so a crashed process does not leave it behind.
func WithInactiveThreshold(d time.Duration) PullOpt {
	return func(c *PullConfig) {
		c.InactiveThreshold = d
	}
}
