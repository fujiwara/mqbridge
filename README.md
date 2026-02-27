# mqbridge

A message bridge between RabbitMQ and SimpleMQ. Define multiple forwarding rules (bridges) in a configuration file and run them concurrently.

## Features

- **RabbitMQ → SimpleMQ**: Consume from a RabbitMQ queue and forward messages to one or more SimpleMQ queues (fan-out).
- **SimpleMQ → RabbitMQ**: Poll a SimpleMQ queue and publish messages to RabbitMQ with exchange/routing key determined by message content.
- **Jsonnet configuration**: Use [jsonnet-armed](https://github.com/fujiwara/jsonnet-armed) for configuration with environment variable support (`env()`, `must_env()`).
- **Secret Manager integration**: Retrieve credentials from [Sakura Cloud Secret Manager](https://manual.sakura.ad.jp/cloud/manual-secret-manager.html) using `secret()` native function in Jsonnet.

## Installation

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
  --config, -c    Config file path (Jsonnet/JSON) (required)
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

### Secret Manager Integration

You can use `secret()` native function to retrieve credentials from Sakura Cloud Secret Manager. This requires `SAKURA_ACCESS_TOKEN` and `SAKURA_ACCESS_TOKEN_SECRET` environment variables.

```
std.native('secret')('vault-id', 'name')      // latest version
std.native('secret')('vault-id', 'name:1')    // specific version
```

```jsonnet
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
            api_key: std.native('secret')('vault-id-xxx', 'simplemq-api-key'),
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

## LICENSE

MIT

## Author

fujiwara
