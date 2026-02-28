package mqbridge

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// Metrics holds OpenTelemetry metric instruments for mqbridge.
type Metrics struct {
	messagesReceived   metric.Int64Counter
	messagesPublished  metric.Int64Counter
	messageErrors      metric.Int64Counter
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
		processingDuration: duration,
	}, nil
}

// setupMeterProvider initializes the OpenTelemetry MeterProvider if OTEL_EXPORTER_OTLP_ENDPOINT is set.
// Returns a shutdown function and any error. If the endpoint is not set, returns a no-op shutdown.
func setupMeterProvider(ctx context.Context) (func(context.Context) error, error) {
	noop := func(context.Context) error { return nil }

	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return noop, nil
	}

	protocol := os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")
	var exporter sdkmetric.Exporter
	var err error
	switch protocol {
	case "grpc":
		exporter, err = otlpmetricgrpc.New(ctx)
	case "http/protobuf", "":
		exporter, err = otlpmetrichttp.New(ctx)
	default:
		return noop, fmt.Errorf("unsupported OTEL_EXPORTER_OTLP_PROTOCOL: %s", protocol)
	}
	if err != nil {
		return noop, fmt.Errorf("failed to create OTLP metrics exporter: %w", err)
	}

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
	)
	otel.SetMeterProvider(provider)

	if protocol == "" {
		protocol = "http/protobuf"
	}
	slog.Info("OpenTelemetry metrics enabled", "protocol", protocol)
	return provider.Shutdown, nil
}
