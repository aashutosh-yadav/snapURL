package main

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// dbPool is the app-wide PostgreSQL connection pool (the source of truth).
//
// Pool sizing mirrors the Python app: pool_size=8 + max_overflow=4, i.e. a
// ceiling of 12 simultaneous database connections per process.
var dbPool *pgxpool.Pool

func connectDatabase(ctx context.Context) error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("DATABASE_URL is not set")
	}

	config, err := pgxpool.ParseConfig(databaseURLWithSSL(databaseURL))
	if err != nil {
		return err
	}

	// 8 pool + 4 overflow = 12 connections max, same as the Python pool.
	config.MaxConns = 12

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return err
	}

	// Connect eagerly, the way the Python app's create_all did at startup,
	// so a bad DATABASE_URL fails fast instead of on the first request.
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return err
	}

	dbPool = pool
	return nil
}

// databaseURLWithSSL accepts a plain postgresql:// URL (pgx understands it
// natively -- no driver-specific scheme rewrite needed). Remote hosts
// (Neon etc.) get sslmode=require appended unless the URL already sets one;
// local hosts need no SSL, same as the Python app's connect_args.
func databaseURLWithSSL(databaseURL string) string {
	isLocal := strings.Contains(databaseURL, "localhost") || strings.Contains(databaseURL, "127.0.0.1")
	if isLocal || strings.Contains(databaseURL, "sslmode=") {
		return databaseURL
	}

	separator := "?"
	if strings.Contains(databaseURL, "?") {
		separator = "&"
	}
	return databaseURL + separator + "sslmode=require"
}

const createTableSQL = `
CREATE TABLE IF NOT EXISTS url_shortener (
    id SERIAL PRIMARY KEY,
    short_code TEXT UNIQUE,
    long_url TEXT UNIQUE
);`

func ensureSchema(ctx context.Context) error {
	_, err := dbPool.Exec(ctx, createTableSQL)
	if err == nil {
		return nil
	}

	message := err.Error()

	// If two instances start at the same time, one creates the table first.
	// The other then gets an "already exists" error, which is harmless.
	alreadyCreated := strings.Contains(message, "already exists") || strings.Contains(message, "duplicate key")

	if alreadyCreated {
		log.Println("Tables were already created by another worker. Continuing.")
		return nil
	}

	// Any other error is a real problem, so stop the startup.
	return err
}
