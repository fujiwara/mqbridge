# mqbridge

RabbitMQ と SimpleMQ 間のメッセージブリッジ。

## Build & Test

```bash
# Build
go build ./cmd/mqbridge

# Test (requires RabbitMQ running on localhost:5672)
docker compose up -d
go test -v -race ./...

# Format (must run before commit)
go fmt ./...
```

## Architecture

- `mqbridge.go` — `Subscriber`/`Publisher` interfaces, `Bridge`, `App` orchestration
- `config.go` — Config structs, Jsonnet loader (`jsonnet-armed`)
- `cli.go` — CLI definition (`kong`), `RunCLI()` entry point
- `rabbitmq.go` — RabbitMQ subscriber/publisher (`amqp091-go`)
- `simplemq.go` — SimpleMQ subscriber/publisher (`simplemq-api-go`)
- `message.go` — SimpleMQ→RabbitMQ JSON message format
- `cmd/mqbridge/main.go` — minimal main, just calls `mqbridge.RunCLI(ctx)`

## Conventions

- CLI logic lives in `mqbridge` package (not `main`) for testability
- Test files are split by module: `config_test.go`, `message_test.go`, `mqbridge_test.go`
- Integration tests use RabbitMQ container + `simplemq-cli/localserver` in-process
- In CI (`CI` env var set), integration tests fail instead of skip when RabbitMQ is unavailable
- SimpleMQ message content is base64-encoded
- SimpleMQ default API URL comes from `simplemq.DefaultMessageAPIRootURL` (SDK), not hardcoded
- Config uses Jsonnet (`jsonnet-armed`), unknown fields cause error on validate

## Tips

- Use `t.Context()` in tests instead of `context.Background()`
- Run `go fix ./...` before commit to apply automatic modernizations (e.g. range over int)
- Constants and default values should come from upstream SDK when available, not be hardcoded
- When adding a new feature, add tests that cover both single and multiple destinations (fan-out)

## Dependencies

| Library | Purpose |
|---------|---------|
| `github.com/alecthomas/kong` | CLI parser |
| `github.com/rabbitmq/amqp091-go` | RabbitMQ AMQP client |
| `github.com/sacloud/simplemq-api-go` | SimpleMQ API client |
| `github.com/fujiwara/jsonnet-armed` | Jsonnet config evaluation |
| `github.com/fujiwara/simplemq-cli` | simplemq-localserver (test) |
