package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"infinity/internal/logging"
)

// responseRecorder captures the status code so it can be logged after
// the handler runs (net/http doesn't expose it otherwise).
type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (r *responseRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// LoggingMiddleware assigns a request ID and logs one structured line
// per request (method, path, status, duration) - equivalent to the
// @app.middleware("http") log_requests function in main.py.
func LoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := uuid.NewString()
			ctx := logging.WithRequestID(r.Context(), requestID)
			r = r.WithContext(ctx)

			rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
			rec.Header().Set("X-Request-ID", requestID)

			start := time.Now()
			next.ServeHTTP(rec, r)
			duration := time.Since(start)

			logging.FromContext(ctx, logger).Info("request completed",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status_code", rec.status),
				slog.Float64("duration_ms", float64(duration.Microseconds())/1000.0),
			)
		})
	}
}
