package mqbridge

import (
	"context"
	"log/slog"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// NewTraceHandlerForTest exposes newTraceHandler for testing.
func NewTraceHandlerForTest(h slog.Handler) slog.Handler {
	return newTraceHandler(h)
}

// SetupOTelProvidersForTest exposes setupOTelProviders for testing.
func SetupOTelProvidersForTest(ctx context.Context) (func(context.Context) error, error) {
	return setupOTelProviders(ctx)
}

// SetupLoggerForTest exposes setupLogger for testing.
func SetupLoggerForTest(format, level string) {
	setupLogger(format, level)
}

// MessageFromDeliveryForTest exposes messageFromDelivery for testing.
func MessageFromDeliveryForTest(d amqp.Delivery) *Message {
	return messageFromDelivery(d)
}

// NewBridgeForTest creates a Bridge with the given subscriber, publishers, and metric attributes.
// This is intended for testing only.
func NewBridgeForTest(ctx context.Context, sub Subscriber, pubs []Publisher, srcType, srcQueue string) *Bridge {
	m, _ := newMetrics()
	srcAttrs := attribute.NewSet(
		attribute.String("bridge", "test"),
		attribute.String("source_type", srcType),
		attribute.String("source_queue", srcQueue),
	)
	b := &Bridge{
		From:       sub,
		To:         pubs,
		metrics:    m,
		tracer:     newTracer(),
		srcAttrs:   srcAttrs,
		srcType:    srcType,
		srcQueue:   srcQueue,
		bridgeName: "test",
		logger:     slog.Default().With("bridge", "test"),
	}
	b.metrics.initCounters(ctx, metric.WithAttributeSet(srcAttrs))
	return b
}
