package mqbridge

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel"
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
		return nil, err
	}

	published, err := meter.Int64Counter("mqbridge.messages.published",
		metric.WithDescription("Messages published to destination"),
	)
	if err != nil {
		return nil, err
	}

	errors, err := meter.Int64Counter("mqbridge.messages.errors",
		metric.WithDescription("Message processing errors"),
	)
	if err != nil {
		return nil, err
	}

	duration, err := meter.Float64Histogram("mqbridge.message.processing.duration",
		metric.WithDescription("Time from receive to all publishes done (seconds)"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
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

	exporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return noop, err
	}

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
	)
	otel.SetMeterProvider(provider)

	slog.Info("OpenTelemetry metrics enabled")
	return provider.Shutdown, nil
}
