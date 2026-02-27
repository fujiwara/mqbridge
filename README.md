# mqbridge

A message bridge between RabbitMQ and SimpleMQ. Define multiple forwarding rules (bridges) in a configuration file and run them concurrently.

## Features

- **RabbitMQ → SimpleMQ**: Consume from a RabbitMQ queue and forward messages to one or more SimpleMQ queues (fan-out).
- **SimpleMQ → RabbitMQ**: Poll a SimpleMQ queue and publish messages to RabbitMQ with exchange/routing key determined by message content.
- **Automatic reconnection**: RabbitMQ subscriber and publisher automatically reconnect with exponential backoff (1s–30s) on connection loss.
- **Graceful shutdown**: On SIGTERM/SIGINT, waits for in-flight messages to complete before exiting.
- **Named bridges**: Optional `name` field per bridge for readable log output.
- **OpenTelemetry metrics**: Built-in metrics (received, published, errors, duration) auto-enabled via `OTEL_EXPORTER_OTLP_ENDPOINT`.
- **Structured logging**: Text (colored) or JSON format with configurable log level.
- **Jsonnet configuration**: Use [jsonnet-armed](https://github.com/fujiwara/jsonnet-armed) for configuration with environment variable support (`env()`, `must_env()`).
- **Secret Manager integration**: Retrieve credentials from [Sakura Cloud Secret Manager](https://manual.sakura.ad.jp/cloud/manual-secret-manager.html) using `secret()` native function in Jsonnet.

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
{
  rabbitmq: {
    url: std.native('must_env')('RABBITMQ_URL'),
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
      name: 'smq-to-rmq',
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

### Secret Manager Integration

You can use `secret()` native function to retrieve credentials from Sakura Cloud Secret Manager. This requires `SAKURA_ACCESS_TOKEN` and `SAKURA_ACCESS_TOKEN_SECRET` environment variables.

```
local secret = std.native('secret');
secret('vault-id', 'name')      // latest version
secret('vault-id', 'name:1')    // specific version
```

```jsonnet
local secret = std.native('secret');
{
  rabbitmq: {
    url: std.native('must_env')('RABBITMQ_URL'),
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

### SimpleMQ → RabbitMQ Message Format

Messages from SimpleMQ to RabbitMQ must be in the following JSON format:

```json
{
  "exchange": "my-exchange",
  "routing_key": "my.routing.key",
  "headers": { "content-type": "application/json" },
  "body": "actual message content"
}
```

- `exchange` and `routing_key` are required.
- `headers` is optional.
- The content of `body` is published to RabbitMQ.

## Metrics

mqbridge supports [OpenTelemetry](https://opentelemetry.io/) metrics. Metrics are auto-enabled when the `OTEL_EXPORTER_OTLP_ENDPOINT` environment variable is set. When not set, metrics are disabled with zero overhead.

```console
$ OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 mqbridge run -c config.jsonnet
```

The following metrics are exported:

| Metric | Type | Description | Attributes |
|--------|------|-------------|------------|
| `mqbridge.messages.received` | Counter | Messages received from subscriber | `source_type`, `source_queue` |
| `mqbridge.messages.published` | Counter | Messages published to destination | `destination_type`, `destination_queue` |
| `mqbridge.messages.errors` | Counter | Message processing errors | `source_type`, `source_queue` |
| `mqbridge.message.processing.duration` | Histogram | Processing duration in seconds | `source_type`, `source_queue` |

Attribute values are derived from the bridge configuration and message content. `source_type` / `destination_type` is `rabbitmq` or `simplemq`. `source_queue` is the source queue name. `destination_queue` is the SimpleMQ queue name, or the exchange name for RabbitMQ (from each message).

Both HTTP and gRPC protocols are supported. Set `OTEL_EXPORTER_OTLP_PROTOCOL` to `grpc` for gRPC transport (default: `http/protobuf`). All standard `OTEL_*` environment variables are supported.

## LICENSE

MIT

## Author

fujiwara
