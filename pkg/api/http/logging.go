package http

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type LoggingMiddleware struct {
	logger *zap.Logger
}

func NewLoggingMiddleware(logger *zap.Logger) *LoggingMiddleware {
	return &LoggingMiddleware{
		logger: logger,
	}
}

// ctxField creates a zap field that carries context for otelzap bridge.
// The otelzap bridge extracts span context from context.Context fields
// to correlate logs with traces. This field is skipped by the JSON encoder.
func ctxField(ctx context.Context) zap.Field {
	return zap.Field{
		Key:       "",
		Type:      zapcore.SkipType,
		Interface: ctx,
	}
}

func (m *LoggingMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get request context (contains span context for OTEL)
		ctx := r.Context()

		fields := []zap.Field{
			zap.String("proto", r.Proto),
			zap.String("uri", r.RequestURI),
			zap.String("method", r.Method),
			zap.String("remote", r.RemoteAddr),
			zap.String("user-agent", r.UserAgent()),
		}

		// Add trace_id to stderr output (manual field for JSON logs)
		spanCtx := trace.SpanContextFromContext(ctx)
		if spanCtx.HasTraceID() {
			fields = append(fields, zap.String("trace_id", spanCtx.TraceID().String()))
		}

		// Pass context for otelzap bridge trace correlation
		// Uses SkipType so it doesn't appear in stderr JSON output
		fields = append(fields, ctxField(ctx))

		m.logger.Debug("request started", fields...)

		next.ServeHTTP(w, r)
	})
}
