// Package logging sets up structured JSON logging for the whole app,
// using the standard library's log/slog - no third-party logging
// dependency needed. Mirrors the request-id-tagged JSON logs from
// the original Python version's logging_config.py.
package logging

import (
	"context"
	"log/slog"
	"os"
)

type ctxKey string

const requestIDKey ctxKey = "request_id"

// New builds a JSON slog.Logger writing to stdout, ready to be picked up
// by any log aggregator (Loki, ELK, CloudWatch, etc.) without extra parsing.
func New(level slog.Level) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})
	return slog.New(handler)
}

// WithRequestID stores a request ID on the context so downstream log
// calls can be tied back to the request that triggered them.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// FromContext returns a logger that automatically attaches the request_id
// found on ctx (if any) to every log line, the same way the Python version's
// ContextVar-based request_id worked.
func FromContext(ctx context.Context, base *slog.Logger) *slog.Logger {
	if id, ok := ctx.Value(requestIDKey).(string); ok && id != "" {
		return base.With(slog.String("request_id", id))
	}
	return base.With(slog.String("request_id", "-"))
}
