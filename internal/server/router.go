package server

import (
	"log/slog"
	"net/http"

	"infinity/internal/handlers"
)

// NewRouter builds the full HTTP handler tree, equivalent to the FastAPI
// app + route registrations in main.py.
//
// Uses Go 1.22's standard-library method-aware routing patterns
// (e.g. "GET /{short_code}"), so no third-party router package is needed.
func NewRouter(h *handlers.Handlers, logger *slog.Logger, frontendDir string) http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /", http.FileServer(http.Dir(frontendDir)))
	mux.HandleFunc("POST /shorten", h.ShortenURL)
	mux.HandleFunc("GET /{short_code}", h.RedirectURL)

	// CORS: allow-all, matching the original main.py's permissive dev config.
	// Tighten allow_origins before this ever runs somewhere real.
	corsWrapped := withCORS(mux)

	return LoggingMiddleware(logger)(corsWrapped)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}
