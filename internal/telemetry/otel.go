package telemetry

import (
	"context"
	"fmt"
	"log/slog"
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

// SetupOptions configures the OTel bootstrap. ServiceName is required
// for multi-replica deployments to avoid metric series collision: the
// SDK's default resource provides a unique service.instance.id that,
// combined with ServiceName, gives every replica a distinct label set
// on the Prometheus side.
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
// The resource merges the SDK default (which provides service.instance.id,
// host.name, and telemetry.sdk.*) with the caller-supplied service name,
// version, and environment. This prevents multiple replicas of the same
// service from emitting identical label sets and colliding into a single
// Prometheus series.
func Setup(ctx context.Context, opts SetupOptions) (*Telemetry, error) {
	if opts.ServiceName == "" {
		return nil, fmt.Errorf("telemetry: SetupOptions.ServiceName is required")
	}

	// Surface OTEL SDK errors (export failures, etc.) via slog instead
	// of silently dropping them.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		slog.Warn("otel export error", "error", err)
	}))

	attrs := []attribute.KeyValue{
		semconv.ServiceName(opts.ServiceName),
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
