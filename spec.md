# mqbridge Specification

## 1. Overview

mqbridge is a message bridge between RabbitMQ and SimpleMQ. It defines multiple forwarding rules (bridges) in a configuration file and runs them concurrently.

## 2. Directions

### 2.1 RabbitMQ → SimpleMQ

- Consume from a RabbitMQ queue and send the message body as-is to a SimpleMQ queue.
- Fan-out from one source to multiple SimpleMQ queues is supported.
- Exchange/queue declaration and binding are performed on the RabbitMQ side.

### 2.2 SimpleMQ → RabbitMQ

- Receive from a SimpleMQ queue via polling.
- The message body must be in the following JSON format:

```json
{
  "exchange": "my-exchange",
  "routing_key": "my.routing.key",
  "headers": { "content-type": "application/json" },
  "body": "actual message content"
}
```

- `exchange` and `routing_key` are required. `headers` is optional.
- The content of `body` is published to RabbitMQ.

## 3. Configuration

- Jsonnet format (plain JSON is also accepted).
- Uses `github.com/fujiwara/jsonnet-armed` (`env()`, `must_env()`, etc. are available).
- `secret(vault_id, name)`: Retrieve a secret value from Sakura Cloud Secret Manager. Requires `SAKURA_ACCESS_TOKEN` and `SAKURA_ACCESS_TOKEN_SECRET` environment variables. The `name` parameter accepts an optional version suffix: `"name"` for latest, `"name:1"` for a specific version.

```jsonnet
{
  rabbitmq: {
    url: std.native('must_env')('RABBITMQ_URL'),
  },
  simplemq: {
    api_url: 'http://localhost:18080',  // optional, for localserver. default: official endpoint
  },
  bridges: [
    {
      // RabbitMQ → SimpleMQ (fan-out)
      from: {
        rabbitmq: {
          queue: 'source-queue',
          exchange: 'source-exchange',
          exchange_type: 'topic',   // direct, fanout, topic, headers
          routing_key: '#',
        },
      },
      to: [
        { simplemq: { queue: 'dest-queue-1', api_key: std.native('must_env')('SIMPLEMQ_API_KEY_1') } },
        { simplemq: { queue: 'dest-queue-2', api_key: std.native('must_env')('SIMPLEMQ_API_KEY_2') } },
      ],
    },
    {
      // SimpleMQ → RabbitMQ (routing by message content)
      from: {
        simplemq: {
          queue: 'inbound-queue',
          api_key: std.native('must_env')('SIMPLEMQ_API_KEY_INBOUND'),
          polling_interval: '1s',  // default: 1s
        },
      },
      to: [
        { rabbitmq: {} },  // destination determined by message JSON
      ],
    },
  ],
}
```

**Note**: SimpleMQ `api_key` differs per queue, so it is specified individually on each simplemq reference (both from and to). Jsonnet variables can be used to avoid duplication:

```jsonnet
local key1 = std.native('must_env')('SIMPLEMQ_API_KEY_1');
// ... reference key1 in multiple places
```

## 4. CLI

Uses `github.com/alecthomas/kong`. Subcommand-based.

```
Usage: mqbridge <command> [flags]

Commands:
  run         Run the bridge
  validate    Validate config (unknown fields cause error)
  render      Render config as JSON to stdout

Flags:
  --config, -c    Config file path (Jsonnet/JSON) (required) [$MQBRIDGE_CONFIG]
  --log-format    Log format: text (default, colored with source) or json [$MQBRIDGE_LOG_FORMAT]
  --version       Show version
  --help          Show help
```

```go
type CLI struct {
    Config    string           `kong:"required,short='c',env='MQBRIDGE_CONFIG',help='Config file path'"`
    LogFormat string           `kong:"default='text',enum='text,json',env='MQBRIDGE_LOG_FORMAT',help='Log format (text or json)'"`
    Run       RunCmd           `cmd:"" help:"Run the bridge"`
    Validate  ValidateCmd      `cmd:"" help:"Validate config"`
    Render    RenderCmd        `cmd:"" help:"Render config as JSON to stdout"`
    Version   kong.VersionFlag `help:"Show version"`
}
```

- `run`: Load config and start all bridges concurrently.
- `validate`: Load config and verify syntax/structure. Unknown fields cause an error.
- `render`: Evaluate config as Jsonnet and output resulting JSON to stdout (for debugging/verification).

## 5. Metrics

mqbridge supports OpenTelemetry metrics for observability. Metrics are auto-enabled when the `OTEL_EXPORTER_OTLP_ENDPOINT` environment variable is set.

### Instruments

| Metric | Type | Description | Attributes |
|--------|------|-------------|------------|
| `mqbridge.messages.received` | Int64Counter | Messages received from subscriber | source_type, source_queue |
| `mqbridge.messages.published` | Int64Counter | Messages published to destination | destination_type, destination_queue |
| `mqbridge.messages.errors` | Int64Counter | Message processing errors | source_type, source_queue |
| `mqbridge.message.processing.duration` | Float64Histogram | Time from receive to all publishes done (seconds) | source_type, source_queue |

### Configuration

Metrics are configured via standard OpenTelemetry environment variables:

- `OTEL_EXPORTER_OTLP_ENDPOINT` — OTLP endpoint URL (e.g., `http://localhost:4318`). If not set, metrics are disabled (zero overhead).
- `OTEL_EXPORTER_OTLP_HEADERS` — Additional headers for the exporter.
- `OTEL_EXPORTER_OTLP_PROTOCOL` — Protocol: `http/protobuf` (default) or `grpc`.

## 6. Libraries

| Library | Purpose |
|---------|---------|
| `github.com/alecthomas/kong` | CLI parser |
| `github.com/rabbitmq/amqp091-go` | RabbitMQ AMQP client |
| `github.com/sacloud/simplemq-api-go` | SimpleMQ API client |
| `github.com/fujiwara/jsonnet-armed` | Jsonnet config evaluation |
| `github.com/fujiwara/simplemq-cli` | simplemq-localserver (test) |
| `github.com/sacloud/secretmanager-api-go` | Sakura Cloud Secret Manager client |
| `github.com/fujiwara/sakura-secrets-cli` | sakura-secrets-localserver (test) |
| `github.com/fujiwara/sloghandler` | Structured log handler (colored text with source) |
| `go.opentelemetry.io/otel` | OpenTelemetry API |
| `go.opentelemetry.io/otel/sdk/metric` | OpenTelemetry metrics SDK |
| `go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp` | OTLP HTTP metrics exporter |
| `go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc` | OTLP gRPC metrics exporter |

## 7. Package Structure

```
mqbridge/
├── cmd/mqbridge/main.go       # CLI entry point
├── mqbridge.go                # Run(), bridge orchestration
├── config.go                  # Config loading (jsonnet-armed)
├── metrics.go                 # OpenTelemetry metrics instruments & setup
├── rabbitmq.go                # RabbitMQ subscribe/publish
├── secretmanager.go            # Secret Manager native function
├── simplemq.go                # SimpleMQ subscribe/publish
├── message.go                 # SimpleMQ→RabbitMQ message format
├── mqbridge_test.go           # Integration tests
├── metrics_test.go            # Metrics unit tests
├── testdata/config.jsonnet    # Test config
├── docker-compose.yml         # RabbitMQ container
└── spec.md
```

## 8. Core Interfaces

```go
// Subscriber consumes messages from a source
type Subscriber interface {
    Subscribe(ctx context.Context, handler func(ctx context.Context, msg []byte) error) error
    Close() error
}

// Publisher sends messages to a destination
type Publisher interface {
    Publish(ctx context.Context, msg []byte) error
    Close() error
}

// Bridge connects one Subscriber to multiple Publishers
type Bridge struct {
    From Subscriber
    To   []Publisher
}
```

- **RabbitMQSubscriber**: Consume from a queue and pass messages to the handler. Ack after handler succeeds.
- **RabbitMQPublisher**: Parse JSON received from SimpleMQ and publish to the specified destination.
- **SimpleMQSubscriber**: Receive via polling and pass messages to the handler. Delete after handler succeeds.
- **SimpleMQPublisher**: Send message body as-is.

## 9. Error Handling & Reliability

- **RabbitMQ**: Reconnect with retry on connection loss.
- **SimpleMQ**: Retry on HTTP errors.
- **Message processing failure**: RabbitMQ uses Nack (requeue); SimpleMQ does not delete (redelivery after visibility timeout).
- **Graceful shutdown**: On SIGTERM/SIGINT, stop all bridges and wait for in-flight messages to complete.

## 10. Testing

- **RabbitMQ**: Start a standalone RabbitMQ container via `docker-compose.yml`.
- **SimpleMQ**: Start `github.com/fujiwara/simplemq-cli` localserver package in-process.
- **Integration test**: Start both and verify bidirectional message forwarding.

## 11. Docker Compose

```yaml
services:
  rabbitmq:
    image: rabbitmq:4-management
    ports:
      - "5672:5672"
      - "15672:15672"
```
