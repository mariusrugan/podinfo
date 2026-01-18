# OpenTelemetry Demo

The directory contains sample [OpenTelemetry Collector](https://github.com/open-telemetry/opentelemetry-collector)
and [Jaeger](https://www.jaegertracing.io) configurations for a tracing demo.

## Features

Podinfo supports OpenTelemetry for both **traces** and **logs**:

| Signal | Export Method | Enabled When |
|--------|---------------|--------------|
| Traces | OTLP gRPC | `--otel-service-name` is set |
| Logs | OTLP gRPC | `--otel-service-name` is set |

When `--otel-service-name` is set, logs are exported to the OTLP endpoint while also being written to stderr. The otelzap bridge automatically includes trace context (trace_id, span_id) for log-trace correlation.

### Environment Variables

- `OTEL_EXPORTER_OTLP_ENDPOINT` - Collector endpoint (e.g., `http://localhost:4317`)
- `OTEL_EXPORTER_OTLP_HEADERS` - Optional headers for authentication

## Configuration

The provided [docker-compose.yaml](docker-compose.yaml) sets up 4 Containers

1. PodInfo Frontend on port 9898
2. PodInfo Backend on port 9899
3. OpenTelemetry Collector listening on port 4317 for GRPC
4. Jaeger all-in-one listening on multiple ports

## How does it work?

The frontend pods are configured to call onto the backend pods. Both the podinfo
pods are configured to send traces over to the collector at port 4317 using GRPC.
The collector forwards all received spans to Jaeger over port 14250 and Jaeger
exposes a UI over port `16686`.

## Running it locally

1. Start all the Containers
```shell
make run
```
2. Send some sample requests
```shell
curl -v http://localhost:9898/status/200
curl -X POST -v http://localhost:9898/api/echo
```
3. Visit `http://localhost:16686/` to see the spans
4. Stop all the containers
```shell
make stop
```
