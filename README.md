# High-Performance URL Shortener

A production-inspired, read-heavy URL shortener built with **Go**, **PostgreSQL**, and **Redis**, with a focus on performance engineering, scalability, and system design.

This project is an engineering exploration rather than a feature-driven product. The goal is to understand real-world system behavior through benchmarking, bottleneck analysis, and iterative optimization — not to accumulate features.

> **Migration note:** this project started as a Python/FastAPI service. It was rewritten in Go to move closer to how production-grade infra services (etcd, CockroachDB-adjacent tooling) are typically built, and to get real compiled-binary performance characteristics rather than an interpreted-runtime baseline. The benchmark numbers below are from the original Python version and are kept for historical reference — see [Benchmarking](#benchmarking) for why they need to be re-run post-migration.

## Features

- High-performance redirect service (~1,900 RPS under load, Python baseline — Go re-benchmark pending)
- Cache-aside Redis integration, with an early-return-on-hit fix that the original Python version was missing (see Key Insights)
- Detailed performance benchmarking using `hey`
- Idempotent URL shortening (same URL maps to the same short code)
- Structured JSON logging with per-request tracing via request ID
- Simple frontend for manual testing

## Tech Stack

| Layer | Technology |
|---|---|
| Backend | Go (standard library `net/http`) |
| Database | PostgreSQL (indexed lookups, via `database/sql` + `lib/pq`) |
| Cache | Redis (`go-redis`) |
| Logging | Structured JSON via `log/slog` |
| Benchmarking | hey |

## Architecture

```
Client
  ↓
Go HTTP server (net/http, stdlib routing)
  ↓
Redis (Cache, cache-aside)
  ↓
PostgreSQL (Source of Truth)
```

## Performance Highlights (Python baseline — pre-migration)

| Scenario | RPS | Avg Latency | p99 |
|---|---|---|---|
| Single Worker | ~317 | ~305 ms | ~657 ms |
| 4 Workers | ~1,919 | ~49 ms | ~115 ms |
| Redis (Warm) | ~1,500 | ~58 ms | ~130 ms |

*These numbers predate both the Go rewrite and the cache-aside bug fix — see Key Insights #2 below. They're kept here as the "before" baseline for comparison, not as current numbers for this codebase.*

## Key Insights

### 1. Worker Scaling Outweighs Other Optimizations
Moving from 1 to 4 workers improved throughput by roughly 6x in the original Python version. The primary bottleneck was worker contention, not the database.

### 2. The Original Cache-Aside Implementation Had a Bug
The "Redis Did Not Improve Performance" result above was measured against a version of `redirect_url` that queried Postgres on every request regardless of cache hit or miss — the cache lookup result was never used to skip the DB call. That bug is fixed in this codebase. The Redis numbers above should be treated as invalid until re-benchmarked against the corrected logic.

### 3. Caching Is Context-Dependent
Caching does not universally improve performance. Redis becomes valuable primarily when:
- The database is remote (network latency dominates)
- The database is under heavy load
- The system is distributed across multiple instances

### 4. System Saturation Behavior
At high concurrency (c=500) in the Python version, RPS dropped to ~983 and p99 latency exceeded ~800 ms, driven by request queueing and CPU saturation. Whether Go's concurrency model shifts this saturation point is an open question for re-benchmarking.

## Benchmarking

All tests were performed using:

```bash
hey -n 5000 -c 100 -disable-redirects http://127.0.0.1:8000/{short_code}
```

**Status: needs re-running.** The table above is the pre-migration Python baseline. Now that the service is Go with the cache bug fixed, re-run the same `hey` command against this codebase (cold and warm cache) and replace the table above with real numbers before drawing any conclusions about Go vs Python performance.

## Setup

### 1. Clone the repository
```bash
git clone https://github.com/aashutosh-yadav/Infinity
cd Infinity
```

### 2. Install Go
Requires Go 1.22+. Verify with:
```bash
go version
```

### 3. Set environment variables
```bash
export DATABASE_URL="postgres://postgres:postgres@localhost:5432/infinity?sslmode=disable"
export REDIS_ADDR="localhost:6379"
export PORT=8000
```

### 4. Install dependencies and build
```bash
go mod download
go build -o infinity-server ./cmd/server
```

### 5. Run the backend
```bash
./infinity-server
# or, without building a binary first:
go run ./cmd/server
```

The server creates the `url_shortener` table automatically on first run if it doesn't already exist.

### 6. Open the frontend
Visit `http://localhost:8000/` in a browser — the Go server serves `frontend/index.html` directly.

## What This Project Demonstrates

- Performance benchmarking and analysis
- Identifying real bottlenecks rather than assumed ones
- Understanding concurrency and worker scaling
- Critically evaluating caching strategies
- Applying real-world system design thinking
- Migrating a working service to a different language while preserving behavior and fixing a real bug found along the way

## Future Improvements

- Re-benchmark this Go version and record real numbers (see Benchmarking above)
- Deploy the database remotely to validate caching benefits
- Add a load balancer and multi-instance setup
- Implement an analytics pipeline for click tracking
- Introduce distributed ID generation (Snowflake) — see `SYSTEM-DESIGN.md`
- Introduce consistent-hash-based sharding — see `SYSTEM-DESIGN.md`
- Add rate limiting and abuse protection

## Key Takeaway

> Optimization must be driven by measured bottlenecks, not assumptions.

## Contributing

If you find this useful, consider starring the repository, or forking it to experiment further.
