package cloudevents

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/telemetry"
)

// subjectPrefix is the root of the CloudEvents egress subject tree. All
// CloudEvents emitted by yipyap internals are published under this prefix
// so that outbound channel adapters, ContainerSource bridges, and CRD
// controllers can subscribe to a single stable tree.
const subjectPrefix = "events.cloudevents.out."

// SubjectFor returns the canonical NATS subject for a CloudEvent emitted by
// the given org with the given type. Consumers use this or a wildcarded
// variant (e.g. "events.cloudevents.out.org-1.>") to subscribe.
func SubjectFor(orgID, eventType string) string {
	return subjectPrefix + orgID + "." + eventType
}

// Publisher wraps a bus.Bus with CloudEvents-aware semantics: subject
// derivation, structured-mode JSON serialization of the full envelope, and
// JetStream-style dedup keyed on the CloudEvent ID.
type Publisher struct {
	bus     bus.Bus
	metrics *telemetry.Metrics
}

// PublisherOption configures a Publisher at construction time.
type PublisherOption func(*Publisher)

// WithPublisherMetrics wires a telemetry.Metrics bundle into the publisher
// so that emit success/failure and latency are recorded. Passing nil is
// allowed and disables metric recording.
func WithPublisherMetrics(m *telemetry.Metrics) PublisherOption {
	return func(p *Publisher) { p.metrics = m }
}

// NewPublisher returns a Publisher that emits CloudEvents onto b.
func NewPublisher(b bus.Bus, opts ...PublisherOption) *Publisher {
	p := &Publisher{bus: b}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	return p
}

// SetMetrics wires (or replaces) the telemetry bundle on an existing
// Publisher. Safe to call with nil to disable recording. Mirrors the
// Emitter setter pattern used elsewhere in the codebase.
func (p *Publisher) SetMetrics(m *telemetry.Metrics) {
	p.metrics = m
}

var tracer = otel.Tracer("github.com/YipYap-run/YipYap-FOSS/internal/cloudevents")

// Publish validates ev, serializes it as a structured-mode CloudEvent
// (application/cloudevents+json), and publishes it durably onto the bus.
// The dedup msgID is the CloudEvent's ID, so redelivery of the same event
// is a no-op on JetStream-backed buses.
func (p *Publisher) Publish(ctx context.Context, orgID string, ev ce.Event) error {
	start := time.Now()

	if err := ev.Validate(); err != nil {
		return fmt.Errorf("cloudevents: invalid event: %w", err)
	}

	subject := SubjectFor(orgID, ev.Type())

	ctx, span := tracer.Start(ctx, "cloudevents.publish")
	span.SetAttributes(
		attribute.String("ce.type", ev.Type()),
		attribute.String("ce.id", ev.ID()),
		attribute.String("org.id", orgID),
		attribute.String("subject", subject),
	)
	defer span.End()

	payload, err := json.Marshal(ev)
	if err != nil {
		span.SetStatus(codes.Error, "marshal failed")
		span.RecordError(err)
		p.recordEmit(ctx, ev.Type(), "failure", start)
		return fmt.Errorf("cloudevents: marshal: %w", err)
	}

	if err := p.bus.PublishDurable(ctx, subject, payload, ev.ID()); err != nil {
		span.SetStatus(codes.Error, "publish failed")
		span.RecordError(err)
		p.recordEmit(ctx, ev.Type(), "failure", start)
		return err
	}
	p.recordEmit(ctx, ev.Type(), "success", start)
	return nil
}

// recordEmit is a nil-safe helper that records the emit-outcome counter and
// emit-latency histogram. When p.metrics is nil (FOSS builds, tests) the
// recording is a no-op.
func (p *Publisher) recordEmit(ctx context.Context, ceType, result string, start time.Time) {
	if p == nil || p.metrics == nil {
		return
	}
	elapsedMS := float64(time.Since(start)) / float64(time.Millisecond)
	if p.metrics.CloudEventsEmitted != nil {
		p.metrics.CloudEventsEmitted.Add(ctx, 1, metric.WithAttributes(
			attribute.String("type", ceType),
			attribute.String("channel", "internal_bus"),
			attribute.String("result", result),
		))
	}
	if p.metrics.CloudEventsEmitLatencyMS != nil {
		p.metrics.CloudEventsEmitLatencyMS.Record(ctx, elapsedMS, metric.WithAttributes(
			attribute.String("type", ceType),
		))
	}
}
