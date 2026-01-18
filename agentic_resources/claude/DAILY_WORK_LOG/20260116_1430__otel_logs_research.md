# OpenTelemetry Logs Export Research

**Date:** 2026-01-16
**Time:** 14:30
**Task:** Research sending logs to OpenTelemetry endpoint

---

## Executive Summary

This document outlines the research findings for implementing OpenTelemetry log export in the podinfo application. Currently, podinfo has:
- ✅ **Structured logging** via Zap (JSON format)
- ✅ **OTLP trace export** via gRPC exporter
- ✅ **Trace-log correlation** (trace_id in log fields)
- ❌ **No log export to OTEL** (logs only go to stderr)

The infrastructure for OTLP export already exists for traces, making log export addition straightforward.

---

## Current State Analysis

### Logging Implementation

**Location:** `cmd/podinfo/main.go:169-214`

Podinfo uses **Uber Zap** for structured logging:
```go
zapConfig := zap.Config{
    Level:       level,
    Development: false,
    Encoding:    "json",
    OutputPaths: []string{"stderr"},
    // ... timestamp, field names, sampling configuration
}
```

**Key Characteristics:**
- JSON-encoded logs to stderr
- Configurable log level via `--level` flag
- Sampling: Initial 100, Thereafter 100 (prevents log spam)
- ISO8601 timestamps with structured fields

**Logger Distribution:**
- HTTP server receives logger instance
- gRPC server receives logger instance
- Signal handlers receive logger instance
- All handlers can perform structured logging

### OpenTelemetry Tracing Integration

**Location:** `pkg/api/http/tracer.go:1-71`

**Current OTLP Export:**
```go
func (s *Server) initTracer() (*sdktrace.TracerProvider, error) {
    if s.config.OTELServiceName == "" {
        return sdktrace.NewTracerProvider(), nil // noop
    }

    // OTLP gRPC exporter for traces
    client := otlptracegrpc.NewClient()
    exporter, _ := otlptrace.New(context.Background(), client)

    tp := sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exporter),
        sdktrace.WithResource(resource.NewWithAttributes(
            semconv.SchemaURL,
            semconv.ServiceNameKey.String(s.config.OTELServiceName),
            semconv.ServiceVersionKey.String(version.VERSION),
        )),
    )

    return tp, nil
}
```

**Infrastructure Already Present:**
- OTLP gRPC client configuration
- Batcher for efficient export
- Resource attributes (service name, version)
- Graceful shutdown in `pkg/signals/shutdown.go:58-63`
- Multi-propagator support (W3C, B3, Jaeger, OT, X-Ray)

### Trace-Log Correlation

**Location:** `pkg/api/http/logging.go:20-42`

The logging middleware already extracts trace context:
```go
func LoggingMiddleware(logger *zap.Logger) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // ... timing setup

            spanCtx := oteltrace.SpanContextFromContext(r.Context())
            if spanCtx.IsValid() {
                logger.With(zap.String("trace_id", spanCtx.TraceID().String()))
            }

            // ... log request with trace_id field
        })
    }
}
```

This means logs already have trace correlation fields when tracing is enabled.

---

## OpenTelemetry Logs Specification

### Log Signal Status

As of January 2026, OpenTelemetry logs support in Go is **experimental** but actively maintained:
- Stable API with pre-v1 versioning
- Two OTLP exporters available: HTTP and gRPC
- Official bridge packages for popular loggers
- Production-ready with appropriate testing

### Architecture

```
┌─────────────────┐
│   Zap Logger    │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  otelzap Core   │ (Bridge)
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ LoggerProvider  │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ BatchProcessor  │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ OTLP Exporter   │ (gRPC or HTTP)
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│   Collector     │
└─────────────────┘
```

---

## Implementation Approaches

### Approach 1: Official otelzap Bridge (Recommended)

**Package:** `go.opentelemetry.io/contrib/bridges/otelzap`
**Version:** v0.14.0 (actively maintained, updated January 2026)

**Advantages:**
- ✅ Official OpenTelemetry project
- ✅ Well-documented and actively maintained
- ✅ Automatic field conversion (zap → OTEL attributes)
- ✅ Proper severity level mapping
- ✅ Support for caller information (file:line)
- ✅ Context propagation for trace correlation
- ✅ Recent fixes for data races and semantic conventions

**Disadvantages:**
- ⚠️ Pre-v1 (API may change, though unlikely given maturity)
- ⚠️ Adds dependency to contrib repository

**Field Mapping:**
| Zap Field | OTEL Field |
|-----------|------------|
| Time | Timestamp |
| Message | Body (StringValue) |
| Level | Severity (numeric + text) |
| Fields | Attributes (key-value pairs) |
| context.Context | Used for trace/span correlation |
| Logger name | Instrumentation scope name |
| Caller | code.filepath, code.lineno attributes |
| Stacktrace | exception.stacktrace attribute |

**Level Mapping:**
| Zap Level | OTEL Severity |
|-----------|---------------|
| DebugLevel | SeverityDebug |
| InfoLevel | SeverityInfo |
| WarnLevel | SeverityWarn |
| ErrorLevel | SeverityError |
| DPanicLevel | SeverityFatal1 |
| PanicLevel | SeverityFatal2 |
| FatalLevel | SeverityFatal3 |

### Approach 2: Uptrace otelzap

**Package:** `github.com/uptrace/opentelemetry-go-extra/otelzap`

**Characteristics:**
- Different approach: logs as span events
- Only records logs when span exists in context
- Good for trace-centric observability
- Not suitable for standalone log export

**Not Recommended for podinfo** because:
- Requires active span for every log (too restrictive)
- Doesn't create independent log records
- Many logs happen outside of traced requests

### Approach 3: Odigos opentelemetry-zap-bridge

**Package:** `github.com/odigos-io/opentelemetry-zap-bridge`

**Characteristics:**
- Third-party implementation
- Similar to official bridge
- Less actively maintained than official

**Not Recommended:** Use official bridge instead for better long-term support.

---

## Recommended Implementation

### Required Dependencies

```bash
go get go.opentelemetry.io/contrib/bridges/otelzap@v0.14.0
go get go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc@v1.38.0
go get go.opentelemetry.io/otel/sdk/log@v0.14.0
```

**Note:** Use gRPC exporter for consistency with existing trace export.

### Architecture Changes

**1. Logger Initialization** (`cmd/podinfo/main.go`)

Current flow:
```
zapConfig → zap.Config.Build() → *zap.Logger
```

Proposed flow:
```
zapConfig → zapcore.Core (console) ┐
                                    ├→ zapcore.NewTee() → *zap.Logger
otelzap.NewCore() (OTLP export)    ┘
```

**Benefits of Dual Output:**
- Logs continue to stderr (for kubectl logs, Kubernetes log collection)
- Logs also exported via OTLP (for centralized observability)
- No breaking changes to existing log collection

**2. LoggerProvider Setup** (new function in `cmd/podinfo/main.go`)

```go
func initLoggerProvider(config *Config) (*sdklog.LoggerProvider, error) {
    // Skip if OTEL not configured
    if config.OTELServiceName == "" {
        return nil, nil // no-op
    }

    ctx := context.Background()

    // Create OTLP gRPC exporter for logs
    logExporter, err := otlploggrpc.New(ctx)
    if err != nil {
        return nil, fmt.Errorf("failed to create log exporter: %w", err)
    }

    // Create LoggerProvider with batch processor
    loggerProvider := sdklog.NewLoggerProvider(
        sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
        sdklog.WithResource(resource.NewWithAttributes(
            semconv.SchemaURL,
            semconv.ServiceNameKey.String(config.OTELServiceName),
            semconv.ServiceVersionKey.String(version.VERSION),
        )),
    )

    return loggerProvider, nil
}
```

**3. Logger Creation with Bridge**

```go
func initLogger(config *Config, loggerProvider *sdklog.LoggerProvider) (*zap.Logger, error) {
    // Base zap configuration (existing)
    zapConfig := zap.Config{
        Level:       level,
        Development: false,
        Encoding:    "json",
        OutputPaths: []string{"stderr"},
        // ... rest of config
    }

    // Create stderr core
    stderrCore, err := zapConfig.Build()
    if err != nil {
        return nil, err
    }

    // If OTEL disabled, return stderr-only logger
    if loggerProvider == nil {
        return stderrCore, nil
    }

    // Create OTEL core
    otelCore := otelzap.NewCore(
        "github.com/stefanprodan/podinfo",
        otelzap.WithLoggerProvider(loggerProvider),
        otelzap.WithVersion(version.VERSION),
    )

    // Combine cores for dual output
    logger := zap.New(
        zapcore.NewTee(stderrCore.Core(), otelCore),
        zap.AddCaller(),
        zap.AddStacktrace(zapcore.ErrorLevel),
    )

    return logger, nil
}
```

**4. Graceful Shutdown** (`pkg/signals/shutdown.go`)

Add LoggerProvider shutdown:

```go
type Server struct {
    // ... existing fields
    loggerProvider *sdklog.LoggerProvider // ADD
}

func (s *Server) GracefulShutdown(ctx context.Context) error {
    // ... existing shutdown code

    // Shutdown tracer provider (existing)
    if s.tracerProvider != nil {
        if err := s.tracerProvider.Shutdown(ctx); err != nil {
            s.logger.Warn("stopping tracer provider", zap.Error(err))
        }
    }

    // Shutdown logger provider (NEW)
    if s.loggerProvider != nil {
        if err := s.loggerProvider.Shutdown(ctx); err != nil {
            s.logger.Warn("stopping logger provider", zap.Error(err))
        }
    }

    // ... rest of shutdown
}
```

### Configuration

**Activation:** Same flag as traces
- When `--otel-service-name` is set: both traces AND logs export to OTLP
- When empty: both disabled (current behavior preserved)

**Environment Variables:**

Standard OTEL environment variables work automatically:

```bash
# Service identification
OTEL_SERVICE_NAME="podinfo"

# Endpoint configuration (auto-discovered by exporters)
OTEL_EXPORTER_OTLP_ENDPOINT="http://otel-collector:4317"
OTEL_EXPORTER_OTLP_PROTOCOL="grpc"

# Authentication (if needed)
OTEL_EXPORTER_OTLP_HEADERS="authorization=Bearer token123"

# Batch processor tuning
OTEL_BLRP_SCHEDULE_DELAY="1s"
OTEL_BLRP_EXPORT_TIMEOUT="30s"
OTEL_BLRP_MAX_QUEUE_SIZE="2048"
OTEL_BLRP_MAX_EXPORT_BATCH_SIZE="512"

# Attribute limits
OTEL_LOGRECORD_ATTRIBUTE_COUNT_LIMIT="128"
OTEL_LOGRECORD_ATTRIBUTE_VALUE_LENGTH_LIMIT="-1"
```

**Separate Endpoints (Optional):**

If logs and traces go to different endpoints:

```bash
OTEL_EXPORTER_OTLP_ENDPOINT="http://collector:4317"
OTEL_EXPORTER_OTLP_LOGS_ENDPOINT="http://logs-collector:4317"
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT="http://traces-collector:4317"
```

### No Breaking Changes

- ✅ Logs still go to stderr (kubectl logs works)
- ✅ JSON format unchanged (existing log parsers work)
- ✅ Log level control unchanged
- ✅ Default behavior: logs only to stderr (OTEL disabled)
- ✅ Existing deployments unaffected without configuration
- ✅ Backward compatible

---

## Benefits of Implementation

### Unified Observability

**Before:**
- Traces → OTLP → Collector → Backend
- Metrics → Prometheus → Backend
- Logs → Stderr → Kubernetes → Log aggregator

**After:**
- Traces → OTLP → Collector → Backend
- Metrics → Prometheus → Backend
- **Logs → OTLP → Collector → Backend** (new!)

### Enhanced Correlation

Current trace correlation only adds trace_id to log text:
```json
{"level":"info","ts":"...","msg":"request handled","trace_id":"abc123"}
```

With OTEL logs, correlation is semantic:
```
LogRecord {
  trace_id: "abc123",
  span_id: "def456",
  body: "request handled",
  severity: INFO,
  attributes: { ... }
}
```

Backends can automatically link:
- Trace → Logs (all logs for this trace)
- Span → Logs (logs during this span)
- Log → Trace (jump to trace from log)

### Query Capabilities

**Structured Attributes:**

All Zap fields become queryable OTEL attributes:
```go
logger.Info("user action",
    zap.String("user_id", "user123"),
    zap.String("action", "purchase"),
    zap.Int("amount", 100),
)
```

Becomes:
```
attributes: {
  user_id: "user123",
  action: "purchase",
  amount: 100
}
```

Queryable in backends:
- "Show all logs where user_id = user123"
- "Show all logs where action = purchase AND amount > 50"

### Centralized Log Management

Single pipeline for all telemetry:
```
Application → OTLP → Collector → Backend(s)
```

Collector can:
- Route logs to multiple backends
- Filter/sample logs
- Enrich with metadata (k8s labels, etc.)
- Transform/redact sensitive data
- Tail sampling (keep logs for failed traces)

---

## Performance Considerations

### Batch Processing

BatchProcessor is asynchronous and efficient:
- Default: 512 records per batch
- Default: 1 second between batches
- Non-blocking: main thread continues
- Backpressure: queue size 2048 (drops if full)

### Memory Overhead

**Per log record (estimated):**
- LogRecord struct: ~200 bytes
- Attributes: ~50 bytes per field
- Batch buffer: 512 records ≈ 128 KB

**With 2048 queue:**
- Max memory: ~512 KB
- Minimal impact for typical applications

### Network Overhead

**Compression:**
- gRPC uses HTTP/2 with header compression
- Protobuf binary encoding (efficient)
- Batching reduces requests

**Comparison:**
- JSON to stderr: ~400 bytes/log
- Protobuf OTLP: ~150 bytes/log (compressed)

OTLP can be MORE efficient than verbose JSON!

### CPU Overhead

**Additional processing:**
1. Field conversion (zap.Field → OTEL attributes)
2. Severity mapping
3. Protobuf encoding
4. gRPC framing

**Expected impact:** < 1% CPU for typical log volumes
- Most work happens in batch processor goroutine
- Non-blocking for request handling

### Latency Impact

**Zero impact on request latency:**
- Logging is asynchronous
- OTLP export is asynchronous (batch processor)
- No synchronous I/O in request path

---

## Testing Strategy

### Unit Tests

**Test cases:**
1. LoggerProvider creation with/without OTEL config
2. Dual-core logger output (both stderr and OTEL)
3. Field conversion (zap fields → OTEL attributes)
4. Trace context extraction and correlation
5. Graceful shutdown flushes pending logs

### Integration Tests

**Test scenarios:**
1. Logs appear in both stderr and OTLP collector
2. Trace IDs match between traces and logs
3. Service name and version in log resource attributes
4. Log levels correctly mapped to OTEL severities
5. Structured fields become searchable attributes

### Manual Testing

**Setup local collector:**
```yaml
# docker-compose.yml
services:
  otel-collector:
    image: otel/opentelemetry-collector-contrib:latest
    ports:
      - "4317:4317"  # OTLP gRPC
      - "55679:55679" # zpages
    volumes:
      - ./otel-config.yaml:/etc/otel/config.yaml
    command: ["--config=/etc/otel/config.yaml"]
```

**Collector config:**
```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317

processors:
  batch:

exporters:
  logging:
    loglevel: debug

service:
  pipelines:
    logs:
      receivers: [otlp]
      processors: [batch]
      exporters: [logging]
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [logging]
```

**Test podinfo:**
```bash
./podinfo --otel-service-name=podinfo
# Make requests, observe logs in both:
# 1. podinfo stderr
# 2. collector output
```

---

## Migration Path

### Phase 1: Add Capability (No Behavior Change)

1. Add dependencies to go.mod
2. Implement LoggerProvider initialization
3. Create dual-core logger setup
4. Add LoggerProvider shutdown
5. Update tests

**Result:** Code exists but disabled by default (OTEL service name empty)

### Phase 2: Documentation

1. Update README with OTEL logs configuration
2. Add example collector configuration
3. Update Kubernetes manifests with environment variables
4. Add logging to observability documentation

### Phase 3: Helm/Kustomize Support

1. Add OTEL logs environment variables to Helm values
2. Update Kustomize overlays
3. Provide example configurations for popular backends:
   - Grafana Loki
   - Elasticsearch
   - OpenSearch
   - Cloud providers (GCP, AWS, Azure)

### Phase 4: Gradual Rollout

1. Enable in development/staging environments
2. Validate correlation with traces
3. Monitor performance impact
4. Enable in production with sampling if needed

---

## Alternatives Considered

### Alternative 1: JSON Log Parsing in Collector

**Approach:** Keep JSON logs, parse in OTEL collector using `filelog` receiver

**Pros:**
- No application changes
- Collector handles everything

**Cons:**
- ❌ Requires access to log files (not always available in K8s)
- ❌ No semantic trace correlation (just text parsing)
- ❌ More complex collector configuration
- ❌ Less reliable (parsing can break)
- ❌ Higher collector resource usage

### Alternative 2: Log Forwarding with Fluent Bit

**Approach:** Fluent Bit sidecar → parse logs → forward to OTLP

**Pros:**
- No application changes

**Cons:**
- ❌ Sidecar overhead (CPU, memory, network)
- ❌ Complex parsing configuration
- ❌ No semantic correlation
- ❌ Additional operational complexity
- ❌ More failure points

### Alternative 3: Application-Level OTLP Library

**Approach:** Replace Zap with OTEL logs API directly

**Pros:**
- Native OTEL integration

**Cons:**
- ❌ Breaking change (replace all logger calls)
- ❌ OTEL logs API less ergonomic than Zap
- ❌ Loses Zap ecosystem (plugins, middleware)
- ❌ Significant refactoring effort

### Why Official otelzap Bridge is Best

✅ Non-breaking (Zap API unchanged)
✅ Semantic correlation (real LogRecord fields)
✅ Efficient (native protobuf, no parsing)
✅ Simple (minimal code changes)
✅ Flexible (dual output to stderr + OTLP)
✅ Official (maintained by OpenTelemetry team)
✅ Future-proof (follows OTEL specification)

---

## Security Considerations

### Sensitive Data in Logs

**Current Risk:**
- Logs may contain tokens, API keys, PII
- Currently only goes to stderr (limited exposure)

**After OTLP Export:**
- Same data now goes to collector + backend
- More storage locations = higher risk

**Mitigations:**

1. **Application-level filtering:**
```go
// Before logging sensitive fields
logger.Info("token validated",
    zap.String("token", redact(token)),
)
```

2. **Custom processor for redaction:**
```go
type RedactProcessor struct {
    next sdklog.Processor
}

func (p *RedactProcessor) OnEmit(ctx context.Context, record *sdklog.Record) error {
    record.WalkAttributes(func(kv log.KeyValue) bool {
        if isSensitive(kv.Key) {
            record.AddAttributes(log.String(kv.Key, "***REDACTED***"))
        }
        return true
    })
    return p.next.OnEmit(ctx, record)
}
```

3. **Collector-level processing:**
```yaml
processors:
  attributes:
    actions:
      - key: password
        action: delete
      - key: api_key
        action: hash
```

### Network Security

**TLS Configuration:**

```go
// For production, use TLS
import "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"

exporter, err := otlploggrpc.New(ctx,
    otlploggrpc.WithTLSCredentials(credentials.NewClientTLSFromCert(certPool, "")),
)
```

**Environment variable:**
```bash
OTEL_EXPORTER_OTLP_CERTIFICATE=/path/to/cert.pem
```

### Authentication

**Headers:**
```bash
OTEL_EXPORTER_OTLP_HEADERS="authorization=Bearer token123"
```

**For production backends:**
- Use API keys in headers
- Rotate credentials regularly
- Use service accounts in Kubernetes

---

## Open Questions

### 1. Should logs export be independently configurable?

**Current proposal:** Use same flag as traces (`--otel-service-name`)

**Alternative:** Separate flag `--otel-logs-enabled`

**Decision needed:** Ask user preference
- **Pros of shared flag:** Simpler (one flag for all OTEL)
- **Pros of separate flag:** More granular control

### 2. Should we add log sampling?

**Context:** Zap already has sampling (100 initial, 100 thereafter)

**Question:** Should OTLP export respect same sampling?

**Options:**
1. Export all logs (let collector sample)
2. Apply Zap sampling to OTLP (consistent with stderr)
3. Add separate OTLP sampling configuration

**Recommendation:** Export all (option 1) - let collector decide

### 3. HTTP vs gRPC exporter?

**Current:** Traces use gRPC

**Proposal:** Logs use gRPC (consistency)

**Alternative:** Support both, let user choose

**Decision:** Stick with gRPC for consistency

### 4. Include log body in attributes?

**Context:** Some backends prefer log message as attribute

**Options:**
1. Body as OTLP Body field (standard)
2. Body as attribute `log.message` (some backends prefer)
3. Both (redundant but compatible)

**Recommendation:** Standard (option 1)

---

## Resources and References

### Official Documentation
- [OpenTelemetry Go Exporters](https://opentelemetry.io/docs/languages/go/exporters/)
- [OTLP Exporter Configuration](https://opentelemetry.io/docs/languages/sdk-configuration/otlp-exporter/)
- [OpenTelemetry Logs Specification](https://opentelemetry.io/docs/specs/otel/logs/)

### Package Documentation
- [go.opentelemetry.io/contrib/bridges/otelzap](https://pkg.go.dev/go.opentelemetry.io/contrib/bridges/otelzap)
- [go.opentelemetry.io/otel/sdk/log](https://pkg.go.dev/go.opentelemetry.io/otel/sdk/log)
- [go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc](https://pkg.go.dev/go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc)

### Integration Guides
- [Uptrace OpenTelemetry Zap Guide](https://uptrace.dev/guides/opentelemetry-zap)
- [Uptrace OpenTelemetry Logs Complete Guide](https://uptrace.dev/opentelemetry/logs)
- [Honeycomb Go SDK Examples](https://docs.honeycomb.io/send-data/logs/opentelemetry/sdk/go/)
- [OneUpTime: How to Set Up Structured Logging in Go with OpenTelemetry (2026)](https://oneuptime.com/blog/post/2026-01-07-go-structured-logging-opentelemetry/view)

### Community Projects
- [Odigos opentelemetry-zap-bridge](https://github.com/odigos-io/opentelemetry-zap-bridge)
- [Agoda otelzap](https://github.com/agoda-com/otelzap)

### Related Issues and Discussions
- [OpenTelemetry Go GitHub](https://github.com/open-telemetry/opentelemetry-go)
- [OpenTelemetry Go Contrib GitHub](https://github.com/open-telemetry/opentelemetry-go-contrib)
- [OpenTelemetry Go Contrib Changelog](https://github.com/open-telemetry/opentelemetry-go-contrib/blob/main/CHANGELOG.md)

---

## Conclusion

Implementing OTLP log export in podinfo is:
- ✅ **Straightforward** - leverage existing OTLP infrastructure
- ✅ **Non-breaking** - dual output maintains backward compatibility
- ✅ **Well-supported** - official packages with active maintenance
- ✅ **Performant** - asynchronous batching with minimal overhead
- ✅ **Valuable** - semantic correlation with traces, structured attributes

**Recommended approach:** Use official `otelzap` bridge with dual-core setup (stderr + OTLP).

**Next steps:**
1. Implement LoggerProvider initialization
2. Create dual-core logger with otelzap bridge
3. Add graceful shutdown for LoggerProvider
4. Add configuration tests
5. Update documentation

---

## Appendix: Code Locations

### Files to Modify

| File | Changes |
|------|---------|
| `cmd/podinfo/main.go` | Add LoggerProvider init, dual-core logger |
| `pkg/signals/shutdown.go` | Add LoggerProvider shutdown |
| `go.mod` | Add otelzap, otlploggrpc, sdk/log dependencies |
| `README.md` | Document OTEL logs configuration |
| `charts/podinfo/values.yaml` | Add OTEL logs env vars |

### Files to Create

| File | Purpose |
|------|---------|
| `pkg/api/http/logger.go` | Log initialization helpers (optional) |
| `test/logs_test.go` | Integration tests for log export |
| `docs/observability.md` | Comprehensive observability guide |

### Existing Files for Reference

| File | Contains |
|------|----------|
| `pkg/api/http/tracer.go` | Trace export (similar pattern) |
| `pkg/api/http/logging.go` | Trace-log correlation |
| `pkg/signals/shutdown.go` | TracerProvider shutdown (template) |

---

**End of Research Document**
