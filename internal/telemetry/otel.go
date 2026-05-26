package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
)

// SetupOptions configures the OTel bootstrap. ServiceName plus a per-replica
// service.instance.id are what keep multiple replicas of the same service from
// emitting identical label sets and colliding into one Prometheus series. See
// resolveInstanceID for how the instance id is chosen.
type SetupOptions struct {
	// OTLPEndpoint is the collector's gRPC address (host:port). When
	// empty, telemetry is collected in-process but not exported.
	OTLPEndpoint string
	// ServiceName populates the service.name resource attribute, e.g.
	// "yipyap-checker". Required: an empty value causes Setup to return
	// an error.
	ServiceName string
	// ServiceVersion populates service.version (often set from a
	// main.version ldflag). Optional.
	ServiceVersion string
	// Environment populates deployment.environment (e.g. "prod",
	// "staging"). Optional.
	Environment string
	// InstanceID overrides service.instance.id. Optional; when empty it is
	// resolved from the hostname, falling back to a random id. Set it only
	// when the hostname is not unique per replica.
	InstanceID string
}

type Telemetry struct {
	provider      *sdkmetric.MeterProvider
	traceProvider *sdktrace.TracerProvider
	Meter         metric.Meter
	Tracer        trace.Tracer
}

// Setup initialises OTLP metrics and tracing. The returned Telemetry
// must be shut down with (*Telemetry).Shutdown during process exit.
//
// The resource carries service.name plus a per-replica service.instance.id, so
// multiple replicas of the same service stay on distinct Prometheus series.
func Setup(ctx context.Context, opts SetupOptions) (*Telemetry, error) {
	if opts.ServiceName == "" {
		return nil, fmt.Errorf("telemetry: SetupOptions.ServiceName is required")
	}

	// Surface OTEL SDK errors (export failures, etc.) via slog instead
	// of silently dropping them.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		slog.Warn("otel export error", "error", err)
	}))

	res, err := newResource(opts)
	if err != nil {
		return nil, err
	}

	metricOpts := []sdkmetric.Option{sdkmetric.WithResource(res)}
	traceOpts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}

	if opts.OTLPEndpoint != "" {
		metricExp, err := otlpmetricgrpc.New(ctx,
			otlpmetricgrpc.WithEndpoint(opts.OTLPEndpoint),
			otlpmetricgrpc.WithInsecure(), // TODO: configurable TLS
		)
		if err != nil {
			return nil, err
		}
		metricOpts = append(metricOpts, sdkmetric.WithReader(
			sdkmetric.NewPeriodicReader(metricExp, sdkmetric.WithInterval(15*time.Second)),
		))

		traceExp, err := otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(opts.OTLPEndpoint),
			otlptracegrpc.WithInsecure(), // TODO: configurable TLS
		)
		if err != nil {
			return nil, err
		}
		traceOpts = append(traceOpts, sdktrace.WithBatcher(traceExp))
	}

	provider := sdkmetric.NewMeterProvider(metricOpts...)
	otel.SetMeterProvider(provider)

	tp := sdktrace.NewTracerProvider(traceOpts...)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	meter := provider.Meter("github.com/YipYap-run/YipYap-FOSS")
	tracer := tp.Tracer("github.com/YipYap-run/YipYap-FOSS")

	return &Telemetry{
		provider:      provider,
		traceProvider: tp,
		Meter:         meter,
		Tracer:        tracer,
	}, nil
}

func (t *Telemetry) Shutdown(ctx context.Context) error {
	if err := t.traceProvider.Shutdown(ctx); err != nil {
		return err
	}
	return t.provider.Shutdown(ctx)
}

// newResource builds the OTel resource for both metrics and traces. It sets
// service.name and an explicit service.instance.id on top of resource.Default()
// (telemetry.sdk.*). The instance id matters: resource.Default() only sets
// service.instance.id when the experimental OTEL_GO_X_RESOURCE gate is on, so
// without setting it here every replica of a service shares one label set and
// the Prometheus series collide.
func newResource(opts SetupOptions) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		semconv.ServiceName(opts.ServiceName),
		semconv.ServiceInstanceID(resolveInstanceID(opts.InstanceID)),
	}
	if opts.ServiceVersion != "" {
		attrs = append(attrs, semconv.ServiceVersion(opts.ServiceVersion))
	}
	if opts.Environment != "" {
		attrs = append(attrs, semconv.DeploymentEnvironmentName(opts.Environment))
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL, attrs...),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: resource merge: %w", err)
	}
	return res, nil
}

// resolveInstanceID returns a value for service.instance.id that is unique per
// running replica. Preference: an explicit override, then the container/pod
// hostname (unique per replica in Compose and Kubernetes), then a random id.
func resolveInstanceID(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err == nil {
		return hex.EncodeToString(b)
	}
	return "unknown"
}
