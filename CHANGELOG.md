# Changelog

## [v0.1.0](https://github.com/fujiwara/mqbridge/compare/v0.0.4...v0.1.0) - 2026-03-11
- Update simplemq-cli to v0.4.0 by @fujiwara in https://github.com/fujiwara/mqbridge/pull/25
- Introduce structured Message type to preserve metadata across bridges by @fujiwara in https://github.com/fujiwara/mqbridge/pull/27
- Update simplemq-api-go to v0.5.0 by @fujiwara in https://github.com/fujiwara/mqbridge/pull/28

## [v0.0.4](https://github.com/fujiwara/mqbridge/compare/v0.0.3...v0.0.4) - 2026-03-03
- Add per-bridge url/api_url config with global defaults by @fujiwara in https://github.com/fujiwara/mqbridge/pull/21
- Bump github.com/sacloud/saclient-go from 0.2.6 to 0.3.1 by @dependabot[bot] in https://github.com/fujiwara/mqbridge/pull/20
- Refactor integration tests with testEnv helper by @fujiwara in https://github.com/fujiwara/mqbridge/pull/23
- Update simplemq-cli to v0.3.0 by @fujiwara in https://github.com/fujiwara/mqbridge/pull/24

## [v0.0.3](https://github.com/fujiwara/mqbridge/compare/v0.0.2...v0.0.3) - 2026-02-28
- Add architecture overview diagram and document exported struct by @fujiwara in https://github.com/fujiwara/mqbridge/pull/11
- Add bridge attribute to OpenTelemetry metrics by @fujiwara in https://github.com/fujiwara/mqbridge/pull/13
- Document at-least-once delivery semantics and duration metric scope by @fujiwara in https://github.com/fujiwara/mqbridge/pull/14
- Add exchange_passive option and fix base64 decode error handling by @fujiwara in https://github.com/fujiwara/mqbridge/pull/15
- Add SimpleMQ to SimpleMQ bridging support by @fujiwara in https://github.com/fujiwara/mqbridge/pull/16
- Make rabbitmq.url optional when no bridge uses RabbitMQ by @fujiwara in https://github.com/fujiwara/mqbridge/pull/17
- Simplify App.Run() and improve error wrapping by @fujiwara in https://github.com/fujiwara/mqbridge/pull/19

## [v0.0.3](https://github.com/fujiwara/mqbridge/compare/v0.0.2...v0.0.3) - 2026-02-28
- Add architecture overview diagram and document exported struct by @fujiwara in https://github.com/fujiwara/mqbridge/pull/11
- Add bridge attribute to OpenTelemetry metrics by @fujiwara in https://github.com/fujiwara/mqbridge/pull/13
- Document at-least-once delivery semantics and duration metric scope by @fujiwara in https://github.com/fujiwara/mqbridge/pull/14
- Add exchange_passive option and fix base64 decode error handling by @fujiwara in https://github.com/fujiwara/mqbridge/pull/15
- Add SimpleMQ to SimpleMQ bridging support by @fujiwara in https://github.com/fujiwara/mqbridge/pull/16
- Make rabbitmq.url optional when no bridge uses RabbitMQ by @fujiwara in https://github.com/fujiwara/mqbridge/pull/17

## [v0.0.2](https://github.com/fujiwara/mqbridge/compare/v0.0.1...v0.0.2) - 2026-02-27
- Add --log-format flag with sloghandler by @fujiwara in https://github.com/fujiwara/mqbridge/pull/5
- Support environment variables for CLI flags by @fujiwara in https://github.com/fujiwara/mqbridge/pull/7
- Add OpenTelemetry metrics support by @fujiwara in https://github.com/fujiwara/mqbridge/pull/8
- Add per-bridge logger and subscriber reconnection by @fujiwara in https://github.com/fujiwara/mqbridge/pull/9
- Update README with install methods and feature list by @fujiwara in https://github.com/fujiwara/mqbridge/pull/10

## [v0.0.1](https://github.com/fujiwara/mqbridge/commits/v0.0.1) - 2026-02-27
- Implement mqbridge: RabbitMQ <-> SimpleMQ message bridge by @fujiwara in https://github.com/fujiwara/mqbridge/pull/1
- Improvements: CI, test coverage, code quality by @fujiwara in https://github.com/fujiwara/mqbridge/pull/2
- Add secret() Jsonnet native function for Secret Manager by @fujiwara in https://github.com/fujiwara/mqbridge/pull/4
