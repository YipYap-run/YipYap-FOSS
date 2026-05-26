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
