package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// InjectTraceContext serializes trace context from ctx into a string map
// suitable for NATS message headers.
func InjectTraceContext(ctx context.Context) map[string]string {
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	return carrier
}

// ExtractTraceContext deserializes trace context from a string map
// (e.g. NATS message headers) into a context.
func ExtractTraceContext(ctx context.Context, headers map[string]string) context.Context {
	if len(headers) == 0 {
		return ctx
	}
	carrier := propagation.MapCarrier(headers)
	return otel.GetTextMapPropagator().Extract(ctx, carrier)
}
