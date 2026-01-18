# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Podinfo is a Go-based demo microservice application showcasing best practices for running microservices in Kubernetes. It's used by CNCF projects like Flux and Flagger for end-to-end testing and workshops.

- **Language**: Go 1.25
- **Architecture**: Dual HTTP/gRPC server
- **Purpose**: Kubernetes demo/testing microservice
- **Current Version**: See `pkg/version/version.go`

## Coding Style & Naming Conventions
- Follow modern idiomatic Go
- gofmt

## Build and Development Commands

### Core Development
```bash
# Run the application locally with debug logging
make run

# Run all tests with coverage
make test

# Build both podinfo and podcli binaries
make build

# Format, vet, and tidy code (automatically run by make test)
make fmt
make vet
make tidy
```

### Container Operations
```bash
# Build Docker image
make build-container

# Build multi-arch image with buildx
make build-xx

# Test the container locally
make test-container
```

### Swagger Documentation
```bash
# Regenerate Swagger docs (requires swag tool)
make swagger
```

### Version Management
```bash
# Update version across all manifests
make version-set TAG=x.y.z
```

### Deployment Validation
```bash
# Lint and package Helm charts
make build-charts

# Build and validate Timoni module
make timoni-build
```

## Architecture

### Code Organization

```
podinfo/
├── cmd/
│   ├── podinfo/main.go    # Main server entry point
│   └── podcli/            # CLI client for testing
├── pkg/
│   ├── api/
│   │   ├── http/          # HTTP handlers, middleware, server
│   │   │   ├── server.go  # HTTP server setup, route registration
│   │   │   ├── mock.go    # Mock server for testing
│   │   │   ├── docs/      # Generated Swagger documentation
│   │   │   └── *.go       # Individual handlers (echo, token, etc.)
│   │   └── grpc/          # gRPC services
│   │       ├── server.go  # gRPC server setup
│   │       ├── mock_grpc.go # Mock server for testing
│   │       └── <service>/ # Each service has its own directory
│   │           ├── <service>.proto
│   │           ├── <service>.pb.go
│   │           └── <service>_grpc.pb.go
│   ├── fscache/           # File watcher for ConfigMap/Secret changes
│   ├── signals/           # Graceful shutdown handling
│   └── version/           # Version management
├── charts/podinfo/        # Helm chart
├── kustomize/             # Kustomize overlays
├── timoni/podinfo/        # Timoni module (CUE-based)
├── deploy/                # Deployment examples
└── test/                  # E2E test scripts
```

### Application Entry Point
- `cmd/podinfo/main.go` - Main server binary that starts both HTTP and gRPC servers
- `cmd/podcli/main.go` - CLI client for testing podinfo endpoints

### Configuration System
The application uses **Viper** for configuration with the following precedence:
1. Command-line flags
2. Environment variables (prefixed with `PODINFO_`)
3. Config file (`config.yaml` from `--config-path`)

All flags use dash-separated names (`--backend-url`) but map to environment variables with underscores (`PODINFO_BACKEND_URL`).

### Dual Server Architecture
The application runs both HTTP and gRPC servers concurrently:

**HTTP Server** (`pkg/api/http/server.go`):
- Uses Gorilla Mux router
- Supports optional HTTPS on separate port
- Optional metrics server on dedicated port
- Swagger UI available at `/swagger/index.html`

**gRPC Server** (`pkg/api/grpc/server.go`):
- Runs only if `--grpc-port` is set
- All service implementations live in `pkg/api/grpc/` with corresponding `.proto` files
- Includes gRPC health checking and reflection
- Uses reflection for service discovery

### Middleware Stack
HTTP middleware is applied in this order (see `pkg/api/http/server.go:registerMiddlewares`):
1. Prometheus metrics collection
2. OpenTelemetry tracing (if `--otel-service-name` is set)
3. Request logging (structured JSON via zap)
4. Version header injection (`X-API-Version`, `X-API-Revision`)
5. Random delay (if `--random-delay` is enabled)
6. Random errors (if `--random-error` is enabled)

### Observability
- **Logging**: Structured JSON logging via zap logger
  - Log levels: debug, info, warn, error, fatal, panic
  - Set via `--level` flag
- **Metrics**: Prometheus metrics exposed at `/metrics`
  - HTTP metrics: `http_request_duration_seconds`, `http_requests_total`
  - RED method for monitoring and alerting
- **Tracing**: OpenTelemetry with support for multiple propagators (B3, Jaeger, OT, AWS X-Ray)
  - Tracing is **disabled by default** and only enabled when `--otel-service-name` is set
  - Uses OTLP exporter (gRPC)
  - Configuration in `pkg/api/http/tracer.go`

### File Watching
The application includes a file watcher (`pkg/fscache/`) that monitors the config directory for changes to ConfigMaps and Secrets when running in Kubernetes.

### Graceful Shutdown
Shutdown handling is in `pkg/signals/`:
- Listens for SIGTERM and SIGINT signals
- Sets health/ready to false immediately
- Waits 3 seconds in non-debug mode (Kubernetes pod termination grace period)
- Gracefully stops HTTP, HTTPS, and gRPC servers with configurable timeout
- Updates health and readiness states before shutdown

### Version Management
Version is stored in `pkg/version/version.go`. The `REVISION` variable is injected at build time via ldflags with the Git commit hash.

To update version across all manifests:
```bash
make version-set TAG=x.y.z
```

## Testing

### Running Tests
```bash
# Run all tests
go test ./...

# Run tests with coverage
go test ./... -coverprofile cover.out

# Run a specific test
go test ./pkg/api/http -run TestVersionHandler

# Run tests for a specific package
go test ./pkg/api/grpc/...
```

### Test Organization
- Unit tests are co-located with the code they test (`*_test.go`)
- Integration/E2E tests are in `test/` directory
- Mock implementations in `pkg/api/http/mock.go` and `pkg/api/grpc/mock_grpc.go`

### Mock Usage

**HTTP Tests** - Use `NewMockServer()` from `pkg/api/http/mock.go`:
```go
srv := NewMockServer()
handler := http.HandlerFunc(srv.echoHandler)
req, _ := http.NewRequest("POST", "/api/echo", strings.NewReader(`{"test": true}`))
rr := httptest.NewRecorder()
handler.ServeHTTP(rr, req)
```

**gRPC Tests** - Use `NewMockGrpcServer()` from `pkg/api/grpc/mock_grpc.go`

### Test Style
- Table-driven tests are the standard pattern
- Use `httptest.NewRecorder()` for HTTP handler tests
- Tests use development zap logger

## HTTP Handler Conventions

### Handler Registration

Handlers are registered in `pkg/api/http/server.go:registerHandlers()`:
```go
s.router.HandleFunc("/endpoint", s.handlerFunc).Methods("GET", "POST")
```

### Handler Signature

```go
func (s *Server) myHandler(w http.ResponseWriter, r *http.Request) {
    ctx, span := s.tracer.Start(r.Context(), "myHandler")
    defer span.End()
    
    // Handler logic...
    
    s.JSONResponse(w, r, result)
}
```

### Response Helpers

- `s.JSONResponse(w, r, result)` - 200 OK with JSON
- `s.JSONResponseCode(w, r, result, code)` - Custom status with JSON
- `s.ErrorResponse(w, r, span, message, code)` - Error with tracing

### Swagger Annotations

Add Swagger docs above handlers:
```go
// MyEndpoint godoc
// @Summary Short description
// @Description Longer description
// @Tags HTTP API
// @Accept json
// @Produce json
// @Router /endpoint [post]
// @Success 200 {object} http.ResponseType
```

## gRPC Service Conventions

### Directory Structure

Each gRPC service has its own directory under `pkg/api/grpc/`:
```
pkg/api/grpc/echo/
├── echo.proto
├── echo.pb.go      # Generated
└── echo_grpc.pb.go # Generated
```

### Proto File Pattern

```protobuf
syntax = "proto3";
option go_package = "./echo";
package echo;

message Message {
    string body = 1;
}

service EchoService {
    rpc Echo(Message) returns (Message) {}
}
```

### Service Implementation

```go
type echoServer struct {
    echo.UnimplementedEchoServiceServer
    config *Config
    logger *zap.Logger
}

func (s *echoServer) Echo(ctx context.Context, msg *echo.Message) (*echo.Message, error) {
    return &echo.Message{Body: msg.Body}, nil
}
```

### Regenerating Protobuf Code

```bash
protoc --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  pkg/api/grpc/<service>/<service>.proto
```

## Key Dependencies

- **gorilla/mux** - HTTP routing
- **spf13/viper** - Configuration management
- **spf13/cobra** & **spf13/pflag** - CLI flag parsing
- **uber/zap** - Structured logging
- **prometheus/client_golang** - Metrics
- **opentelemetry** - Distributed tracing
- **golang-jwt/jwt** - JWT token handling
- **gomodule/redigo** - Redis client (optional)
- **swaggo/swag** - Swagger documentation generation

## Deployment

The repository includes three deployment methods:
- **Helm chart** in `charts/podinfo/`
- **Kustomize overlays** in `kustomize/`
- **Timoni module** in `timoni/podinfo/`

### Helm Chart (`charts/podinfo/`)
```bash
helm lint ./charts/podinfo/
helm template ./charts/podinfo/
```

### Kustomize (`kustomize/`)
```bash
kubectl kustomize ./kustomize/
```

### Timoni Module (`timoni/podinfo/`)
```bash
timoni build podinfo ./timoni/podinfo -f ./timoni/podinfo/debug_values.cue
```

All deployment manifests reference the version from `pkg/version/version.go` and are updated together via `make version-set`.

## CI/CD Validation

The CI pipeline (`.github/workflows/test.yml`) validates:
1. Unit tests (`make test`)
2. Helm chart linting and kubeconform validation
3. Kustomize overlay validation
4. CUE formatting (`cue fmt`)
5. Timoni module validation
6. Clean git working tree

## Important Gotchas

### Tracing is Opt-In
OpenTelemetry tracing only activates when `--otel-service-name` is provided. Without it, a no-op tracer is used.

### Version Injection
Build flags inject the git revision:
```bash
-ldflags "-s -w -X github.com/stefanprodan/podinfo/pkg/version.REVISION=$(GIT_COMMIT)"
```

### Config Path Watching
The application watches `--config-path` for ConfigMap/Secret changes (Kubernetes use case). Changes to `..data` symlink trigger cache updates.

### CGO Disabled
Static binary compilation with `CGO_ENABLED=0`.

### Redis is Optional
Redis connection (`--cache-server`) is only initialized if provided.

### H2C Support
HTTP/2 Cleartext can be enabled with `--h2c` flag.

### Kubernetes Shutdown Behavior
In non-debug mode, the server waits 3 seconds after receiving shutdown signal to allow Kubernetes to update endpoints.

### Data Directory
The `/data` directory is used for persistent storage (file store feature).

---

## Important instructions for AI assistants

**Work Log Convention**:
- Save work session logs in `agentic_resources/claude/DAILY_WORK_LOG/`
- **Naming format**: `YYYYMMDD_HHMM__description.md`
- **Examples**:
  - `20260113_2330__instruction_discrepancy_analysis.md`
  - `20260113_1530_)rate_limiting_implementation.md`
  - `20260114_0900__websocket_refactor.md`
- Include date (YYYYMMDD), time (HHMM in 24-hour format), and brief description
- Use underscores to separate components
- This format is sortable and human-readable

**Proposal Convention**:
- Save implementation proposals in `agentic_resources/claude/proposals/`
- **Naming format**: `YYYYMMDD_feature_name.md` or `feature_name_proposal.md`
