// Package handlers implements the HTTP endpoints, equivalent to the
// route functions in the original main.py.
package handlers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"infinity/internal/cache"
	"infinity/internal/logging"
	"infinity/internal/shortcode"
	"infinity/internal/store"
)

type Handlers struct {
	store  *store.Store
	cache  *cache.Cache
	logger *slog.Logger
}

func New(s *store.Store, c *cache.Cache, logger *slog.Logger) *Handlers {
	return &Handlers{store: s, cache: c, logger: logger}
}

type shortenRequest struct {
	LongURL string `json:"long_url"`
}

type shortenResponse struct {
	ShortCode string `json:"short_code"`
	LongURL   string `json:"long_url"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]string{"detail": detail})
}

// ShortenURL is the equivalent of POST /shorten in main.py:
//  1. If the long URL already has a short code, return the existing one
//     (idempotent shortening).
//  2. Otherwise generate a new short code and insert it.
func (h *Handlers) ShortenURL(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logging.FromContext(ctx, h.logger)

	var req shortenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.LongURL == "" {
		writeError(w, http.StatusUnprocessableEntity, "long_url is required")
		return
	}

	existing, err := h.store.GetByLongURL(req.LongURL)
	if err == nil {
		log.Info("shorten: existing short code reused", slog.String("short_code", existing.ShortCode))
		writeJSON(w, http.StatusOK, shortenResponse{ShortCode: existing.ShortCode, LongURL: existing.LongURL})
		return
	}
	if !errors.Is(err, store.ErrNotFound) {
		log.Error("shorten: db lookup failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	code, err := shortcode.GenerateShortCode()
	if err != nil {
		log.Error("shorten: short code generation failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	created, err := h.store.Create(code, req.LongURL)
	if err != nil {
		log.Error("shorten: db insert failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	log.Info("shorten: new short code created", slog.String("short_code", created.ShortCode))
	writeJSON(w, http.StatusOK, shortenResponse{ShortCode: created.ShortCode, LongURL: created.LongURL})
}

// RedirectURL is the equivalent of GET /{short_code} in main.py, including
// the cache-aside fix: a cache hit returns immediately and never touches
// the database.
func (h *Handlers) RedirectURL(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logging.FromContext(ctx, h.logger)
	shortCode := r.PathValue("short_code")

	// 1. Try cache first (fast path)
	cachedURL, hit, err := h.cache.Get(ctx, shortCode)
	if err != nil {
		log.Warn("redis unavailable on read, falling back to db", slog.String("short_code", shortCode))
	}

	// Cache HIT -> return immediately, skip the DB entirely.
	if hit {
		log.Info("cache hit", slog.String("short_code", shortCode))
		http.Redirect(w, r, cachedURL, http.StatusTemporaryRedirect)
		return
	}

	log.Info("cache miss", slog.String("short_code", shortCode))

	// 2. Cache MISS -> fall back to db.
	dbURL, err := h.store.GetByShortCode(shortCode)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			log.Info("short code not found", slog.String("short_code", shortCode))
			writeError(w, http.StatusNotFound, "URL not found")
			return
		}
		log.Error("redirect: db lookup failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// 3. Populate cache after DB read (lazy caching) so the next request is a hit.
	if err := h.cache.Set(ctx, shortCode, dbURL.LongURL); err != nil {
		log.Warn("redis unavailable on write, cache not populated", slog.String("short_code", shortCode))
	}

	http.Redirect(w, r, dbURL.LongURL, http.StatusTemporaryRedirect)
}
