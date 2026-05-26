package bus

import (
	"context"
	"math/rand/v2"
	"strings"
	"sync"
)

// ChannelBus is an in-process message bus for FOSS single-binary deployments.
// It implements the full Bus interface using goroutines for async delivery.
// Durable operations (PublishDurable, PullSubscribe) gracefully fall back to
// their non-durable equivalents.
type ChannelBus struct {
	mu   sync.RWMutex
	subs map[string][]subscription
}

// NewChannel returns a ready-to-use in-process channel bus.
func NewChannel() *ChannelBus {
	return &ChannelBus{
		subs: make(map[string][]subscription),
	}
}

func (b *ChannelBus) Subscribe(subject string, handler Handler) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[subject] = append(b.subs[subject], subscription{handler: handler})
	return nil
}

func (b *ChannelBus) QueueSubscribe(subject, queue string, handler Handler) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[subject] = append(b.subs[subject], subscription{handler: handler, queue: queue})
	return nil
}

// Unsubscribe drops every subscription registered against (subject, queue).
// No-op if no match. R2-H6: lets ceConsumerCache eviction stop new messages
// reaching the cache slot it just freed.
func (b *ChannelBus) Unsubscribe(subject, queue string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	subs := b.subs[subject]
	if len(subs) == 0 {
		return nil
	}
	kept := subs[:0]
	for _, s := range subs {
		if s.queue == queue {
			continue
		}
		kept = append(kept, s)
	}
	if len(kept) == 0 {
		delete(b.subs, subject)
	} else {
		b.subs[subject] = kept
	}
	return nil
}

// subjectMatches reports whether a subscription pattern matches a concrete
// published subject. Supported wildcards mirror NATS: "*" matches exactly
// one token and ">" matches one or more trailing tokens. A bare exact subject
// (no wildcards) compares for equality.
func subjectMatches(pattern, subject string) bool {
	if pattern == subject {
		return true
	}
	if !strings.ContainsAny(pattern, "*>") {
		return false
	}
	pt := strings.Split(pattern, ".")
	st := strings.Split(subject, ".")
	for i, tok := range pt {
		if tok == ">" {
			return i <= len(st)-1
		}
		if i >= len(st) {
			return false
		}
		if tok == "*" {
			continue
		}
		if tok != st[i] {
			return false
		}
	}
	return len(pt) == len(st)
}

func (b *ChannelBus) Publish(ctx context.Context, subject string, data []byte) error {
	b.mu.RLock()
	var snapshot []subscription
	for pattern, subs := range b.subs {
		if subjectMatches(pattern, subject) {
			snapshot = append(snapshot, subs...)
		}
	}
	b.mu.RUnlock()

	// Partition into broadcast and per-queue-group buckets.
	queues := make(map[string][]subscription)
	var broadcast []subscription
	for _, s := range snapshot {
		if s.queue == "" {
			broadcast = append(broadcast, s)
		} else {
			queues[s.queue] = append(queues[s.queue], s)
		}
	}

	// Deliver to every broadcast subscriber.
	for _, s := range broadcast {
		go func(h Handler) { _ = h(ctx, subject, data) }(s.handler)
	}

	// Deliver to exactly one subscriber per queue group.
	for _, members := range queues {
		chosen := members[rand.IntN(len(members))]
		go func(h Handler) { _ = h(ctx, subject, data) }(chosen.handler)
	}

	return nil
}

// PublishDurable falls back to a plain Publish; the msgID is ignored.
func (b *ChannelBus) PublishDurable(ctx context.Context, subject string, data []byte, _ string) error {
	return b.Publish(ctx, subject, data)
}

// PullSubscribe adapts QueueSubscribe with an auto-ack Msg wrapper.
// The consumer name is used as the queue group so that multiple pull
// consumers on the same subject get competing-consumer semantics.
func (b *ChannelBus) PullSubscribe(subject, consumer string, handler AckHandler, _ ...PullOpt) error {
	return b.QueueSubscribe(subject, consumer, func(ctx context.Context, subj string, data []byte) error {
		msg := &Msg{
			Subject: subj,
			Data:    data,
			Ack:     func() error { return nil },
			Nak:     func() error { return nil },
		}
		return handler(ctx, msg)
	})
}

func (b *ChannelBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs = make(map[string][]subscription)
	return nil
}
