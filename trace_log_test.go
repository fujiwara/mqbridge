package mqbridge_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/fujiwara/mqbridge"
	"go.opentelemetry.io/otel/trace"
)

func TestTraceLogHandler(t *testing.T) {
	tracer := setupTestTracer(t)

	var buf bytes.Buffer
	handler := mqbridge.NewTraceHandlerForTest(
		slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
	)
	logger := slog.New(handler)

	t.Run("with trace context", func(t *testing.T) {
		buf.Reset()
		ctx, span := tracer.Start(t.Context(), "test-span")
		sc := trace.SpanContextFromContext(ctx)

		logger.InfoContext(ctx, "test message")
		span.End()

		var record map[string]any
		if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
			t.Fatalf("failed to parse log: %v, output: %s", err, buf.String())
		}
		if record["trace_id"] != sc.TraceID().String() {
			t.Errorf("trace_id: expected %q, got %q", sc.TraceID().String(), record["trace_id"])
		}
		if record["span_id"] != sc.SpanID().String() {
			t.Errorf("span_id: expected %q, got %q", sc.SpanID().String(), record["span_id"])
		}
	})

	t.Run("without trace context", func(t *testing.T) {
		buf.Reset()
		logger.Info("no trace")

		var record map[string]any
		if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
			t.Fatalf("failed to parse log: %v, output: %s", err, buf.String())
		}
		if _, ok := record["trace_id"]; ok {
			t.Error("expected no trace_id in log without trace context")
		}
		if _, ok := record["span_id"]; ok {
			t.Error("expected no span_id in log without trace context")
		}
	})
}
