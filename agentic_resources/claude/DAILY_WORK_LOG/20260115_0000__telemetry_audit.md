# Telemetry Implementation Audit Report

**Date:** 2026-01-15
**Component:** Podinfo Telemetry System
**Auditor:** Claude Code

## Executive Summary

This audit examined the observability and telemetry implementation across the podinfo microservice application. The application implements a comprehensive telemetry stack including OpenTelemetry tracing, Prometheus metrics, and structured logging via Zap. Overall, the implementation follows modern observability best practices with some areas for improvement.

## Audit Scope

1. OpenTelemetry distributed tracing implementation
2. Prometheus metrics collection and instrumentation
3. Structured logging with Zap
4. Middleware integration and ordering
5. gRPC telemetry coverage
6. Configuration management for telemetry features

## Findings

### 1. OpenTelemetry Tracing

**Location:** `pkg/api/http/tracer.go`, HTTP handlers

**Strengths:**
- ✅ Proper opt-in design - tracing is disabled by default and only enabled when `--otel-service-name` flag is set
- ✅ Comprehensive propagator support (W3C TraceContext, B3, Jaeger, OT, AWS X-Ray)
- ✅ Uses OTLP/gRPC exporter for modern, efficient trace export
- ✅ Proper resource attributes with service name and version
- ✅ Trace IDs are correctly correlated with logs (see `pkg/api/http/logging.go:30-33`)
- ✅ HTTP handlers consistently create spans using `s.tracer.Start()`
- ✅ Context propagation for outbound HTTP calls via `otelhttp.NewTransport` and `otelhttptrace`
- ✅ Manual trace header forwarding for legacy B3/OT headers in `copyTracingHeaders()`

**Issues:**
- ⚠️ **Line 32 in tracer.go**: NoopTracer is created with empty service name string instead of using the instrumentation name constant
  ```go
  s.tracer = nop.Tracer(viper.GetString("otel-service-name")) // Empty string
  ```
  Should be:
  ```go
  s.tracer = nop.Tracer(instrumentationName)
  ```

- ⚠️ **Missing error handling**: If OTLP exporter creation fails (line 38-40), error is logged but tracing initialization continues with a nil exporter, which will cause a panic when creating the TracerProvider

- 📝 **Semantic conventions version**: Using `semconv v1.7.0` which is quite old. Current stable version is v1.21+

**Recommendations:**
1. Fix the noop tracer initialization to use `instrumentationName` constant
2. Return or panic if OTLP exporter creation fails - don't continue with invalid state
3. Upgrade to newer semantic conventions package (`go.opentelemetry.io/otel/semconv/v1.21.0` or later)
4. Consider adding span attributes for important business context (e.g., user ID, request size)

### 2. Prometheus Metrics

**Location:** `pkg/api/http/metrics.go`

**Strengths:**
- ✅ Implements RED method metrics (Rate, Errors, Duration)
- ✅ Request duration histogram with method, path, and status labels
- ✅ Request counter for HPA scaling
- ✅ Uses default Prometheus buckets (suitable for most HTTP workloads)
- ✅ Proper path normalization - converts Gorilla mux route templates to Prometheus-safe labels
- ✅ Sophisticated response writer wrapping to preserve HTTP interface implementations
- ✅ Metrics exposed on both main server (`/metrics`) and optional dedicated metrics port

**Issues:**
- 📝 **Histogram labels**: The histogram uses `method`, `path`, and `status` labels. This can lead to high cardinality if there are many unique paths (though the path normalization helps)
- 📝 **No custom buckets**: Uses `prometheus.DefBuckets` which may not be ideal for all use cases
- 📝 **Missing metrics**: No metrics for:
  - Request/response body sizes
  - Active/in-flight requests
  - Backend call duration (when using `--backend-url`)
  - Cache hit/miss rates
  - WebSocket connection counts

**Recommendations:**
1. Consider making histogram buckets configurable
2. Add gauge metric for in-flight requests
3. Add metrics for backend service calls with backend URL as label
4. Add cache metrics (hits, misses, errors)
5. Document metric names and labels in Swagger/API docs

### 3. Structured Logging

**Location:** `pkg/api/http/logging.go`, `cmd/podinfo/main.go`

**Strengths:**
- ✅ Uses Zap for high-performance structured logging
- ✅ JSON output format for machine parsing
- ✅ Configurable log levels via `--level` flag
- ✅ Logs include trace IDs when tracing is enabled (excellent correlation)
- ✅ Consistent field naming (snake_case)
- ✅ Request context logged: proto, URI, method, remote address, user-agent
- ✅ Sampling configuration to prevent log flooding (100/100)

**Issues:**
- 📝 **Log level**: Logs are at DEBUG level (`m.logger.Debug`) which means they won't appear with default INFO level
- 📝 **Missing request completion logging**: Only logs "request started" but no "request completed" with duration/status
- 📝 **Response status not logged**: The logging middleware doesn't capture response status or duration
- 📝 **Error logging inconsistency**: Some handlers use `s.logger.Error` with proper fields, but there's no structured error logging middleware

**Recommendations:**
1. Change logging middleware to log at INFO level or make it configurable
2. Add request completion logging with:
   - Response status code
   - Request duration
   - Response size
3. Consider creating response writer wrapper in logging middleware to capture status/size
4. Add correlation ID support (x-request-id) if not relying solely on trace IDs

### 4. Middleware Integration and Ordering

**Location:** `pkg/api/http/server.go:139-154`

**Current Order:**
1. Prometheus metrics
2. OpenTelemetry tracing
3. Request logging
4. Version headers
5. Random delay (optional)
6. Random errors (optional)

**Analysis:**
- ✅ Correct ordering: Metrics first (outermost) captures total request time including all middleware
- ✅ Tracing second ensures spans wrap all downstream operations
- ✅ Logging third gets trace context from tracing middleware
- ✅ Business logic middleware (version, delay, error) applied last

**Issues:**
- 📝 **No recovery middleware**: If a handler panics, the request won't be properly handled and metrics may be incomplete
- 📝 **No timeout middleware**: No request timeout enforcement at middleware level

**Recommendations:**
1. Add panic recovery middleware as the outermost layer
2. Consider adding timeout middleware for request deadline enforcement
3. Document the middleware ordering and rationale in code comments

### 5. gRPC Telemetry Coverage

**Location:** `pkg/api/grpc/server.go`

**Critical Finding:**
- ❌ **No telemetry whatsoever**: The gRPC server has:
  - No tracing interceptors
  - No metrics interceptors
  - No logging interceptors
  - Only health checks and reflection

**This is a major observability gap** as gRPC traffic is completely dark.

**Recommendations (HIGH PRIORITY):**
1. Add gRPC tracing via `go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc`
   ```go
   import "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"

   srv := grpc.NewServer(
       grpc.StatsHandler(otelgrpc.NewServerHandler()),
   )
   ```

2. Add gRPC Prometheus metrics via `github.com/grpc-ecosystem/go-grpc-prometheus`
   ```go
   import grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"

   srv := grpc.NewServer(
       grpc.UnaryInterceptor(grpc_prometheus.UnaryServerInterceptor),
       grpc.StreamInterceptor(grpc_prometheus.StreamServerInterceptor),
   )
   grpc_prometheus.Register(srv)
   ```

3. Add gRPC logging via custom interceptor:
   ```go
   grpc.UnaryInterceptor(unaryLoggingInterceptor),
   grpc.StreamInterceptor(streamLoggingInterceptor),
   ```

### 6. Configuration and Documentation

**Strengths:**
- ✅ Clear flag-based configuration
- ✅ Environment variable support (PODINFO_*)
- ✅ Docker Compose example with OTEL collector
- ✅ CLAUDE.md documents tracing is opt-in

**Issues:**
- 📝 **Missing docs**: No documentation about:
  - Available Prometheus metrics and their meanings
  - OpenTelemetry configuration options (OTEL_EXPORTER_OTLP_TRACES_ENDPOINT)
  - Log levels and what they control
- 📝 **No examples**: Missing example queries/dashboards for Prometheus
- 📝 **No sampling configuration**: Trace sampling is not configurable (always samples everything)

**Recommendations:**
1. Add metrics documentation to CLAUDE.md
2. Create example Grafana dashboard JSON
3. Add example Prometheus alert rules
4. Document all OTEL_* environment variables
5. Add trace sampling configuration option

## Code Quality Issues

### Thread-Safety Issues

**Location:** `pkg/api/http/delay.go:30`, `pkg/api/http/http.go:18`

**Issue:** `rand.Seed(time.Now().Unix())` is called on every request
- ❌ **Deprecated**: `rand.Seed` is deprecated as of Go 1.20
- ❌ **Not thread-safe**: Multiple goroutines calling `rand.Seed` simultaneously causes data races
- ❌ **Performance**: Reseeding on every request is unnecessary and slow

**Fix:**
```go
// Use Go 1.20+ rand/v2 or initialize once with sync.Once
var rng = rand.New(rand.NewSource(time.Now().UnixNano()))
var rngMutex sync.Mutex

func randomDelay() int {
    rngMutex.Lock()
    defer rngMutex.Unlock()
    return rng.Intn(max-min) + min
}
```

Or better, use `math/rand/v2` which is thread-safe by default.

## Security Considerations

**Strengths:**
- ✅ No sensitive data in logs (passwords, tokens, etc.)
- ✅ Headers properly sanitized for metrics labels
- ✅ Trace IDs don't expose sensitive information

**Recommendations:**
1. Add option to disable detailed error messages in production
2. Consider PII scrubbing for log fields
3. Document which headers are forwarded in traces

## Testing Coverage

**Findings:**
- ✅ HTTP handlers have unit tests
- ❌ **No telemetry tests**: No tests verify:
  - Metrics are incremented correctly
  - Spans are created with proper attributes
  - Logs contain expected fields
  - Middleware ordering is correct

**Recommendations:**
1. Add metrics tests using `prometheus/testutil`
2. Add tracing tests using OTEL SDK's test helpers
3. Add integration tests with real OTEL collector
4. Test log output format and fields

## Priority Action Items

### Critical (Fix Immediately)
1. **Add gRPC telemetry** - Complete observability gap
2. **Fix rand.Seed data race** - Thread-safety issue
3. **Fix tracer initialization** - Potential panic on error path

### High Priority
1. **Upgrade semantic conventions** - Using old version
2. **Add request completion logging** - Missing visibility
3. **Add telemetry tests** - No coverage

### Medium Priority
1. Add panic recovery middleware
2. Add backend call metrics
3. Document metrics and configuration
4. Make trace sampling configurable

### Low Priority
1. Add Grafana dashboards
2. Add example alert rules
3. Add cache metrics
4. Make histogram buckets configurable

## Conclusion

The podinfo application has a **solid telemetry foundation** for HTTP traffic with:
- Modern OpenTelemetry integration
- Prometheus RED metrics
- Structured JSON logging
- Proper trace-log correlation

However, there are **critical gaps**:
- gRPC traffic has no observability
- Thread-safety issues in middleware
- Limited test coverage for telemetry

**Overall Grade: B** (would be A- if gRPC telemetry was present)

The implementation demonstrates good understanding of observability principles but needs completion for production readiness.

## References

Files audited:
- `pkg/api/http/tracer.go` - OpenTelemetry setup
- `pkg/api/http/metrics.go` - Prometheus metrics
- `pkg/api/http/logging.go` - Request logging
- `pkg/api/http/server.go` - Middleware registration
- `pkg/api/http/http.go` - Helper middleware
- `pkg/api/http/delay.go` - Random delay middleware
- `pkg/api/grpc/server.go` - gRPC server
- `cmd/podinfo/main.go` - Configuration and initialization
- `otel/docker-compose.yaml` - OTEL example setup
- `go.mod` - Dependencies

## Next Steps

Would you like me to:
1. Create a detailed implementation plan to address the critical issues?
2. Implement gRPC telemetry?
3. Fix the thread-safety and initialization issues?
4. Add comprehensive telemetry tests?
5. Create example Grafana dashboards and Prometheus rules?
