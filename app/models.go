package main

// URL maps to a row in the url_shortener table.
type URL struct {
	ID        int64
	ShortCode string
	LongURL   string
}
