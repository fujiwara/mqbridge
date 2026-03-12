package mqbridge

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Metrics holds OpenTelemetry metric instruments for mqbridge.
type Metrics struct {
	messagesReceived   metric.Int64Counter
	messagesPublished  metric.Int64Counter
	messageErrors      metric.Int64Counter
	messagesDropped    metric.Int64Counter
	processingDuration metric.Float64Histogram
}

func newMetrics() (*Metrics, error) {
	meter := otel.Meter("github.com/fujiwara/mqbridge")

	received, err := meter.Int64Counter("mqbridge.messages.received",
		metric.WithDescription("Messages received from subscriber"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create messages.received counter: %w", err)
	}

	published, err := meter.Int64Counter("mqbridge.messages.published",
		metric.WithDescription("Messages published to destination"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create messages.published counter: %w", err)
	}

	errors, err := meter.Int64Counter("mqbridge.messages.errors",
		metric.WithDescription("Message processing errors"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create messages.errors counter: %w", err)
	}

	dropped, err := meter.Int64Counter("mqbridge.messages.dropped",
		metric.WithDescription("Messages dropped due to unrecoverable content errors"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create messages.dropped counter: %w", err)
	}

	duration, err := meter.Float64Histogram("mqbridge.message.processing.duration",
		metric.WithDescription("Time from receive to all publishes done (seconds)"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create processing.duration histogram: %w", err)
	}

	return &Metrics{
		messagesReceived:   received,
		messagesPublished:  published,
		messageErrors:      errors,
		messagesDropped:    dropped,
		processingDuration: duration,
	}, nil
}

// setupOTelProviders initializes OpenTelemetry MeterProvider and TracerProvider
// if OTEL_EXPORTER_OTLP_ENDPOINT is set.
// Returns a shutdown function and any error. If the endpoint is not set, returns a no-op shutdown.
func setupOTelProviders(ctx context.Context) (func(context.Context) error, error) {
	noop := func(context.Context) error { return nil }

	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return noop, nil
	}

	protocol := os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")

	var metricExporter sdkmetric.Exporter
	var traceExporter sdktrace.SpanExporter
	var err error

	switch protocol {
	case "grpc":
		metricExporter, err = otlpmetricgrpc.New(ctx)
		if err != nil {
			return noop, fmt.Errorf("failed to create OTLP metrics exporter: %w", err)
		}
		traceExporter, err = otlptracegrpc.New(ctx)
		if err != nil {
			return noop, fmt.Errorf("failed to create OTLP trace exporter: %w", err)
		}
	case "http/protobuf", "":
		metricExporter, err = otlpmetrichttp.New(ctx)
		if err != nil {
			return noop, fmt.Errorf("failed to create OTLP metrics exporter: %w", err)
		}
		traceExporter, err = otlptracehttp.New(ctx)
		if err != nil {
			return noop, fmt.Errorf("failed to create OTLP trace exporter: %w", err)
		}
	default:
		return noop, fmt.Errorf("unsupported OTEL_EXPORTER_OTLP_PROTOCOL: %s", protocol)
	}

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("mqbridge"),
	)

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
	)
	otel.SetMeterProvider(meterProvider)

	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(traceExporter),
	)
	otel.SetTracerProvider(tracerProvider)

	if protocol == "" {
		protocol = "http/protobuf"
	}
	slog.Info("OpenTelemetry enabled", "protocol", protocol)

	shutdown := func(ctx context.Context) error {
		if err := tracerProvider.Shutdown(ctx); err != nil {
			slog.Error("failed to shutdown tracer provider", "error", err)
		}
		return meterProvider.Shutdown(ctx)
	}
	return shutdown, nil
}
