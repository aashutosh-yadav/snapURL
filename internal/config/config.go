package config

import "os"

// Config holds everything the app needs to connect to its dependencies.
// Kept as plain env-var lookups (no external config library) to mirror
// the original project's use of python-dotenv - simple and dependency-free.
type Config struct {
	DatabaseURL string
	RedisAddr   string
	Port        string
}

func Load() Config {
	return Config{
		DatabaseURL: getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/infinity?sslmode=disable"),
		RedisAddr:   getEnv("REDIS_ADDR", "localhost:6379"),
		Port:        getEnv("PORT", "8000"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
