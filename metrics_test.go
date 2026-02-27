package mqbridge_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/fujiwara/mqbridge"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// mockSubscriber calls the handler once per message, then blocks until context is cancelled.
type mockSubscriber struct {
	messages [][]byte
}

func (s *mockSubscriber) Subscribe(ctx context.Context, handler func(ctx context.Context, msg []byte) error) error {
	for _, msg := range s.messages {
		if err := handler(ctx, msg); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return nil
}

func (s *mockSubscriber) Close() error { return nil }

// mockPublisher records published messages.
type mockPublisher struct {
	messages [][]byte
}

func (p *mockPublisher) Publish(_ context.Context, msg []byte) error {
	p.messages = append(p.messages, msg)
	return nil
}

func (p *mockPublisher) Close() error { return nil }

// failingPublisher always returns an error.
type failingPublisher struct{}

func (p *failingPublisher) Publish(_ context.Context, _ []byte) error {
	return fmt.Errorf("publish failed")
}

func (p *failingPublisher) Close() error { return nil }

func setupTestMeterProvider(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(provider)
	t.Cleanup(func() {
		provider.Shutdown(t.Context())
		// Reset to default noop provider
		otel.SetMeterProvider(nil)
	})
	return reader
}

func findMetric(rm metricdata.ResourceMetrics, name string) *metricdata.Metrics {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return &m
			}
		}
	}
	return nil
}

func sumInt64(m *metricdata.Metrics) int64 {
	if m == nil {
		return 0
	}
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		return 0
	}
	var total int64
	for _, dp := range sum.DataPoints {
		total += dp.Value
	}
	return total
}

func int64DataPointsWithAttrs(m *metricdata.Metrics, attrs attribute.Set) int64 {
	if m == nil {
		return 0
	}
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		return 0
	}
	var total int64
	for _, dp := range sum.DataPoints {
		if dp.Attributes.Equals(&attrs) {
			total += dp.Value
		}
	}
	return total
}

func histogramCount(m *metricdata.Metrics) uint64 {
	if m == nil {
		return 0
	}
	hist, ok := m.Data.(metricdata.Histogram[float64])
	if !ok {
		return 0
	}
	var total uint64
	for _, dp := range hist.DataPoints {
		total += dp.Count
	}
	return total
}

func TestMetricsSingleDestination(t *testing.T) {
	reader := setupTestMeterProvider(t)

	bridge := mqbridge.NewBridgeForTest(
		&mockSubscriber{messages: [][]byte{[]byte("msg1"), []byte("msg2"), []byte("msg3")}},
		[]mqbridge.Publisher{&mockPublisher{}},
		"rabbitmq", "test-queue",
		[]mqbridge.DestinationInfo{{Type: "simplemq", Queue: "dest-queue"}},
	)

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() {
		errCh <- bridge.Run(ctx)
	}()

	// Wait for messages to be processed, then cancel
	cancel()
	<-errCh

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}

	received := findMetric(rm, "mqbridge.messages.received")
	if got := sumInt64(received); got != 3 {
		t.Errorf("messages.received: got %d, want 3", got)
	}

	published := findMetric(rm, "mqbridge.messages.published")
	if got := sumInt64(published); got != 3 {
		t.Errorf("messages.published: got %d, want 3", got)
	}

	errors := findMetric(rm, "mqbridge.messages.errors")
	if got := sumInt64(errors); got != 0 {
		t.Errorf("messages.errors: got %d, want 0", got)
	}

	duration := findMetric(rm, "mqbridge.message.processing.duration")
	if got := histogramCount(duration); got != 3 {
		t.Errorf("processing.duration count: got %d, want 3", got)
	}
}

func TestMetricsFanout(t *testing.T) {
	reader := setupTestMeterProvider(t)

	pub1 := &mockPublisher{}
	pub2 := &mockPublisher{}
	pub3 := &mockPublisher{}

	bridge := mqbridge.NewBridgeForTest(
		&mockSubscriber{messages: [][]byte{[]byte("msg1"), []byte("msg2")}},
		[]mqbridge.Publisher{pub1, pub2, pub3},
		"rabbitmq", "src-queue",
		[]mqbridge.DestinationInfo{
			{Type: "simplemq", Queue: "dest-1"},
			{Type: "simplemq", Queue: "dest-2"},
			{Type: "simplemq", Queue: "dest-3"},
		},
	)

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() {
		errCh <- bridge.Run(ctx)
	}()

	cancel()
	<-errCh

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}

	received := findMetric(rm, "mqbridge.messages.received")
	if got := sumInt64(received); got != 2 {
		t.Errorf("messages.received: got %d, want 2", got)
	}

	published := findMetric(rm, "mqbridge.messages.published")
	// 2 messages * 3 destinations = 6
	if got := sumInt64(published); got != 6 {
		t.Errorf("messages.published: got %d, want 6", got)
	}

	// Verify per-destination attributes
	for _, dq := range []string{"dest-1", "dest-2", "dest-3"} {
		attrs := attribute.NewSet(
			attribute.String("destination_type", "simplemq"),
			attribute.String("destination_queue", dq),
		)
		if got := int64DataPointsWithAttrs(published, attrs); got != 2 {
			t.Errorf("messages.published for %s: got %d, want 2", dq, got)
		}
	}

	duration := findMetric(rm, "mqbridge.message.processing.duration")
	if got := histogramCount(duration); got != 2 {
		t.Errorf("processing.duration count: got %d, want 2", got)
	}
}

func TestMetricsPublishError(t *testing.T) {
	reader := setupTestMeterProvider(t)

	bridge := mqbridge.NewBridgeForTest(
		&mockSubscriber{messages: [][]byte{[]byte("msg1")}},
		[]mqbridge.Publisher{&failingPublisher{}},
		"simplemq", "src-queue",
		[]mqbridge.DestinationInfo{{Type: "rabbitmq", Queue: ""}},
	)

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() {
		errCh <- bridge.Run(ctx)
	}()

	err := <-errCh
	cancel()
	if err == nil {
		t.Fatal("expected error from failing publisher")
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}

	received := findMetric(rm, "mqbridge.messages.received")
	if got := sumInt64(received); got != 1 {
		t.Errorf("messages.received: got %d, want 1", got)
	}

	errors := findMetric(rm, "mqbridge.messages.errors")
	if got := sumInt64(errors); got != 1 {
		t.Errorf("messages.errors: got %d, want 1", got)
	}

	published := findMetric(rm, "mqbridge.messages.published")
	if got := sumInt64(published); got != 0 {
		t.Errorf("messages.published: got %d, want 0", got)
	}
}
