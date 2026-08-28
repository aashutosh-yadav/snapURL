// Command server runs the Infinity URL shortener API.
// This is the Go equivalent of the original Python app/main.py entrypoint.
package main

import (
	"log/slog"
	"net/http"
	"os"

	"infinity/internal/cache"
	"infinity/internal/config"
	"infinity/internal/handlers"
	"infinity/internal/logging"
	"infinity/internal/server"
	"infinity/internal/store"
)

func main() {
	logger := logging.New(slog.LevelInfo)
	cfg := config.Load()

	db, err := store.New(cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to connect to database", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer db.Close()

	redisCache := cache.New(cfg.RedisAddr)
	defer redisCache.Close()

	h := handlers.New(db, redisCache, logger)
	router := server.NewRouter(h, logger, "frontend")

	logger.Info("starting server", slog.String("port", cfg.Port))
	if err := http.ListenAndServe(":"+cfg.Port, router); err != nil {
		logger.Error("server stopped", slog.String("error", err.Error()))
		os.Exit(1)
	}
}
