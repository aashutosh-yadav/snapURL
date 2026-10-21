package main

import (
	"math"
	"sync/atomic"
)

// Simple in-process counters for cache hit-rate visibility.
//
// With the Python implementation these lived per uvicorn worker process.
// The Go port runs a single process, so every goroutine shares these
// counters and /stats is a true aggregate for the process. (Multiple
// replicas would still each keep their own copy.)
type Stats struct {
	l1Hits atomic.Int64
	l2Hits atomic.Int64
	dbHits atomic.Int64
	misses atomic.Int64 // short code not found at all
}

func NewStats() *Stats {
	return &Stats{}
}

func (s *Stats) L1Hit() { s.l1Hits.Add(1) }
func (s *Stats) L2Hit() { s.l2Hits.Add(1) }
func (s *Stats) DBHit() { s.dbHits.Add(1) }
func (s *Stats) Miss()  { s.misses.Add(1) }

// StatsSnapshot is the /stats response body, mirroring main.py's as_dict().
type StatsSnapshot struct {
	TotalRequests int64   `json:"total_requests"`
	L1Hits        int64   `json:"l1_hits"`
	L2Hits        int64   `json:"l2_hits"`
	DBHits        int64   `json:"db_hits"`
	NotFound      int64   `json:"not_found"`
	L1HitRate     float64 `json:"l1_hit_rate"`
	L2HitRate     float64 `json:"l2_hit_rate"`
	DBHitRate     float64 `json:"db_hit_rate"`
}

func (s *Stats) Snapshot() StatsSnapshot {
	l1 := s.l1Hits.Load()
	l2 := s.l2Hits.Load()
	db := s.dbHits.Load()
	misses := s.misses.Load()
	total := l1 + l2 + db + misses

	rate := func(count int64) float64 {
		if total == 0 {
			return 0.0
		}
		return math.Round(float64(count)/float64(total)*10000) / 10000
	}

	return StatsSnapshot{
		TotalRequests: total,
		L1Hits:        l1,
		L2Hits:        l2,
		DBHits:        db,
		NotFound:      misses,
		L1HitRate:     rate(l1),
		L2HitRate:     rate(l2),
		DBHitRate:     rate(db),
	}
}
