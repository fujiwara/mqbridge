package mqbridge_test

import (
	"testing"

	"github.com/fujiwara/mqbridge"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func setupTestTracer(t *testing.T) trace.Tracer {
	t.Helper()
	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { tp.Shutdown(t.Context()) })
	return tp.Tracer("test")
}

func TestTraceContextPropagation(t *testing.T) {
	tracer := setupTestTracer(t)

	// Start a span to create trace context
	ctx, span := tracer.Start(t.Context(), "test-span")
	defer span.End()

	sc := trace.SpanContextFromContext(ctx)
	if !sc.HasTraceID() {
		t.Fatal("expected span context to have trace ID")
	}

	// Inject trace context into message headers
	msg := &mqbridge.Message{
		Body: []byte("test"),
		Headers: map[string]string{
			mqbridge.HeaderRabbitMQExchange:   "ex",
			mqbridge.HeaderRabbitMQRoutingKey: "rk",
		},
	}
	prop := propagation.TraceContext{}
	carrier := propagation.MapCarrier(msg.Headers)
	prop.Inject(ctx, carrier)

	if msg.Headers["traceparent"] == "" {
		t.Fatal("expected traceparent header to be set")
	}
	t.Logf("traceparent: %s", msg.Headers["traceparent"])
}

func TestTraceContextRoundTrip(t *testing.T) {
	tracer := setupTestTracer(t)

	// Create a span and serialize its trace context
	ctx, span := tracer.Start(t.Context(), "original-span")
	originalSC := trace.SpanContextFromContext(ctx)
	span.End()

	// Inject into headers using W3C propagation
	headers := map[string]string{}
	prop := propagation.TraceContext{}
	prop.Inject(ctx, propagation.MapCarrier(headers))
	traceparent := headers["traceparent"]

	t.Run("top-level traceparent", func(t *testing.T) {
		msg := &mqbridge.Message{
			Body: []byte("test"),
			Headers: map[string]string{
				"traceparent": traceparent,
			},
		}
		data, err := mqbridge.MarshalMessage(msg)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}
		restored := mqbridge.UnmarshalMessage(data)
		if restored.Headers["traceparent"] != traceparent {
			t.Errorf("traceparent: expected %q, got %q", traceparent, restored.Headers["traceparent"])
		}

		// Verify trace ID is preserved through extract
		extractedCtx := prop.Extract(t.Context(), propagation.MapCarrier(restored.Headers))
		extractedSC := trace.SpanContextFromContext(extractedCtx)
		if extractedSC.TraceID() != originalSC.TraceID() {
			t.Errorf("trace ID: expected %s, got %s", originalSC.TraceID(), extractedSC.TraceID())
		}
	})

	t.Run("rabbitmq.header.traceparent fallback", func(t *testing.T) {
		msg := &mqbridge.Message{
			Body: []byte("test"),
			Headers: map[string]string{
				"rabbitmq.header.traceparent": traceparent,
			},
		}
		data, err := mqbridge.MarshalMessage(msg)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}
		restored := mqbridge.UnmarshalMessage(data)
		if restored.Headers["rabbitmq.header.traceparent"] != traceparent {
			t.Errorf("traceparent: expected %q, got %q", traceparent, restored.Headers["rabbitmq.header.traceparent"])
		}
	})
}
