package mqbridge

import (
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
)

// NewBridgeForTest creates a Bridge with the given subscriber, publishers, and metric attributes.
// This is intended for testing only.
func NewBridgeForTest(sub Subscriber, pubs []Publisher, srcType, srcQueue string) *Bridge {
	m, _ := newMetrics()
	srcAttrs := attribute.NewSet(
		attribute.String("bridge", "test"),
		attribute.String("source_type", srcType),
		attribute.String("source_queue", srcQueue),
	)
	return &Bridge{
		From:       sub,
		To:         pubs,
		metrics:    m,
		srcAttrs:   srcAttrs,
		srcType:    srcType,
		srcQueue:   srcQueue,
		bridgeName: "test",
		logger:     slog.Default().With("bridge", "test"),
	}
}
