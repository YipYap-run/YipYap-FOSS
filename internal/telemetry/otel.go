package telemetry

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type Telemetry struct {
	provider      *sdkmetric.MeterProvider
	traceProvider *sdktrace.TracerProvider
	Meter         metric.Meter
	Tracer        trace.Tracer
}

// Setup initializes OTLP. If endpoint is empty, uses a no-op (metrics collected but not exported).
func Setup(ctx context.Context, endpoint string) (*Telemetry, error) {
	// Surface OTEL SDK errors (export failures, etc.) via slog instead of
	// silently dropping them.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		slog.Warn("otel export error", "error", err)
	}))

	var metricOpts []sdkmetric.Option
	var traceOpts []sdktrace.TracerProviderOption

	if endpoint != "" {
		metricExp, err := otlpmetricgrpc.New(ctx,
			otlpmetricgrpc.WithEndpoint(endpoint),
			otlpmetricgrpc.WithInsecure(), // TODO: configurable TLS
		)
		if err != nil {
			return nil, err
		}
		metricOpts = append(metricOpts, sdkmetric.WithReader(
			sdkmetric.NewPeriodicReader(metricExp, sdkmetric.WithInterval(15*time.Second)),
		))

		traceExp, err := otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(endpoint),
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
