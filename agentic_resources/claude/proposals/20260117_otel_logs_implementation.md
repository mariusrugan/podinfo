# OpenTelemetry Logs Implementation Proposal

**Date:** 2026-01-17
**Based on:** [20260116_1430__otel_logs_research.md](../DAILY_WORK_LOG/20260116_1430__otel_logs_research.md)
**Status:** Ready for Implementation

---

## Summary

Add OTLP log export to podinfo using the official `otelzap` bridge. Logs will be exported to the same OTLP endpoint as traces when `--otel-service-name` is set, while maintaining existing stderr output for backward compatibility.

## Implementation Plan

### Files to Modify

| File | Changes |
|------|---------|
| `go.mod` | Add 3 new dependencies |
| `cmd/podinfo/main.go` | Add LoggerProvider init, update initZap for dual-core |
| `pkg/signals/shutdown.go` | Add LoggerProvider to Shutdown struct and graceful shutdown |

### Files to Create (Optional)

| File | Purpose |
|------|---------|
| `pkg/api/http/logger.go` | LoggerProvider initialization (could keep in main.go) |

---

## Detailed Changes

### 1. Dependencies (`go.mod`)

```bash
go get go.opentelemetry.io/contrib/bridges/otelzap
go get go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc
go get go.opentelemetry.io/otel/sdk/log
```

### 2. `cmd/podinfo/main.go`

#### 2.1 Add imports

```go
import (
    // ... existing imports ...
    
    // NEW: OpenTelemetry logs
    "go.opentelemetry.io/contrib/bridges/otelzap"
    "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
    sdklog "go.opentelemetry.io/otel/sdk/log"
    "go.opentelemetry.io/otel/sdk/resource"
    semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)
```

#### 2.2 Add LoggerProvider initialization function

```go
// initLoggerProvider creates an OTLP log exporter if otel-service-name is set.
// Returns nil if OTEL is not configured (logs only go to stderr).
func initLoggerProvider(ctx context.Context, serviceName string) (*sdklog.LoggerProvider, error) {
    if serviceName == "" {
        return nil, nil // OTEL disabled, stderr-only logging
    }

    // Create OTLP gRPC exporter for logs
    logExporter, err := otlploggrpc.New(ctx)
    if err != nil {
        return nil, fmt.Errorf("failed to create OTLP log exporter: %w", err)
    }

    // Create LoggerProvider with batch processor
    loggerProvider := sdklog.NewLoggerProvider(
        sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
        sdklog.WithResource(resource.NewWithAttributes(
            semconv.SchemaURL,
            semconv.ServiceNameKey.String(serviceName),
            semconv.ServiceVersionKey.String(version.VERSION),
        )),
    )

    return loggerProvider, nil
}
```

#### 2.3 Modify `initZap` to accept LoggerProvider

```go
// initZap creates a zap logger with optional OTLP export.
// If loggerProvider is non-nil, logs are sent to both stderr and OTLP.
func initZap(logLevel string, loggerProvider *sdklog.LoggerProvider) (*zap.Logger, error) {
    level := zap.NewAtomicLevelAt(zapcore.InfoLevel)
    switch logLevel {
    case "debug":
        level = zap.NewAtomicLevelAt(zapcore.DebugLevel)
    case "info":
        level = zap.NewAtomicLevelAt(zapcore.InfoLevel)
    case "warn":
        level = zap.NewAtomicLevelAt(zapcore.WarnLevel)
    case "error":
        level = zap.NewAtomicLevelAt(zapcore.ErrorLevel)
    case "fatal":
        level = zap.NewAtomicLevelAt(zapcore.FatalLevel)
    case "panic":
        level = zap.NewAtomicLevelAt(zapcore.PanicLevel)
    }

    zapEncoderConfig := zapcore.EncoderConfig{
        TimeKey:        "ts",
        LevelKey:       "level",
        NameKey:        "logger",
        CallerKey:      "caller",
        MessageKey:     "msg",
        StacktraceKey:  "stacktrace",
        LineEnding:     zapcore.DefaultLineEnding,
        EncodeLevel:    zapcore.LowercaseLevelEncoder,
        EncodeTime:     zapcore.ISO8601TimeEncoder,
        EncodeDuration: zapcore.SecondsDurationEncoder,
        EncodeCaller:   zapcore.ShortCallerEncoder,
    }

    // Create stderr core
    stderrCore := zapcore.NewCore(
        zapcore.NewJSONEncoder(zapEncoderConfig),
        zapcore.AddSync(os.Stderr),
        level,
    )

    // If OTEL disabled, return stderr-only logger
    if loggerProvider == nil {
        return zap.New(stderrCore,
            zap.AddCaller(),
            zap.AddStacktrace(zapcore.ErrorLevel),
        ), nil
    }

    // Create OTEL core for dual output
    otelCore := otelzap.NewCore(
        instrumentationName,
        otelzap.WithLoggerProvider(loggerProvider),
    )

    // Combine cores: logs go to both stderr and OTLP
    combinedCore := zapcore.NewTee(stderrCore, otelCore)

    return zap.New(combinedCore,
        zap.AddCaller(),
        zap.AddStacktrace(zapcore.ErrorLevel),
    ), nil
}

const instrumentationName = "github.com/stefanprodan/podinfo"
```

#### 2.4 Update main() to initialize LoggerProvider

```go
func main() {
    // ... existing flag parsing and viper setup ...

    // Initialize LoggerProvider (before creating logger)
    ctx := context.Background()
    loggerProvider, err := initLoggerProvider(ctx, viper.GetString("otel-service-name"))
    if err != nil {
        fmt.Fprintf(os.Stderr, "Warning: failed to initialize OTEL log export: %v\n", err)
        // Continue without OTEL logs - not fatal
    }

    // Configure logging with optional OTEL export
    logger, _ := initZap(viper.GetString("level"), loggerProvider)
    defer logger.Sync()
    stdLog := zap.RedirectStdLog(logger)
    defer stdLog()

    // ... rest of main() unchanged until shutdown ...

    // Graceful shutdown - pass loggerProvider
    stopCh := signals.SetupSignalHandler()
    sd, _ := signals.NewShutdown(srvCfg.ServerShutdownTimeout, logger)
    sd.SetLoggerProvider(loggerProvider)  // NEW
    sd.Graceful(stopCh, httpServer, httpsServer, grpcServer, healthy, ready)
}
```

### 3. `pkg/signals/shutdown.go`

#### 3.1 Add import and field

```go
import (
    // ... existing imports ...
    sdklog "go.opentelemetry.io/otel/sdk/log"
)

type Shutdown struct {
    logger                *zap.Logger
    pool                  *redis.Pool
    tracerProvider        *sdktrace.TracerProvider
    loggerProvider        *sdklog.LoggerProvider  // NEW
    serverShutdownTimeout time.Duration
}
```

#### 3.2 Add setter method

```go
// SetLoggerProvider sets the OpenTelemetry LoggerProvider for graceful shutdown.
func (s *Shutdown) SetLoggerProvider(lp *sdklog.LoggerProvider) {
    s.loggerProvider = lp
}
```

#### 3.3 Update Graceful() to shutdown LoggerProvider

```go
func (s *Shutdown) Graceful(stopCh <-chan struct{}, httpServer *http.Server, httpsServer *http.Server, grpcServer *grpc.Server, healthy *int32, ready *int32) {
    ctx := context.Background()

    // wait for SIGTERM or SIGINT
    <-stopCh
    ctx, cancel := context.WithTimeout(ctx, s.serverShutdownTimeout)
    defer cancel()

    // all calls to /healthz and /readyz will fail from now on
    atomic.StoreInt32(healthy, 0)
    atomic.StoreInt32(ready, 0)

    // close cache pool
    if s.pool != nil {
        _ = s.pool.Close()
    }

    s.logger.Info("Shutting down HTTP/HTTPS server", zap.Duration("timeout", s.serverShutdownTimeout))

    // Brief wait for Kubernetes endpoint updates
    if viper.GetString("level") != "debug" {
        time.Sleep(3 * time.Second)
    }

    // stop OpenTelemetry tracer provider
    if s.tracerProvider != nil {
        if err := s.tracerProvider.Shutdown(ctx); err != nil {
            s.logger.Warn("stopping tracer provider", zap.Error(err))
        }
    }

    // NEW: stop OpenTelemetry logger provider (flushes pending logs)
    if s.loggerProvider != nil {
        if err := s.loggerProvider.Shutdown(ctx); err != nil {
            s.logger.Warn("stopping logger provider", zap.Error(err))
        }
    }

    // determine if the GRPC was started
    if grpcServer != nil {
        s.logger.Info("Shutting down GRPC server", zap.Duration("timeout", s.serverShutdownTimeout))
        grpcServer.GracefulStop()
    }

    // determine if the http server was started
    if httpServer != nil {
        if err := httpServer.Shutdown(ctx); err != nil {
            s.logger.Warn("HTTP server graceful shutdown failed", zap.Error(err))
        }
    }

    // determine if the secure server was started
    if httpsServer != nil {
        if err := httpsServer.Shutdown(ctx); err != nil {
            s.logger.Warn("HTTPS server graceful shutdown failed", zap.Error(err))
        }
    }
}
```

---

## Configuration

### Environment Variables

Same OTEL environment variables work for both traces and logs:

```bash
# Enable OTEL (both traces and logs)
PODINFO_OTEL_SERVICE_NAME="podinfo"

# Endpoint (auto-discovered)
OTEL_EXPORTER_OTLP_ENDPOINT="http://otel-collector:4317"

# Optional: Separate endpoints for logs vs traces
OTEL_EXPORTER_OTLP_LOGS_ENDPOINT="http://logs-collector:4317"
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT="http://traces-collector:4317"
```

### Helm Values (Future Enhancement)

```yaml
# charts/podinfo/values.yaml
otel:
  enabled: false
  serviceName: ""
  endpoint: ""
  # logs:
  #   enabled: true  # could be separate flag if desired
```

---

## Testing

### Manual Testing

```bash
# Start collector (otel/ directory already exists in repo)
cd otel && docker compose up -d

# Run podinfo with OTEL
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317 \
  EXTRA_RUN_ARGS="--otel-service-name=podinfo" \
  make run

# Make requests
curl localhost:9898/
curl localhost:9898/version

# Check collector logs for both traces and logs
docker compose logs otel-collector
```

### Expected Output in Collector

```
# Traces (existing)
Trace ID: abc123...
Span: GET /version

# Logs (new)
LogRecord #0
  Timestamp: 2026-01-17T...
  SeverityText: INFO
  Body: Starting podinfo
  Attributes:
    service.name: podinfo
    service.version: 6.9.4
    version: 6.9.4
    port: 9898
```

---

## Backward Compatibility

| Scenario | Behavior |
|----------|----------|
| `--otel-service-name` not set | Logs to stderr only (unchanged) |
| `--otel-service-name` set | Logs to stderr AND OTLP |
| OTEL collector unavailable | Logs still go to stderr, OTLP export fails gracefully |
| Existing log parsing | JSON format unchanged on stderr |

---

## Open Questions (Resolved)

1. **Shared vs separate flag?** → Use same `--otel-service-name` flag (simpler)
2. **Sampling?** → Let collector handle sampling (export all)
3. **HTTP vs gRPC?** → Use gRPC for consistency with traces
4. **Shutdown order?** → Shutdown LoggerProvider after TracerProvider but before servers

---

## Implementation Steps

1. [ ] Add dependencies to `go.mod`
2. [ ] Add `initLoggerProvider()` function to `main.go`
3. [ ] Modify `initZap()` for dual-core output
4. [ ] Update `main()` to initialize and pass LoggerProvider
5. [ ] Add `loggerProvider` field and setter to `Shutdown` struct
6. [ ] Add LoggerProvider shutdown to `Graceful()`
7. [ ] Update existing otel/ test configuration for logs pipeline
8. [ ] Test manually with collector
9. [ ] Run `make test` to ensure no regressions
10. [ ] Update README observability section

---

## Estimated Effort

- Code changes: ~100 lines
- Testing: 1-2 hours
- Documentation: 30 minutes

**Total: ~3-4 hours**
