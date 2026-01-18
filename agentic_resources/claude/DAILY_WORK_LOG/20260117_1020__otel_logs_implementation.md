# OTEL Logs Implementation

**Date**: 2026-01-17 10:20
**Status**: Completed

## Summary

Implemented OpenTelemetry logs export using the official otelzap bridge, enabling dual-output logging to both stderr and OTLP endpoints when `--otel-service-name` is set.

## Changes Made

### 1. Dependencies Added (`go.mod`)
- `go.opentelemetry.io/otel/sdk/log v0.15.0`
- `go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.15.0`
- `go.opentelemetry.io/contrib/bridges/otelzap v0.14.0`

### 2. `cmd/podinfo/main.go`
- Added `initLoggerProvider()` function to create OTLP log exporter
- Modified `initZap()` to accept optional `*sdklog.LoggerProvider` parameter
- Updated `initZap()` to use `zapcore.NewTee()` for dual-core output (stderr + OTEL)
- Updated `main()` to initialize logger provider when OTEL is enabled
- Connected logger provider to shutdown handler via `sd.SetLoggerProvider()`

### 3. `pkg/signals/shutdown.go`
- Added `loggerProvider *sdklog.LoggerProvider` field to `Shutdown` struct
- Added `SetLoggerProvider()` method for setting the logger provider
- Updated `Graceful()` to shutdown logger provider after tracer provider (ensures logs are flushed)

## Architecture

```
                    ┌─────────────────────┐
                    │      initZap()      │
                    └──────────┬──────────┘
                               │
              ┌────────────────┴────────────────┐
              │                                 │
              ▼                                 ▼
    ┌─────────────────┐             ┌─────────────────┐
    │   stderr core   │             │   otelzap core  │
    │ (JSON encoder)  │             │   (bridge)      │
    └─────────────────┘             └────────┬────────┘
              │                              │
              ▼                              ▼
         stderr output              OTLP gRPC exporter
                                           │
                                           ▼
                                    OTEL Collector
```

## Behavior

| Condition | Stderr Output | OTLP Export |
|-----------|---------------|-------------|
| `--otel-service-name` not set | ✅ JSON logs | ❌ Disabled |
| `--otel-service-name=podinfo` | ✅ JSON logs | ✅ Enabled |

## Testing

1. **Build**: `make build` ✅
2. **Unit tests**: `go test ./...` ✅
3. **Manual test without OTEL**: `./bin/podinfo --level=info` ✅
4. **Manual test with OTEL**: `OTEL_EXPORTER_OTLP_ENDPOINT=http://192.168.1.100:4317 ./bin/podinfo --level=debug --otel-service-name=podinfo` ✅

## Configuration

Logs use the same OTLP endpoint configuration as traces via environment variables:
- `OTEL_EXPORTER_OTLP_ENDPOINT` - Collector endpoint (e.g., `http://localhost:4317`)
- `OTEL_EXPORTER_OTLP_HEADERS` - Optional headers for authentication

## Notes

- Used `resource.NewWithAttributes()` directly (matching tracer.go pattern) to avoid schema version conflicts between `resource.Default()` and semconv versions
- Logger provider is shut down before tracer provider to ensure all logs are flushed before traces
- The otelzap bridge automatically includes trace context (trace_id, span_id) for log-trace correlation
