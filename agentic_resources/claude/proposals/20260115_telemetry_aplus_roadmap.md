# Telemetry A++ Roadmap

**Goal:** Transform podinfo's telemetry from B to A++ grade
**Current State:** Solid HTTP observability, critical gRPC gaps, missing advanced features
**Target State:** Production-grade, comprehensive, cloud-native observability

## What A++ Telemetry Looks Like

### The Three Pillars (Metrics, Traces, Logs)
- ✅ **Complete coverage** - All services, protocols, and operations instrumented
- ✅ **Automatic correlation** - Traces, logs, and metrics linked via trace/span IDs
- ✅ **Context propagation** - Full distributed tracing across service boundaries
- ✅ **Cardinality control** - Smart labeling to prevent metric explosion

### Production Readiness
- ✅ **Resilient** - Telemetry failures don't break the app
- ✅ **Performant** - <1% overhead, async export, sampling
- ✅ **Configurable** - Runtime adjustable, feature flags
- ✅ **Observable** - Telemetry system monitors itself

### Developer Experience
- ✅ **Discoverable** - Clear documentation, examples, dashboards
- ✅ **Actionable** - Alerts and runbooks for common issues
- ✅ **Testable** - Comprehensive test coverage
- ✅ **Debuggable** - Easy to troubleshoot in dev and prod

---

## Phase 0: Critical Fixes (Day 1)

**Effort:** 2-4 hours
**Priority:** CRITICAL - These are bugs/gaps

### 0.1 Fix Thread-Safety Bug ⚠️

**File:** `pkg/api/http/delay.go`, `pkg/api/http/http.go`

**Problem:** `rand.Seed()` data race, deprecated API

**Solution:**
```go
// Create new file: pkg/random/random.go
package random

import (
    "math/rand/v2"
    "sync"
)

var (
    rng   = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
    mu    sync.Mutex
)

// Intn returns a random int in [0, n)
func Intn(n int) int {
    mu.Lock()
    defer mu.Unlock()
    return rng.IntN(n)
}

// IntRange returns a random int in [min, max)
func IntRange(min, max int) int {
    if max <= min {
        return min
    }
    return min + Intn(max-min)
}
```

**Update:** `delay.go` and `http.go` to use `random.IntRange()`

### 0.2 Fix Tracer Initialization Bug ⚠️

**File:** `pkg/api/http/tracer.go`

**Changes:**
```go
func (s *Server) initTracer(ctx context.Context) error {
    if viper.GetString("otel-service-name") == "" {
        nop := trace.NewNoopTracerProvider()
        s.tracer = nop.Tracer(instrumentationName) // Fix: use constant
        return nil
    }

    client := otlptracegrpc.NewClient()
    exporter, err := otlptrace.New(ctx, client)
    if err != nil {
        // Don't continue with nil exporter
        return fmt.Errorf("creating OTLP trace exporter: %w", err)
    }

    s.tracerProvider = sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exporter),
        sdktrace.WithResource(resource.NewWithAttributes(
            semconv.SchemaURL,
            semconv.ServiceNameKey.String(viper.GetString("otel-service-name")),
            semconv.ServiceVersionKey.String(version.VERSION),
        )),
    )

    otel.SetTracerProvider(s.tracerProvider)
    otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
        propagation.TraceContext{},
        propagation.Baggage{},
        b3.New(),
        &jaeger.Jaeger{},
        &ot.OT{},
        &xray.Propagator{},
    ))

    s.tracer = s.tracerProvider.Tracer(
        instrumentationName,
        trace.WithInstrumentationVersion(version.VERSION),
        trace.WithSchemaURL(semconv.SchemaURL),
    )

    return nil
}
```

**Update:** `server.go:161` to handle error

### 0.3 Add gRPC Telemetry 🚨

**File:** `pkg/api/grpc/server.go`

**Add dependencies:**
```bash
go get go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc@latest
go get github.com/grpc-ecosystem/go-grpc-prometheus@latest
```

**Create:** `pkg/api/grpc/telemetry.go`
```go
package grpc

import (
    "context"
    "time"

    grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
    "github.com/prometheus/client_golang/prometheus"
    "github.com/spf13/viper"
    "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/trace"
    "go.uber.org/zap"
    "google.golang.org/grpc"
)

const instrumentationName = "github.com/stefanprodan/podinfo/pkg/api/grpc"

func (s *Server) setupTelemetry() ([]grpc.ServerOption, error) {
    var opts []grpc.ServerOption

    // 1. OpenTelemetry tracing (if enabled)
    if viper.GetString("otel-service-name") != "" {
        opts = append(opts,
            grpc.StatsHandler(otelgrpc.NewServerHandler()),
        )
    }

    // 2. Prometheus metrics
    grpcMetrics := grpc_prometheus.NewServerMetrics()
    prometheus.MustRegister(grpcMetrics)
    opts = append(opts,
        grpc.ChainUnaryInterceptor(
            grpcMetrics.UnaryServerInterceptor(),
            s.unaryLoggingInterceptor(),
        ),
        grpc.ChainStreamInterceptor(
            grpcMetrics.StreamServerInterceptor(),
            s.streamLoggingInterceptor(),
        ),
    )

    return opts, nil
}

// Logging interceptor for unary RPCs
func (s *Server) unaryLoggingInterceptor() grpc.UnaryServerInterceptor {
    return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
        start := time.Now()

        fields := []zap.Field{
            zap.String("grpc.method", info.FullMethod),
            zap.String("grpc.type", "unary"),
        }

        // Add trace ID if available
        spanCtx := trace.SpanContextFromContext(ctx)
        if spanCtx.HasTraceID() {
            fields = append(fields, zap.String("trace_id", spanCtx.TraceID().String()))
        }

        s.logger.Debug("grpc call started", fields...)

        resp, err := handler(ctx, req)

        duration := time.Since(start)
        fields = append(fields,
            zap.Duration("grpc.duration", duration),
            zap.Error(err),
        )

        if err != nil {
            s.logger.Error("grpc call failed", fields...)
        } else {
            s.logger.Debug("grpc call completed", fields...)
        }

        return resp, err
    }
}

// Logging interceptor for streaming RPCs
func (s *Server) streamLoggingInterceptor() grpc.StreamServerInterceptor {
    return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
        start := time.Now()

        fields := []zap.Field{
            zap.String("grpc.method", info.FullMethod),
            zap.String("grpc.type", "stream"),
        }

        spanCtx := trace.SpanContextFromContext(ss.Context())
        if spanCtx.HasTraceID() {
            fields = append(fields, zap.String("trace_id", spanCtx.TraceID().String()))
        }

        s.logger.Debug("grpc stream started", fields...)

        err := handler(srv, ss)

        duration := time.Since(start)
        fields = append(fields,
            zap.Duration("grpc.duration", duration),
            zap.Error(err),
        )

        if err != nil {
            s.logger.Error("grpc stream failed", fields...)
        } else {
            s.logger.Debug("grpc stream completed", fields...)
        }

        return err
    }
}
```

**Update:** `server.go:68-74`
```go
func (s *Server) ListenAndServe() (*grpc.Server, error) {
    listener, err := net.Listen("tcp", fmt.Sprintf(":%v", s.config.Port))
    if err != nil {
        return nil, fmt.Errorf("failed to listen on port %d: %w", s.config.Port, err)
    }

    // Setup telemetry (tracing, metrics, logging)
    opts, err := s.setupTelemetry()
    if err != nil {
        return nil, fmt.Errorf("failed to setup telemetry: %w", err)
    }

    srv := grpc.NewServer(opts...)
    server := health.NewServer()

    // ... rest of registration ...

    // Initialize metrics after server registration
    grpc_prometheus.Register(srv)

    go func() {
        if err := srv.Serve(listener); err != nil {
            s.logger.Fatal("failed to serve", zap.Error(err))
        }
    }()

    return srv, nil
}
```

---

## Phase 1: Enhanced Observability (Week 1)

**Effort:** 1-2 days
**Priority:** HIGH - Makes existing telemetry production-ready

### 1.1 Upgrade Dependencies

**File:** `go.mod`

```bash
# Update to latest semantic conventions
go get go.opentelemetry.io/otel/semconv/v1.28.0

# Update OTEL SDK (if needed)
go get go.opentelemetry.io/otel@latest
go get go.opentelemetry.io/otel/sdk@latest
```

**Update imports** in `tracer.go` from `v1.7.0` to `v1.28.0`

### 1.2 Improve HTTP Request Logging

**File:** `pkg/api/http/logging.go`

**Problem:** Only logs request start, missing completion, status, duration

**Solution:**
```go
package http

import (
    "net/http"
    "time"

    "go.opentelemetry.io/otel/trace"
    "go.uber.org/zap"
)

type LoggingMiddleware struct {
    logger *zap.Logger
}

type loggingResponseWriter struct {
    http.ResponseWriter
    statusCode    int
    bytesWritten  int
    wroteHeader   bool
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
    if !lrw.wroteHeader {
        lrw.statusCode = code
        lrw.wroteHeader = true
        lrw.ResponseWriter.WriteHeader(code)
    }
}

func (lrw *loggingResponseWriter) Write(b []byte) (int, error) {
    if !lrw.wroteHeader {
        lrw.WriteHeader(http.StatusOK)
    }
    n, err := lrw.ResponseWriter.Write(b)
    lrw.bytesWritten += n
    return n, err
}

func NewLoggingMiddleware(logger *zap.Logger) *LoggingMiddleware {
    return &LoggingMiddleware{
        logger: logger,
    }
}

func (m *LoggingMiddleware) Handler(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()

        // Wrap response writer to capture status and bytes
        lrw := &loggingResponseWriter{
            ResponseWriter: w,
            statusCode:     http.StatusOK,
        }

        // Build base fields
        fields := []zap.Field{
            zap.String("proto", r.Proto),
            zap.String("method", r.Method),
            zap.String("uri", r.RequestURI),
            zap.String("remote", r.RemoteAddr),
            zap.String("user_agent", r.UserAgent()),
        }

        // Add trace ID if available
        spanCtx := trace.SpanContextFromContext(r.Context())
        if spanCtx.HasTraceID() {
            fields = append(fields,
                zap.String("trace_id", spanCtx.TraceID().String()),
                zap.String("span_id", spanCtx.SpanID().String()),
            )
        }

        // Add request ID if present
        if reqID := r.Header.Get("X-Request-ID"); reqID != "" {
            fields = append(fields, zap.String("request_id", reqID))
        }

        // Serve the request
        next.ServeHTTP(lrw, r)

        // Log completion
        duration := time.Since(start)
        fields = append(fields,
            zap.Int("status", lrw.statusCode),
            zap.Int("bytes", lrw.bytesWritten),
            zap.Duration("duration", duration),
            zap.Float64("duration_ms", float64(duration.Milliseconds())),
        )

        // Log at appropriate level based on status
        if lrw.statusCode >= 500 {
            m.logger.Error("request completed", fields...)
        } else if lrw.statusCode >= 400 {
            m.logger.Warn("request completed", fields...)
        } else {
            m.logger.Info("request completed", fields...)
        }
    })
}
```

### 1.3 Add Advanced Metrics

**Create:** `pkg/api/http/metrics_advanced.go`

```go
package http

import (
    "net/http"
    "time"

    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

var (
    // In-flight requests gauge
    httpInFlightRequests = promauto.NewGauge(prometheus.GaugeOpts{
        Subsystem: "http",
        Name:      "requests_in_flight",
        Help:      "Current number of HTTP requests being processed.",
    })

    // Request/response size histograms
    httpRequestSizeBytes = promauto.NewHistogramVec(prometheus.HistogramOpts{
        Subsystem: "http",
        Name:      "request_size_bytes",
        Help:      "HTTP request size in bytes.",
        Buckets:   prometheus.ExponentialBuckets(100, 10, 7), // 100B to 10MB
    }, []string{"method", "path"})

    httpResponseSizeBytes = promauto.NewHistogramVec(prometheus.HistogramOpts{
        Subsystem: "http",
        Name:      "response_size_bytes",
        Help:      "HTTP response size in bytes.",
        Buckets:   prometheus.ExponentialBuckets(100, 10, 7),
    }, []string{"method", "path", "status"})

    // Backend call metrics
    backendRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
        Subsystem: "http",
        Name:      "backend_request_duration_seconds",
        Help:      "Duration of backend HTTP requests.",
        Buckets:   prometheus.DefBuckets,
    }, []string{"backend", "status"})

    backendRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
        Subsystem: "http",
        Name:      "backend_requests_total",
        Help:      "Total number of backend HTTP requests.",
    }, []string{"backend", "status"})

    // Cache metrics
    cacheOperationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
        Subsystem: "cache",
        Name:      "operations_total",
        Help:      "Total number of cache operations.",
    }, []string{"operation", "status"}) // operation: get/set/delete, status: hit/miss/error

    cacheOperationDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
        Subsystem: "cache",
        Name:      "operation_duration_seconds",
        Help:      "Duration of cache operations.",
        Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1},
    }, []string{"operation"})
)

// Middleware to track in-flight requests
type InFlightMiddleware struct{}

func NewInFlightMiddleware() *InFlightMiddleware {
    return &InFlightMiddleware{}
}

func (m *InFlightMiddleware) Handler(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        httpInFlightRequests.Inc()
        defer httpInFlightRequests.Dec()
        next.ServeHTTP(w, r)
    })
}

// Helper to record backend call metrics
func RecordBackendCall(backend string, duration time.Duration, statusCode int) {
    status := "success"
    if statusCode >= 400 {
        status = "error"
    }
    backendRequestDuration.WithLabelValues(backend, status).Observe(duration.Seconds())
    backendRequestsTotal.WithLabelValues(backend, status).Inc()
}

// Helper to record cache operations
func RecordCacheOperation(operation string, duration time.Duration, hit bool, err error) {
    status := "hit"
    if err != nil {
        status = "error"
    } else if !hit {
        status = "miss"
    }
    cacheOperationsTotal.WithLabelValues(operation, status).Inc()
    cacheOperationDuration.WithLabelValues(operation).Observe(duration.Seconds())
}
```

**Update:** `echo.go` to use `RecordBackendCall()`

**Update:** `cache.go` to use `RecordCacheOperation()`

### 1.4 Add Panic Recovery Middleware

**Create:** `pkg/api/http/recovery.go`

```go
package http

import (
    "fmt"
    "net/http"
    "runtime/debug"

    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
    "go.uber.org/zap"
)

var (
    panicCounter = promauto.NewCounterVec(prometheus.CounterOpts{
        Subsystem: "http",
        Name:      "panics_total",
        Help:      "Total number of panics recovered.",
    }, []string{"path"})
)

type RecoveryMiddleware struct {
    logger *zap.Logger
}

func NewRecoveryMiddleware(logger *zap.Logger) *RecoveryMiddleware {
    return &RecoveryMiddleware{logger: logger}
}

func (m *RecoveryMiddleware) Handler(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        defer func() {
            if err := recover(); err != nil {
                panicCounter.WithLabelValues(r.URL.Path).Inc()

                m.logger.Error("panic recovered",
                    zap.Any("error", err),
                    zap.String("path", r.URL.Path),
                    zap.String("method", r.Method),
                    zap.String("stack", string(debug.Stack())),
                )

                w.Header().Set("Content-Type", "application/json")
                w.WriteHeader(http.StatusInternalServerError)
                fmt.Fprintf(w, `{"code":500,"message":"Internal server error"}`)
            }
        }()

        next.ServeHTTP(w, r)
    })
}
```

**Update:** `server.go` middleware order:
```go
func (s *Server) registerMiddlewares() {
    // 1. Panic recovery (outermost - catch everything)
    recovery := NewRecoveryMiddleware(s.logger)
    s.router.Use(recovery.Handler)

    // 2. In-flight requests tracking
    inflight := NewInFlightMiddleware()
    s.router.Use(inflight.Handler)

    // 3. Prometheus metrics
    prom := NewPrometheusMiddleware()
    s.router.Use(prom.Handler)

    // 4. OpenTelemetry tracing
    otel := NewOpenTelemetryMiddleware()
    s.router.Use(otel)

    // 5. Request logging
    httpLogger := NewLoggingMiddleware(s.logger)
    s.router.Use(httpLogger.Handler)

    // 6. Version headers
    s.router.Use(versionMiddleware)

    // 7. Business logic middleware
    if s.config.RandomDelay {
        randomDelayer := NewRandomDelayMiddleware(s.config.RandomDelayMin, s.config.RandomDelayMax, s.config.RandomDelayUnit)
        s.router.Use(randomDelayer.Handler)
    }
    if s.config.RandomError {
        s.router.Use(randomErrorMiddleware)
    }
}
```

---

## Phase 2: Advanced Features (Week 2)

**Effort:** 2-3 days
**Priority:** MEDIUM - Production optimizations

### 2.1 Configurable Trace Sampling

**File:** `pkg/api/http/tracer.go`

**Add flags to `main.go`:**
```go
fs.Float64("otel-sampling-ratio", 1.0, "trace sampling ratio (0.0 to 1.0)")
```

**Update tracer initialization:**
```go
func (s *Server) initTracer(ctx context.Context) error {
    if viper.GetString("otel-service-name") == "" {
        nop := trace.NewNoopTracerProvider()
        s.tracer = nop.Tracer(instrumentationName)
        return nil
    }

    // ... exporter setup ...

    // Configure sampler
    samplingRatio := viper.GetFloat64("otel-sampling-ratio")
    var sampler sdktrace.Sampler
    if samplingRatio <= 0 {
        sampler = sdktrace.NeverSample()
    } else if samplingRatio >= 1.0 {
        sampler = sdktrace.AlwaysSample()
    } else {
        sampler = sdktrace.ParentBased(
            sdktrace.TraceIDRatioBased(samplingRatio),
        )
    }

    s.tracerProvider = sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exporter,
            // Configure batch processor for efficiency
            sdktrace.WithMaxQueueSize(2048),
            sdktrace.WithMaxExportBatchSize(512),
            sdktrace.WithBatchTimeout(5*time.Second),
        ),
        sdktrace.WithSampler(sampler),
        sdktrace.WithResource(resource.NewWithAttributes(
            semconv.SchemaURL,
            semconv.ServiceName(viper.GetString("otel-service-name")),
            semconv.ServiceVersion(version.VERSION),
            semconv.DeploymentEnvironment(viper.GetString("environment")),
        )),
    )

    // ... rest ...
}
```

### 2.2 Add Exemplars (Link Metrics to Traces)

**Update:** `metrics.go`

```go
// Import exemplar support
import (
    "github.com/prometheus/client_golang/prometheus"
    "go.opentelemetry.io/otel/trace"
)

func (p *PrometheusMiddleware) Handler(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        begin := time.Now()
        interceptor := &interceptor{ResponseWriter: w, statusCode: http.StatusOK}
        path := p.getRouteName(r)
        next.ServeHTTP(interceptor.wrappedResponseWriter(), r)

        var (
            status = strconv.Itoa(interceptor.statusCode)
            took   = time.Since(begin)
        )

        // Add exemplar with trace ID
        spanCtx := trace.SpanContextFromContext(r.Context())
        if spanCtx.HasTraceID() {
            exemplar := prometheus.Labels{
                "traceID": spanCtx.TraceID().String(),
            }
            p.Histogram.WithLabelValues(r.Method, path, status).(prometheus.ExemplarObserver).
                ObserveWithExemplar(took.Seconds(), exemplar)
        } else {
            p.Histogram.WithLabelValues(r.Method, path, status).Observe(took.Seconds())
        }

        p.Counter.WithLabelValues(status).Inc()
    })
}
```

### 2.3 Add Rich Span Attributes

**Create:** `pkg/api/http/tracing.go`

```go
package http

import (
    "net/http"
    "strconv"

    "go.opentelemetry.io/otel/attribute"
    semconv "go.opentelemetry.io/otel/semconv/v1.28.0"
    "go.opentelemetry.io/otel/trace"
)

// EnrichSpanWithHTTPAttributes adds standard HTTP attributes to a span
func EnrichSpanWithHTTPAttributes(span trace.Span, r *http.Request, statusCode int, responseSize int64) {
    attrs := []attribute.KeyValue{
        semconv.HTTPMethod(r.Method),
        semconv.HTTPTarget(r.URL.Path),
        semconv.HTTPScheme(r.URL.Scheme),
        semconv.HTTPStatusCode(statusCode),
        semconv.NetHostName(r.Host),
        semconv.UserAgentOriginal(r.UserAgent()),
    }

    if r.ContentLength > 0 {
        attrs = append(attrs, semconv.HTTPRequestContentLength(int(r.ContentLength)))
    }

    if responseSize > 0 {
        attrs = append(attrs, semconv.HTTPResponseContentLength(int(responseSize)))
    }

    if r.URL.RawQuery != "" {
        attrs = append(attrs, attribute.String("http.query", r.URL.RawQuery))
    }

    span.SetAttributes(attrs...)
}

// EnrichSpanWithError marks span as error and adds error details
func EnrichSpanWithError(span trace.Span, err error) {
    span.RecordError(err)
    span.SetStatus(codes.Error, err.Error())
}
```

**Update handlers** to use `EnrichSpanWithHTTPAttributes()`

### 2.4 Self-Monitoring Telemetry

**Create:** `pkg/telemetry/health.go`

```go
package telemetry

import (
    "sync/atomic"
    "time"

    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

var (
    // Track telemetry export health
    telemetryExportsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
        Subsystem: "telemetry",
        Name:      "exports_total",
        Help:      "Total number of telemetry exports.",
    }, []string{"type", "status"}) // type: traces/metrics, status: success/failure

    telemetryExportDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
        Subsystem: "telemetry",
        Name:      "export_duration_seconds",
        Help:      "Duration of telemetry exports.",
        Buckets:   []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10},
    }, []string{"type"})

    telemetryDroppedSpans = promauto.NewCounter(prometheus.CounterOpts{
        Subsystem: "telemetry",
        Name:      "dropped_spans_total",
        Help:      "Total number of dropped spans due to backpressure.",
    })

    // Health status
    telemetryHealthy int32 = 1
)

func RecordExport(exportType string, duration time.Duration, err error) {
    status := "success"
    if err != nil {
        status = "failure"
        atomic.StoreInt32(&telemetryHealthy, 0)
    }

    telemetryExportsTotal.WithLabelValues(exportType, status).Inc()
    telemetryExportDuration.WithLabelValues(exportType).Observe(duration.Seconds())
}

func RecordDroppedSpans(count int) {
    telemetryDroppedSpans.Add(float64(count))
    if count > 0 {
        atomic.StoreInt32(&telemetryHealthy, 0)
    }
}

func IsTelemetryHealthy() bool {
    return atomic.LoadInt32(&telemetryHealthy) == 1
}
```

---

## Phase 3: Dashboards & Alerts (Week 2)

**Effort:** 1 day
**Priority:** MEDIUM - Operational excellence

### 3.1 Create Grafana Dashboards

**Create:** `dashboards/grafana/podinfo-overview.json`

Key panels:
- **RED metrics**: Request rate, error rate, duration (p50/p95/p99)
- **Saturation**: In-flight requests, goroutines, memory
- **Backend health**: Backend call latency/errors
- **Cache performance**: Hit rate, operation latency
- **gRPC metrics**: RPC rate/errors/duration
- **Telemetry health**: Export success rate, dropped spans

**Create:** `dashboards/grafana/podinfo-traces.json`

Integration with Tempo/Jaeger for trace exploration

### 3.2 Create Prometheus Alert Rules

**Create:** `alerts/prometheus/podinfo.rules.yml`

```yaml
groups:
  - name: podinfo
    interval: 30s
    rules:
      # High error rate
      - alert: PodinfoHighErrorRate
        expr: |
          (
            sum(rate(http_requests_total{status=~"5.."}[5m]))
            /
            sum(rate(http_requests_total[5m]))
          ) > 0.05
        for: 2m
        labels:
          severity: warning
        annotations:
          summary: "High error rate on {{ $labels.instance }}"
          description: "Error rate is {{ $value | humanizePercentage }}"

      # High latency
      - alert: PodinfoHighLatency
        expr: |
          histogram_quantile(0.95,
            sum(rate(http_request_duration_seconds_bucket[5m])) by (le)
          ) > 1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High latency on {{ $labels.instance }}"
          description: "P95 latency is {{ $value }}s"

      # Backend failures
      - alert: PodinfoBackendFailures
        expr: |
          sum(rate(http_backend_requests_total{status="error"}[5m])) > 0
        for: 2m
        labels:
          severity: critical
        annotations:
          summary: "Backend failures detected"
          description: "{{ $value }} backend errors/sec"

      # Cache errors
      - alert: PodinfoCacheErrors
        expr: |
          rate(cache_operations_total{status="error"}[5m]) > 1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Cache errors detected"

      # Telemetry export failures
      - alert: PodinfoTelemetryFailures
        expr: |
          rate(telemetry_exports_total{status="failure"}[5m]) > 0
        for: 5m
        labels:
          severity: info
        annotations:
          summary: "Telemetry export failures"
          description: "OTEL exports are failing"

      # Memory leak detection
      - alert: PodinfoMemoryLeak
        expr: |
          increase(go_memstats_heap_alloc_bytes[1h]) > 100000000
        for: 2h
        labels:
          severity: warning
        annotations:
          summary: "Possible memory leak"
          description: "Heap grew by {{ $value | humanize }}B in 1h"
```

---

## Phase 4: Testing & Documentation (Week 3)

**Effort:** 2-3 days
**Priority:** HIGH - Required for production confidence

### 4.1 Add Telemetry Tests

**Create:** `pkg/api/http/metrics_test.go`

```go
package http

import (
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/testutil"
)

func TestPrometheusMiddleware(t *testing.T) {
    // Reset metrics
    prometheus.DefaultRegisterer = prometheus.NewRegistry()

    middleware := NewPrometheusMiddleware()
    handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    }))

    req := httptest.NewRequest("GET", "/test", nil)
    rec := httptest.NewRecorder()

    handler.ServeHTTP(rec, req)

    // Verify metrics were recorded
    metricCount := testutil.CollectAndCount(middleware.Counter)
    if metricCount == 0 {
        t.Error("Expected counter metric to be incremented")
    }

    histogramCount := testutil.CollectAndCount(middleware.Histogram)
    if histogramCount == 0 {
        t.Error("Expected histogram metric to be observed")
    }
}
```

**Create:** `pkg/api/http/tracing_test.go`

**Create:** `pkg/api/http/logging_test.go`

**Create:** `pkg/api/grpc/telemetry_test.go`

**Create:** Integration tests in `test/telemetry/`

### 4.2 Documentation

**Update:** `CLAUDE.md` with complete telemetry documentation

**Create:** `docs/TELEMETRY.md`

```markdown
# Telemetry Guide

## Overview
Podinfo implements comprehensive observability using the three pillars...

## Metrics
### HTTP Metrics
- `http_requests_total` - Counter of HTTP requests by status
- `http_request_duration_seconds` - Histogram of request duration by method/path/status
- `http_requests_in_flight` - Gauge of currently processing requests
- `http_request_size_bytes` - Histogram of request body sizes
- `http_response_size_bytes` - Histogram of response body sizes

### gRPC Metrics
- `grpc_server_started_total` - Counter of gRPC calls started
- `grpc_server_handled_total` - Counter of gRPC calls completed by code
- `grpc_server_handling_seconds` - Histogram of RPC latency

### Backend Metrics
- `http_backend_requests_total` - Counter of backend calls
- `http_backend_request_duration_seconds` - Histogram of backend latency

### Cache Metrics
- `cache_operations_total` - Counter of cache operations by operation/status
- `cache_operation_duration_seconds` - Histogram of cache operation latency

### Telemetry Health
- `telemetry_exports_total` - Counter of telemetry exports by type/status
- `telemetry_export_duration_seconds` - Histogram of export duration
- `telemetry_dropped_spans_total` - Counter of dropped spans

## Traces
### Configuration
```bash
# Enable tracing
--otel-service-name=podinfo-frontend

# Configure OTLP endpoint (default: localhost:4317)
export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://otel-collector:4317

# Configure sampling (0.0 to 1.0, default: 1.0)
--otel-sampling-ratio=0.1  # Sample 10% of traces
```

### Trace Attributes
All spans include:
- `http.method`, `http.target`, `http.status_code`
- `net.host.name`, `user_agent.original`
- `trace_id`, `span_id` (also in logs)

### Exemplars
Metrics include exemplars linking to traces for sampled requests.

## Logs
### Configuration
```bash
# Set log level
--level=info  # debug, info, warn, error, fatal, panic
```

### Log Format
```json
{
  "ts": "2026-01-15T12:34:56.789Z",
  "level": "info",
  "msg": "request completed",
  "method": "GET",
  "uri": "/api/info",
  "status": 200,
  "duration": 12.5,
  "duration_ms": 12.5,
  "trace_id": "abc123...",
  "span_id": "def456...",
  "request_id": "req-789"
}
```

## Dashboards
Import Grafana dashboards from `dashboards/grafana/`:
- `podinfo-overview.json` - RED metrics, saturation, health
- `podinfo-traces.json` - Trace exploration

## Alerts
Load Prometheus rules from `alerts/prometheus/podinfo.rules.yml`

## Examples
See `examples/telemetry/` for:
- Docker Compose with OTEL Collector + Jaeger + Prometheus
- Kubernetes manifests with OpenTelemetry Operator
- Sample queries and dashboards
```

**Create:** `examples/telemetry/docker-compose.yml` (comprehensive setup)

**Create:** `examples/telemetry/k8s/` (K8s manifests with OTEL Operator)

---

## Phase 5: Production Hardening (Week 3-4)

**Effort:** 2-3 days
**Priority:** MEDIUM - Production robustness

### 5.1 Add Circuit Breaker for Telemetry

**Problem:** OTEL export failures can impact app performance

**Solution:** Implement graceful degradation

```go
// Create: pkg/telemetry/circuit_breaker.go

type CircuitBreaker struct {
    failures     int32
    lastFailure  time.Time
    state        int32 // 0=closed, 1=open, 2=half-open
    threshold    int
    resetTimeout time.Duration
    mu           sync.RWMutex
}

func (cb *CircuitBreaker) Call(fn func() error) error {
    if cb.IsOpen() {
        return errors.New("circuit breaker open")
    }

    err := fn()
    if err != nil {
        cb.RecordFailure()
    } else {
        cb.RecordSuccess()
    }
    return err
}
```

### 5.2 Add Resource Detection

**Update:** `tracer.go` to auto-detect K8s/Cloud metadata

```go
import (
    "go.opentelemetry.io/otel/sdk/resource"
    semconv "go.opentelemetry.io/otel/semconv/v1.28.0"
)

// Automatically detect resources
res, err := resource.New(ctx,
    resource.WithFromEnv(),      // OTEL_RESOURCE_ATTRIBUTES
    resource.WithProcess(),      // Process info
    resource.WithOS(),           // OS info
    resource.WithContainer(),    // Container ID
    resource.WithHost(),         // Host info
    resource.WithAttributes(
        semconv.ServiceName(viper.GetString("otel-service-name")),
        semconv.ServiceVersion(version.VERSION),
    ),
)
```

### 5.3 Add Custom Metrics for Business Logic

**Examples:**
- Token generation rate
- Cache size/evictions
- Backend connection pool stats
- File store operations

### 5.4 Add Baggage Propagation

**Use case:** Propagate user ID, tenant ID across services

```go
import "go.opentelemetry.io/otel/baggage"

// In HTTP handler
member, _ := baggage.NewMember("user.id", userID)
bag, _ := baggage.New(member)
ctx = baggage.ContextWithBaggage(ctx, bag)

// Baggage propagates automatically via TextMapPropagator
```

---

## Phase 6: Advanced Optimizations (Optional)

**Effort:** 1-2 days
**Priority:** LOW - Performance optimizations

### 6.1 Tail-Based Sampling

**Problem:** Head-based sampling misses interesting traces (errors)

**Solution:** Use OTEL Collector tail sampling processor

```yaml
# otel-config.yaml
processors:
  tail_sampling:
    decision_wait: 10s
    policies:
      - name: errors
        type: status_code
        status_code: {status_codes: [ERROR]}
      - name: slow
        type: latency
        latency: {threshold_ms: 1000}
      - name: sample-rest
        type: probabilistic
        probabilistic: {sampling_percentage: 10}
```

### 6.2 Continuous Profiling

**Add:** `runtime/pprof` endpoints already exist at `/debug/pprof/`

**Integrate:** Pyroscope or Grafana Phlare for continuous profiling

### 6.3 Real User Monitoring (RUM)

**Add:** Frontend instrumentation for web UI
**Track:** Page load times, API call latencies from user perspective

### 6.4 Distributed Context Propagation

**Add:** W3C Correlation Context for business metadata
**Propagate:** Customer segment, A/B test variant, feature flags

---

## Success Criteria for A++

### ✅ Complete Coverage
- [x] HTTP server fully instrumented
- [x] gRPC server fully instrumented
- [x] Backend calls traced and measured
- [x] Cache operations tracked
- [x] Background jobs monitored (if any)

### ✅ Production Ready
- [x] Graceful degradation if telemetry fails
- [x] <1% performance overhead
- [x] Configurable sampling
- [x] Circuit breakers for exports
- [x] Self-monitoring metrics

### ✅ Correlated Observability
- [x] Trace IDs in logs
- [x] Exemplars link metrics to traces
- [x] Unified dashboard showing all three pillars
- [x] Context propagates across services

### ✅ Actionable Insights
- [x] Pre-built dashboards
- [x] Alert rules for common issues
- [x] Runbooks for alerts
- [x] SLI/SLO definitions

### ✅ Developer Experience
- [x] Comprehensive documentation
- [x] Working examples
- [x] Easy to test locally
- [x] Well-tested codebase

### ✅ Cloud Native
- [x] Auto-resource detection
- [x] Works with OTEL Operator
- [x] Follows OTEL best practices
- [x] Compatible with major backends (Jaeger, Tempo, Prometheus, Grafana)

---

## Implementation Timeline

### Week 1: Foundation
- **Day 1-2:** Phase 0 (Critical fixes)
- **Day 3-5:** Phase 1 (Enhanced observability)

### Week 2: Advanced Features
- **Day 1-2:** Phase 2 (Sampling, exemplars, attributes)
- **Day 3-4:** Phase 3 (Dashboards, alerts)
- **Day 5:** Phase 4 start (Testing)

### Week 3: Polish
- **Day 1-3:** Phase 4 complete (Tests, docs)
- **Day 4-5:** Phase 5 (Production hardening)

### Week 4: Optional Enhancements
- **Day 1-3:** Phase 6 (Advanced optimizations)
- **Day 4-5:** Buffer for refinement

**Total Effort:** 15-20 days for full A++ grade

---

## Quick Start (Critical Path Only)

If you want to get to A- quickly (2-3 days):

1. **Day 1:** Fix Phase 0 (all critical bugs)
2. **Day 2:** Add gRPC telemetry, improve logging, add panic recovery
3. **Day 3:** Create dashboards, basic alerts, documentation

This gets you to **A- grade** with minimal investment.

---

## What do you want to implement?

I can help you:

1. **Execute the full roadmap** - I'll implement everything phase by phase
2. **Critical path only** - Fix bugs, add gRPC telemetry, get to A- in 2-3 days
3. **Specific phase** - Pick any phase to implement
4. **Review and customize** - Adjust the plan based on your priorities

Which approach would you like?
