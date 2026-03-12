package mqbridge

import (
	"context"
	"maps"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/fujiwara/mqbridge"

// headerCarrier adapts map[string]string for OTel propagation.
type headerCarrier map[string]string

func (c headerCarrier) Get(key string) string { return c[key] }
func (c headerCarrier) Set(key, val string)   { c[key] = val }
func (c headerCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

const (
	// W3C Trace Context header keys
	headerTraceparent = "traceparent"
	headerTracestate  = "tracestate"

	// Fallback: RabbitMQ custom header prefix used by mqbridge
	headerRMQTraceparent = "rabbitmq.header.traceparent"
	headerRMQTracestate  = "rabbitmq.header.tracestate"
)

// extractTraceContext extracts trace context from message headers.
// Checks top-level traceparent first, falls back to rabbitmq.header.traceparent.
func extractTraceContext(ctx context.Context, headers map[string]string) context.Context {
	if headers == nil {
		return ctx
	}
	carrier := make(headerCarrier)
	// Try top-level first
	if v, ok := headers[headerTraceparent]; ok {
		carrier[headerTraceparent] = v
		if v, ok := headers[headerTracestate]; ok {
			carrier[headerTracestate] = v
		}
	} else if v, ok := headers[headerRMQTraceparent]; ok {
		// Fallback to rabbitmq.header.* prefix
		carrier[headerTraceparent] = v
		if v, ok := headers[headerRMQTracestate]; ok {
			carrier[headerTracestate] = v
		}
	}
	if carrier[headerTraceparent] == "" {
		return ctx
	}
	prop := propagation.TraceContext{}
	return prop.Extract(ctx, carrier)
}

// injectTraceContext injects trace context into message headers.
func injectTraceContext(ctx context.Context, headers map[string]string) {
	if headers == nil {
		return
	}
	carrier := make(headerCarrier)
	prop := propagation.TraceContext{}
	prop.Inject(ctx, carrier)
	maps.Copy(headers, carrier)
}

func newTracer() trace.Tracer {
	return otel.Tracer(tracerName)
}
