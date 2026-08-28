// Package store wraps Postgres access for the url_shortener table.
// Equivalent to database.py + models.py in the original Python version.
package store

import (
	"database/sql"
	"errors"
	"fmt"

	_ "github.com/lib/pq"
)

// ErrNotFound is returned when a short code has no matching row.
var ErrNotFound = errors.New("short code not found")

type URL struct {
	ID        int64
	ShortCode string
	LongURL   string
}

type Store struct {
	db *sql.DB
}

// New opens a Postgres connection pool and ensures the schema exists.
// The CREATE TABLE IF NOT EXISTS mirrors what SQLAlchemy's
// Base.metadata.create_all(bind=engine) did in the Python version.
func New(databaseURL string) (*Store, error) {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("opening db: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("pinging db: %w", err)
	}

	schema := `
		CREATE TABLE IF NOT EXISTS url_shortener (
			id SERIAL PRIMARY KEY,
			short_code VARCHAR UNIQUE NOT NULL,
			long_url VARCHAR UNIQUE NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_url_shortener_short_code ON url_shortener (short_code);
		CREATE INDEX IF NOT EXISTS idx_url_shortener_long_url ON url_shortener (long_url);
	`
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("creating schema: %w", err)
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// GetByLongURL mirrors the existing_url lookup in shorten_url - used to
// avoid generating a new short code for a URL that's already shortened.
func (s *Store) GetByLongURL(longURL string) (*URL, error) {
	row := s.db.QueryRow(
		`SELECT id, short_code, long_url FROM url_shortener WHERE long_url = $1`,
		longURL,
	)
	var u URL
	if err := row.Scan(&u.ID, &u.ShortCode, &u.LongURL); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

// GetByShortCode mirrors the db_url lookup in redirect_url.
func (s *Store) GetByShortCode(shortCode string) (*URL, error) {
	row := s.db.QueryRow(
		`SELECT id, short_code, long_url FROM url_shortener WHERE short_code = $1`,
		shortCode,
	)
	var u URL
	if err := row.Scan(&u.ID, &u.ShortCode, &u.LongURL); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

// Create inserts a new short_code -> long_url mapping.
func (s *Store) Create(shortCode, longURL string) (*URL, error) {
	row := s.db.QueryRow(
		`INSERT INTO url_shortener (short_code, long_url) VALUES ($1, $2) RETURNING id`,
		shortCode, longURL,
	)
	var id int64
	if err := row.Scan(&id); err != nil {
		return nil, err
	}
	return &URL{ID: id, ShortCode: shortCode, LongURL: longURL}, nil
}
