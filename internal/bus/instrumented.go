package bus

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// BusMetrics holds the OTEL instruments for bus instrumentation.
type BusMetrics struct {
	PublishCount     metric.Int64Counter
	ConsumeCount     metric.Int64Counter
	ConsumeLatencyMS metric.Float64Histogram
}

// InstrumentedBus wraps any Bus and records publish/consume metrics.
type InstrumentedBus struct {
	Bus
	m BusMetrics
}

// Instrument wraps a Bus with OTEL metrics. If metrics is nil, returns
// the bus unchanged.
func Instrument(b Bus, m *BusMetrics) Bus {
	if m == nil || m.PublishCount == nil {
		return b
	}
	return &InstrumentedBus{Bus: b, m: *m}
}

func (b *InstrumentedBus) Publish(ctx context.Context, subject string, data []byte) error {
	err := b.Bus.Publish(ctx, subject, data)
	if err == nil {
		b.m.PublishCount.Add(ctx, 1, metric.WithAttributeSet(
			attribute.NewSet(attribute.String("subject", subject))))
	}
	return err
}

func (b *InstrumentedBus) PublishDurable(ctx context.Context, subject string, data []byte, msgID string) error {
	err := b.Bus.PublishDurable(ctx, subject, data, msgID)
	if err == nil {
		b.m.PublishCount.Add(ctx, 1, metric.WithAttributeSet(
			attribute.NewSet(attribute.String("subject", subject))))
	}
	return err
}

func (b *InstrumentedBus) Subscribe(subject string, handler Handler) error {
	return b.Bus.Subscribe(subject, b.wrapHandler(subject, handler))
}

func (b *InstrumentedBus) QueueSubscribe(subject, queue string, handler Handler) error {
	return b.Bus.QueueSubscribe(subject, queue, b.wrapHandler(subject, handler))
}

func (b *InstrumentedBus) PullSubscribe(subject, consumer string, handler AckHandler, opts ...PullOpt) error {
	wrapped := func(ctx context.Context, msg *Msg) error {
		start := time.Now()
		err := handler(ctx, msg)
		attrs := metric.WithAttributeSet(
			attribute.NewSet(attribute.String("subject", msg.Subject)))
		b.m.ConsumeCount.Add(ctx, 1, attrs)
		b.m.ConsumeLatencyMS.Record(ctx, float64(time.Since(start).Milliseconds()), attrs)
		return err
	}
	return b.Bus.PullSubscribe(subject, consumer, wrapped, opts...)
}

func (b *InstrumentedBus) wrapHandler(subject string, handler Handler) Handler {
	return func(ctx context.Context, subj string, data []byte) error {
		start := time.Now()
		err := handler(ctx, subj, data)
		attrs := metric.WithAttributeSet(
			attribute.NewSet(attribute.String("subject", subj)))
		b.m.ConsumeCount.Add(ctx, 1, attrs)
		b.m.ConsumeLatencyMS.Record(ctx, float64(time.Since(start).Milliseconds()), attrs)
		return err
	}
}
