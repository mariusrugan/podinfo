---
name: implementOtelObservability
description: Implement OpenTelemetry observability features in a Go application with trace correlation.
argument-hint: Specify which OTEL signal to implement (logs, traces, metrics) and the logging library in use
---
Implement OpenTelemetry observability in the current Go application following these guidelines:

## Analysis Phase
1. Review existing logging/tracing setup in the codebase
2. Identify the logging library in use (zap, logrus, slog, etc.)
3. Check for existing OTEL dependencies and configuration patterns
4. Understand the application's graceful shutdown mechanism

## Implementation Requirements
1. **Dependencies**: Add required OTEL SDK packages (sdk/log, exporters, bridges)
2. **Provider Initialization**: Create provider initialization functions matching existing patterns (e.g., tracer setup)
3. **Dual Output**: Maintain existing output (stderr/file) while adding OTEL export
4. **Trace Correlation**: Ensure logs include trace_id/span_id for correlation with traces
5. **Graceful Shutdown**: Register providers for proper shutdown to flush pending data
6. **Backward Compatibility**: Feature should be opt-in, controlled by configuration flag

## Trace Correlation Pattern
When using logger bridges (otelzap, otellogrus, etc.):
- Pass request context to the bridge for automatic trace extraction
- Keep trace_id visible in local output for debugging
- Use appropriate field types to avoid cluttering local output with context data

## Configuration
- Use the same OTLP endpoint configuration as existing OTEL signals
- Follow existing flag/env var naming conventions (e.g., `--otel-service-name`)
- Match resource attributes (service.name, service.version) across all signals

## Testing Checklist
- [ ] Application builds successfully
- [ ] Existing tests pass
- [ ] Works without OTEL enabled (backward compatible)
- [ ] Works with OTEL enabled (logs exported with trace context)
- [ ] Graceful shutdown flushes pending data
