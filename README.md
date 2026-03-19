# mqbridge

A message bridge between [RabbitMQ](https://www.rabbitmq.com/) and [SimpleMQ (Sakura Cloud)](https://manual.sakura.ad.jp/cloud/appliance/simplemq/index.html). Define multiple forwarding rules (bridges) in a configuration file and run them concurrently.

## Table of Contents

- [Overview](#overview) / [Features](#features)
- [Message Delivery](#message-delivery)
- [Installation](#installation) / [Usage](#usage)
- [Configuration](#configuration) — [Per-bridge URL Override](#per-bridge-url-override) / [Secret Manager](#secret-manager-integration)
- [Message Format](#message-format)
- [High Availability](#high-availability)
- [Observability](#observability) — [Metrics](#metrics) / [Tracing](#tracing)

## Overview

```
                         mqbridge
              ┌─────────────────────────┐
              │                         │
              │   ┌───────────────┐     │
 RabbitMQ ────┼──►│  Bridge (1:N) │──┬──┼────► SimpleMQ
   queue      │   └───────────────┘  │  │       queue
              │                      └──┼────► SimpleMQ
              │                         │       queue
              │   ┌───────────────┐     │
 SimpleMQ ────┼──►│  Bridge (1:1) │────-┼────► RabbitMQ
   queue      │   └───────────────┘     │    exchange/routing_key
              │                         │
              │   ┌───────────────┐     │
 SimpleMQ ────┼──►│  Bridge (1:N) │──┬──┼────► SimpleMQ
   queue      │   └───────────────┘  │  │       queue
              │                      └──┼────► SimpleMQ
              │                         │       queue
              │          ...            │
              └─────────────────────────┘
                    (concurrent bridges)
```

Each bridge has one **subscriber** (source) and one or more **publishers** (destinations). Multiple bridges run concurrently within a single mqbridge process.

## Features

- **RabbitMQ → SimpleMQ**: Consume from a RabbitMQ queue and forward messages to one or more SimpleMQ queues (fan-out).
- **SimpleMQ → RabbitMQ**: Poll a SimpleMQ queue and publish messages to RabbitMQ with exchange/routing key determined by message content.
- **SimpleMQ → SimpleMQ**: Poll a SimpleMQ queue and forward messages to one or more SimpleMQ queues (fan-out). Useful as a fan-out mechanism since SimpleMQ has no native fan-out support.
- **RabbitMQ → RabbitMQ**: Not supported. Use RabbitMQ's built-in features such as [exchange bindings](https://www.rabbitmq.com/docs/e2e) or the [shovel plugin](https://www.rabbitmq.com/docs/shovel) instead.
- **Automatic reconnection**: RabbitMQ subscriber and publisher automatically reconnect with exponential backoff (1s–30s) on connection loss.
- **Graceful shutdown**: On SIGTERM/SIGINT, waits for in-flight messages to complete before exiting.
- **Named bridges**: Optional `name` field per bridge for readable log output.
- **OpenTelemetry metrics and tracing**: Built-in metrics (received, published, errors, duration) and distributed tracing auto-enabled via `OTEL_EXPORTER_OTLP_ENDPOINT`. Trace context is propagated through messages using [W3C Trace Context](https://www.w3.org/TR/trace-context/) (`traceparent` header).
- **Structured logging with trace correlation**: Text (colored) or JSON format with configurable log level. When tracing is active, `trace_id` and `span_id` are automatically included in log output.
- **Jsonnet configuration**: Use [jsonnet-armed](https://github.com/fujiwara/jsonnet-armed) for configuration with environment variable support (`env()`, `must_env()`).
- **Secret Manager integration**: Retrieve credentials from [Sakura Cloud Secret Manager](https://manual.sakura.ad.jp/cloud/appliance/secretsmanager/index.html) using `secret()` native function in Jsonnet.

## Message Delivery

mqbridge provides **at-least-once** delivery semantics. Messages are acknowledged to the source only after all destinations have been published to successfully.

When a bridge has multiple destinations (fan-out), publishes are performed **sequentially**. If a publish to one destination fails, the remaining destinations are skipped, and the message is returned to the source queue for redelivery. This means destinations that were already published to before the failure will receive **duplicate** messages on retry. Consumers should handle messages idempotently.

## Installation

### Homebrew

```console
$ brew install fujiwara/tap/mqbridge
```

### Binary releases

Download the latest binary from [GitHub Releases](https://github.com/fujiwara/mqbridge/releases).

### Go install

```console
$ go install github.com/fujiwara/mqbridge/cmd/mqbridge@latest
```

## Usage

```
Usage: mqbridge <command> [flags]

Commands:
  run         Run the bridge
  validate    Validate config (unknown fields cause error)
  render      Render config as JSON to stdout

Flags:
  --config, -c    Config file path (Jsonnet/JSON) (required) [$MQBRIDGE_CONFIG]
  --log-format    Log format: text (default, colored with source) or json [$MQBRIDGE_LOG_FORMAT]
  --log-level     Log level: debug, info (default), warn, error [$MQBRIDGE_LOG_LEVEL]
  --version       Show version
  --help          Show help
```

## Configuration

Configuration is written in Jsonnet (plain JSON is also accepted).

```jsonnet
local must_env = std.native('must_env');
{
  rabbitmq: {
    // AMQP URI: amqp://user:pass@host:port/vhost
    // See https://www.rabbitmq.com/docs/uri-spec
    url: must_env('RABBITMQ_URL'),
  },
  simplemq: {
    api_url: 'http://localhost:18080',  // optional, default: official endpoint
  },
  bridges: [
    {
      name: 'rmq-to-smq',  // optional, used in log output (defaults to bridge index)
      // RabbitMQ → SimpleMQ (fan-out)
      from: {
        rabbitmq: {
          queue: 'source-queue',
          exchange: 'source-exchange',
          exchange_type: 'topic',     // direct, fanout, topic, headers
          routing_key: '#',
          exchange_passive: false,    // true to verify exchange exists without declaring
        },
      },
      to: [
        { simplemq: { queue: 'dest-queue-1', api_key: must_env('SIMPLEMQ_API_KEY_1') } },
        { simplemq: { queue: 'dest-queue-2', api_key: must_env('SIMPLEMQ_API_KEY_2') } },
      ],
    },
    {
      name: 'smq-to-rmq',
      // SimpleMQ → RabbitMQ (routing by message content)
      from: {
        simplemq: {
          queue: 'inbound-queue',
          api_key: must_env('SIMPLEMQ_API_KEY_INBOUND'),
          polling_interval: '1s',  // default: 1s
        },
      },
      to: [
        { rabbitmq: {} },  // destination determined by message JSON
      ],
    },
    {
      name: 'smq-to-smq',
      // SimpleMQ → SimpleMQ (fan-out)
      from: {
        simplemq: {
          queue: 'source-queue',
          api_key: must_env('SIMPLEMQ_API_KEY_SOURCE'),
          polling_interval: '1s',
        },
      },
      to: [
        { simplemq: { queue: 'fanout-queue-1', api_key: must_env('SIMPLEMQ_API_KEY_FANOUT_1') } },
        { simplemq: { queue: 'fanout-queue-2', api_key: must_env('SIMPLEMQ_API_KEY_FANOUT_2') } },
      ],
    },
  ],
}
```

### Per-bridge URL Override

By default, all bridges share the global `rabbitmq.url` and `simplemq.api_url` settings. You can override these per bridge by specifying `url` or `api_url` directly in each bridge's `from` / `to` block. If a per-bridge URL is set, it takes precedence over the global setting.

```jsonnet
{
  rabbitmq: {
    url: 'amqp://default-host:5672/',  // global default
  },
  bridges: [
    {
      name: 'bridge-to-other-host',
      from: {
        rabbitmq: {
          url: 'amqp://other-host:5672/',  // overrides global
          queue: 'source-queue',
          exchange: 'source-exchange',
        },
      },
      to: [
        { simplemq: { queue: 'dest-queue', api_key: 'key' } },
      ],
    },
    {
      name: 'bridge-using-global',
      from: {
        rabbitmq: {
          // no url: inherits amqp://default-host:5672/ from global
          queue: 'another-queue',
          exchange: 'another-exchange',
        },
      },
      to: [
        { simplemq: { queue: 'dest-queue-2', api_key: 'key2' } },
      ],
    },
  ],
}
```

### Configuration Notes

- `rabbitmq.url` is required for each bridge that uses RabbitMQ (either from global setting or per-bridge override).
- RabbitMQ source requires SimpleMQ destinations. SimpleMQ source supports both RabbitMQ and SimpleMQ destinations. RabbitMQ → RabbitMQ bridging is not supported; use RabbitMQ's built-in features such as [exchange bindings](https://www.rabbitmq.com/docs/e2e) or the [shovel plugin](https://www.rabbitmq.com/docs/shovel) instead.
- `exchange_type` defaults to `direct` if omitted.
- `exchange_passive` (default: `false`): When `true`, the subscriber uses `ExchangeDeclarePassive` instead of `ExchangeDeclare`. This verifies the exchange exists without attempting to create it or modify its properties. Useful when the exchange is managed externally and its properties (type, durable, etc.) may differ from what mqbridge would declare, avoiding `PRECONDITION_FAILED` errors.
- `routing_key` defaults to `#` if omitted.
- `polling_interval` defaults to `1s`. Invalid values silently fall back to `1s`.

### Secret Manager Integration

You can use `secret()` native function to retrieve credentials from Sakura Cloud Secret Manager. This requires `SAKURA_ACCESS_TOKEN` and `SAKURA_ACCESS_TOKEN_SECRET` environment variables.

```
local secret = std.native('secret');
secret('vault-id', 'name')      // latest version
secret('vault-id', 'name:1')    // specific version
```

```jsonnet
local must_env = std.native('must_env');
local secret = std.native('secret');
{
  rabbitmq: {
    url: must_env('RABBITMQ_URL'),
  },
  bridges: [
    {
      from: {
        rabbitmq: {
          queue: 'source-queue',
          exchange: 'source-exchange',
          exchange_type: 'topic',
          routing_key: '#',
        },
      },
      to: [
        {
          simplemq: {
            queue: 'dest-queue',
            api_key: secret('vault-id-xxx', 'simplemq-api-key'),
          },
        },
      ],
    },
  ],
}
```

### Message Format

mqbridge uses a structured [`Message`](https://pkg.go.dev/github.com/fujiwara/mqbridge#Message) type internally, consisting of `Body` (raw bytes) and `Headers` (string key-value pairs). This allows metadata to flow through the bridge without loss.

#### Wire format on SimpleMQ

Messages stored in SimpleMQ are base64-encoded JSON:

```json
{
  "headers": {
    "rabbitmq.exchange": "my-exchange",
    "rabbitmq.routing_key": "my.routing.key",
    "rabbitmq.header.x-custom": "value"
  },
  "body": "message body as UTF-8 string"
}
```

- `headers` is optional. When absent, the message has no metadata.
- `body` is the message body. For UTF-8 text, it is stored as a plain string. For binary data (non-UTF-8), it is automatically base64-encoded.
- `body_encoding` (auto-set): When `body` is base64-encoded, this field is set to `"base64"` so the receiver can decode it. No configuration is needed — encoding is determined automatically based on whether the body is valid UTF-8.

For backward compatibility, if the base64-decoded content is not valid JSON in this format, it is treated as a plain body with no headers.

#### RabbitMQ → SimpleMQ

When consuming from RabbitMQ, the subscriber captures delivery metadata into `Message.Headers` with `rabbitmq.*` prefixed keys:

| Header key | AMQP field |
|------------|------------|
| `rabbitmq.exchange` | Exchange |
| `rabbitmq.routing_key` | RoutingKey |
| `rabbitmq.reply_to` | ReplyTo |
| `rabbitmq.correlation_id` | CorrelationId |
| `rabbitmq.content_type` | ContentType |
| `rabbitmq.message_id` | MessageId |
| `rabbitmq.header.<key>` | Custom AMQP headers |

This means RPC-related fields (`reply_to`, `correlation_id`) are preserved when forwarding to SimpleMQ, enabling request-reply patterns across the bridge.

#### SimpleMQ → RabbitMQ

When publishing to RabbitMQ, the publisher reads the destination from `Message.Headers`:

- `rabbitmq.exchange` (required key) — target exchange; may be the empty string (`""`) to use the AMQP default exchange
- `rabbitmq.routing_key` (required key) — routing key; may be empty (e.g. for fanout exchanges)
- `rabbitmq.reply_to`, `rabbitmq.correlation_id`, `rabbitmq.content_type`, `rabbitmq.message_id` — mapped to the corresponding AMQP fields
- `rabbitmq.header.<key>` — published as custom AMQP headers (prefix stripped)

External producers sending messages to a SimpleMQ queue for RabbitMQ delivery must use the wire format above. The `rabbitmq.exchange` and `rabbitmq.routing_key` header keys must be present; to target the default exchange, set `"rabbitmq.exchange": ""` in the headers.

#### SimpleMQ → SimpleMQ

Messages are forwarded as-is (re-serialized in the same wire format). No RabbitMQ-specific headers are added unless the original message already contained them.

#### Programmatic Message Construction

You can use [`mqbridge.MarshalMessage()`](https://pkg.go.dev/github.com/fujiwara/mqbridge#MarshalMessage) and [`mqbridge.UnmarshalMessage()`](https://pkg.go.dev/github.com/fujiwara/mqbridge#UnmarshalMessage) to construct or parse messages programmatically. For messages destined for RabbitMQ, use [`msg.ValidateForRabbitMQ()`](https://pkg.go.dev/github.com/fujiwara/mqbridge#Message.ValidateForRabbitMQ) to verify that the required headers are present before sending.

```go
msg := &mqbridge.Message{
    Body: []byte("hello"),
    Headers: map[string]string{
        mqbridge.HeaderRabbitMQExchange:   "my-exchange",
        mqbridge.HeaderRabbitMQRoutingKey: "my.key",
    },
}
if err := msg.ValidateForRabbitMQ(); err != nil {
    log.Fatal(err) // rabbitmq.exchange or rabbitmq.routing_key is missing
}
data, _ := mqbridge.MarshalMessage(msg)
// send data to SimpleMQ queue (base64-encoded)
```

Messages that fail validation at the bridge are logged and dropped (not retried), and counted in the `mqbridge.messages.dropped` metric.

## High Availability

You can run multiple mqbridge instances with the same configuration to achieve high availability. When multiple instances subscribe to the same RabbitMQ queue, RabbitMQ distributes messages across consumers using round-robin ([competing consumers pattern](https://www.rabbitmq.com/tutorials/tutorial-two-go#round-robin-dispatching)). Each message is delivered to exactly one instance — no duplication occurs.

```
              +------------+
              | mqbridge   |---+
              | instance 1 |   |
        +-----+            |   |   +------------+
        |     +------------+   +-->| SimpleMQ   |
 RabbitMQ                      |   | dest queue |
  queue-+                      +-->|            |
        |     +------------+   |   +------------+
        +-----+ mqbridge   |---+
              | instance 2 |
              +------------+
```

- No configuration changes are needed — just run multiple instances with the same config.
- If one instance goes down, unacknowledged messages are automatically redelivered to the remaining instances.
- Manual acknowledgement (`auto-ack: false`) ensures messages are not lost on instance failure.

## Observability

mqbridge supports [OpenTelemetry](https://opentelemetry.io/) metrics and distributed tracing. Both are auto-enabled when the `OTEL_EXPORTER_OTLP_ENDPOINT` environment variable is set. When not set, they are disabled with zero overhead.

```console
$ OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 mqbridge run -c config.jsonnet
```

Both HTTP and gRPC protocols are supported. Set `OTEL_EXPORTER_OTLP_PROTOCOL` to `grpc` for gRPC transport (default: `http/protobuf`). All standard `OTEL_*` environment variables are supported.

### Metrics

The following metrics are exported:

| Metric | Type | Description | Attributes |
|--------|------|-------------|------------|
| `mqbridge.messages.received` | Counter | Messages received from subscriber | `bridge`, `source_type`, `source_queue` |
| `mqbridge.messages.published` | Counter | Messages published to destination | `bridge`, `destination_type`, `destination_queue` |
| `mqbridge.messages.errors` | Counter | Message processing errors (retriable) | `bridge`, `source_type`, `source_queue` |
| `mqbridge.messages.dropped` | Counter | Messages dropped due to unrecoverable content errors | `bridge`, `source_type`, `source_queue` |
| `mqbridge.message.processing.duration` | Histogram | Time to publish to all destinations (seconds) | `bridge`, `source_type`, `source_queue` |

`processing.duration` measures only the publish phase (from after the message is received to after all destinations have been published to). It does not include subscriber wait time.

Attribute values are derived from the bridge configuration and message content. `bridge` is the bridge name (or index if unnamed). `source_type` / `destination_type` is `rabbitmq` or `simplemq`. `source_queue` is the source queue name. `destination_queue` is the SimpleMQ queue name, or the exchange name for RabbitMQ (from each message).

### Tracing

mqbridge creates a span (`mqbridge.bridge`) for each message processed through a bridge. Trace context is propagated using [W3C Trace Context](https://www.w3.org/TR/trace-context/) format.

**Trace context extraction** (incoming messages):
1. Checks for `traceparent` header in the message
2. Falls back to `rabbitmq.header.traceparent` (set when RabbitMQ messages carry trace context in custom AMQP headers)

**Trace context injection** (outgoing messages):
- `traceparent` (and `tracestate` if present) are injected into message headers before publishing

This is compatible with [simplemq-subscriber](https://github.com/fujiwara/simplemq-subscriber), which uses the same trace context propagation format.

### Log Trace Correlation

When tracing is active, `trace_id` and `span_id` from the current span context are automatically added to all log records that have a span in their context. This works with both text and JSON log formats.

## LICENSE

MIT

## Author

fujiwara
