package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
)

var (
	l1Cache = NewLRUCache(1024, 60*time.Second)
	stats   = NewStats()
)

func main() {
	loadEnv()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ---------- STARTUP ----------
	if err := connectDatabase(ctx); err != nil {
		log.Fatalf("database: %v", err)
	}
	defer dbPool.Close()

	if err := ensureSchema(ctx); err != nil {
		log.Fatalf("create tables: %v", err)
	}

	connectRedis()

	// ---------- APP RUNS ----------
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", serveFrontend)
	mux.HandleFunc("GET /stats", getStats)
	mux.HandleFunc("POST /seed_cold", seedCold)
	mux.HandleFunc("POST /shorten", shortenURL)
	mux.HandleFunc("GET /{short_code}", redirectURL)

	server := &http.Server{
		Addr:              ":" + port(),
		Handler:           withCORS(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("infinity listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	select {
	case err := <-serverErrors:
		log.Fatalf("server: %v", err)

	case <-ctx.Done():
		// ---------- SHUTDOWN ----------
		log.Println("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}
}

// root
func serveFrontend(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "app/frontend/index.html")
}

func getStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, stats.Snapshot())
}

func seedCold(w http.ResponseWriter, r *http.Request) {
	n := 300
	if raw := r.URL.Query().Get("n"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusUnprocessableEntity, "n must be a non-negative integer")
			return
		}
		n = parsed
	}

	// Unique codes, same as building a set in the Python version.
	codes := make([]string, 0, n)
	seen := make(map[string]struct{}, n)
	for len(codes) < n {
		code := generateShortCode(6)
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}

	longURLs := make([]string, n)
	for i := range longURLs {
		longURLs[i] = "https://example.com/cold-" + uuid.NewString()
	}

	_, err := dbPool.Exec(r.Context(), `
		INSERT INTO url_shortener (short_code, long_url)
		SELECT * FROM unnest($1::text[], $2::text[])
		ON CONFLICT DO NOTHING`, codes, longURLs)
	if err != nil {
		log.Printf("seed_cold: %v", err)
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	writeJSON(w, http.StatusOK, struct {
		Codes []string `json:"codes"`
		Count int      `json:"count"`
	}{Codes: codes, Count: len(codes)})
}

// Create short URL
func shortenURL(w http.ResponseWriter, r *http.Request) {
	var body URLCreate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.LongURL == "" {
		writeError(w, http.StatusUnprocessableEntity, "long_url is required")
		return
	}

	ctx := r.Context()

	// Check if URL already exists.
	// If the url already exists then return the same short code previously
	// generated and don't generate a new code for the same url repeatedly.
	var existing URL
	err := dbPool.QueryRow(ctx,
		`SELECT id, short_code, long_url FROM url_shortener WHERE long_url = $1`,
		body.LongURL,
	).Scan(&existing.ID, &existing.ShortCode, &existing.LongURL)
	if err == nil {
		writeJSON(w, http.StatusOK, URLResponse{ShortCode: existing.ShortCode, LongURL: existing.LongURL})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		log.Printf("shorten: lookup: %v", err)
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	var created URL
	err = dbPool.QueryRow(ctx,
		`INSERT INTO url_shortener (short_code, long_url) VALUES ($1, $2)
		 RETURNING id, short_code, long_url`,
		generateShortCode(6), body.LongURL,
	).Scan(&created.ID, &created.ShortCode, &created.LongURL)
	if err != nil {
		log.Printf("shorten: insert: %v", err)
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	writeJSON(w, http.StatusOK, URLResponse{ShortCode: created.ShortCode, LongURL: created.LongURL})
}

func redirectURL(w http.ResponseWriter, r *http.Request) {
	shortCode := r.PathValue("short_code")
	ctx := r.Context()

	// 1. Try L1 (in-process) cache first -- no network hop at all
	if longURL, ok := l1Cache.Get(shortCode); ok {
		stats.L1Hit()
		redirect(w, longURL)
		return
	}

	// 2. Try L2 (Redis) cache. A miss and an outage look the same here and
	// both fall through to the database -- never let a cache being down
	// break the app.
	if cachedURL, _ := redisClient.Get(ctx, shortCode).Result(); cachedURL != "" {
		stats.L2Hit()
		l1Cache.Set(shortCode, cachedURL) // promote into L1 for next time
		redirect(w, cachedURL)
		return
	}

	// 3. Cache miss on both levels -> fallback to db
	var dbURL URL
	err := dbPool.QueryRow(ctx,
		`SELECT id, short_code, long_url FROM url_shortener WHERE short_code = $1`,
		shortCode,
	).Scan(&dbURL.ID, &dbURL.ShortCode, &dbURL.LongURL)
	if errors.Is(err, pgx.ErrNoRows) {
		stats.Miss()
		writeError(w, http.StatusNotFound, "URL not found")
		return
	}
	if err != nil {
		log.Printf("redirect: %v", err)
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	stats.DBHit()

	// 4. Populate both cache levels (lazy caching)
	_ = redisClient.Set(ctx, shortCode, dbURL.LongURL, time.Hour).Err() // never let cache break the app
	l1Cache.Set(shortCode, dbURL.LongURL)

	redirect(w, dbURL.LongURL)
}

// withCORS mirrors the FastAPI CORSMiddleware setup: every origin allowed,
// credentials allowed. Starlette can't send a literal "*" together with
// credentials, so the request origin is echoed back instead.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}

		header := w.Header()
		header.Set("Access-Control-Allow-Origin", origin) // allow any domain (risky)
		header.Set("Access-Control-Allow-Credentials", "true")
		header.Add("Vary", "Origin")

		if r.Method == http.MethodOptions {
			header.Set("Access-Control-Allow-Methods", "DELETE, GET, HEAD, OPTIONS, PATCH, POST, PUT")
			if requested := r.Header.Get("Access-Control-Request-Headers"); requested != "" {
				header.Set("Access-Control-Allow-Headers", requested)
				header.Add("Vary", "Access-Control-Request-Headers")
			}
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func redirect(w http.ResponseWriter, longURL string) {
	w.Header().Set("Location", longURL)
	w.WriteHeader(http.StatusTemporaryRedirect) // 307, same as Starlette's RedirectResponse
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("encode response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]string{"detail": detail})
}

func loadEnv() {
	// .env is optional: real deployments inject env vars directly.
	_ = godotenv.Load()
}

func port() string {
	// Render/Railway/Fly inject $PORT; default to 8000 locally.
	if p := os.Getenv("PORT"); p != "" {
		return p
	}
	return "8000"
}
