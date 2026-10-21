# Infinity

A URL shortener built to go deep on real backend engineering — caching correctness, concurrency behavior, database connection limits, and honest benchmarking — rather than to just ship a working redirect endpoint. The feature set is intentionally simple; the point was understanding, and measuring, what's underneath it.

This repository is the **Go port** of the original FastAPI implementation: same endpoints, same behavior, same two-level cache, same single-file frontend. Only the language changed.

## What this actually is

Most URL shortener tutorials stop at "map a short code to a long URL." This project treats that as the starting point. Every claim below was arrived at by actually breaking something, measuring it, and — more than once — discovering the first explanation was wrong. That process (documented in full in the running project log) is the actual point of the project, not the feature list.

## Features

- `POST /shorten` — idempotent short URL creation (the same long URL always returns the same short code)
- `GET /{short_code}` — redirect, backed by a two-level cache
- `GET /stats` — live cache hit-rate visibility (L1 / L2 / DB split)
- Forced cold-path benchmarking: `POST /seed_cold?n=...` seeds never-before-requested codes straight into Postgres, so both the browser tool and `vegeta` can measure a guaranteed cache-miss path
- Concurrent request path — one goroutine per request, no blocking calls on the hot path
- Multi-level caching: in-process LRU (L1) → Redis (L2) → PostgreSQL (source of truth)
- A live, in-browser benchmark tool in the frontend — fires real concurrent requests from your own browser and shows real, measured throughput/latency, not canned numbers

## Tech stack

| Layer | Technology |
| --- | --- |
| API | Go standard library (`net/http`) |
| Database | PostgreSQL, via `pgx/v5` + `pgxpool` |
| Cache (L2) | Redis, via `go-redis/v9` |
| Cache (L1) | Hand-built in-process LRU with TTL |
| Load testing | `hey`, `vegeta` |

## Architecture

```
Client
  │
  ▼
Go server (goroutine per request)
  │
  ├─► L1 cache (in-process LRU, shared) ─── hit? return immediately
  │
  ├─► L2 cache (Redis) ─── hit? promote to L1, return
  │
  └─► PostgreSQL (source of truth) ─── populate L1 + L2, return
```

**Note on processes:** the original Python implementation ran multiple `uvicorn` worker processes, each with its **own** L1 cache and its **own** `/stats` counters. The Go port runs as a **single process**: every goroutine shares one L1 cache and one set of counters, so `/stats` is a true aggregate for the process. If you run multiple replicas (e.g. several containers behind a load balancer), each replica still keeps its own L1 and counters.

## Architecture in detail

### Request lifecycle (`GET /{short_code}`)

```
Client
  │
  ▼
┌──────────────────────────────────────────────────┐
│ Go server (one process, goroutine per request)     │
│                                                   │
│  1. L1 lookup (in-process LRU, TTL 60s)          │──── hit ──► 307 redirect  (stats: l1_hits)
│         │ miss                                    │
│         ▼                                         │
│  2. L2 lookup (Redis, TTL 3600s)                 │──── hit ──► promote into L1 ──► 307 redirect
│         │ miss                                    │                             (stats: l2_hits)
│         ▼                                         │
│  3. PostgreSQL (source of truth, indexed)        │
│         │ row found?                              │──── hit ──► populate L1 + L2 ──► 307 redirect
│         │ no                                      │                             (stats: db_hits)
│         ▼                                         │
│     404 Not Found                                │             (stats: misses → not_found)
└──────────────────────────────────────────────────┘
```

Every mutating call also **populates both cache levels** (lazy/write-through-on-read), so a cold miss warms L2 and L1 for all subsequent requests of the same code.

### Why three levels exist

| Level | Where it lives | Latency | Scope | Why it's there |
| --- | --- | --- | --- | --- |
| L1 | In-process LRU (Go map + list) | ~microseconds | One process | Skips the network entirely for the hottest codes |
| L2 | Redis | ~0.2ms local / a few ms remote | Shared across processes | One warm copy serves every goroutine, including ones whose L1 is still cold |
| Postgres | Managed database | Single-digit ms | Source of truth | Durability; also what a genuine cache miss must fall back to |

The L1 TTL (60s) is deliberately shorter than Redis's (3600s): L1 is a "thin, fast skim," not the freshness authority. If a link is rewritten elsewhere, worst case it stays stale in a process's L1 for a minute.

### Hot path vs cold path (what the benchmark tool measures)

```
HOT:   client → L1 → redirect                              (~4.5ms, ~20k req/s)
COLD:  client → L1✗ → Redis✗ → Postgres → warm L1+L2 → redirect   (~28ms, ~3.5k req/s)
```

Cold path is only honest if every request is a genuine miss — that requires codes nobody has ever requested. That's what `POST /seed_cold` exists for: it inserts never-before-seen codes directly into Postgres, bypassing the caches, so each one is guaranteed cold on first hit. Keep total requests under the number of seeded codes, or the client cycles back and starts measuring a warm path again.

### Deployment topology (production, free tier)

```
Browser / curl / hey / vegeta
        │
        ▼
┌─────────────────────┐       ┌──────────────┐
│   Render (app)       │       │              │
│  Go binary (1 process)──────►│ Neon          │── source of truth (Postgres)
│  net/http + L1 cache │       │  (managed)    │     (SSL required for remote)
└─────────┬───────────┘       └──────────────┘
          │
          ▼
┌─────────────────────┐
│ Upstash (Redis)      │── L2 cache, rediss:// (TLS)
└─────────────────────┘
```

- All three services run in the **same region (AWS US East 2, Ohio)** so inter-service hops stay cheap.
- `DATABASE_URL` and `REDIS_URL` come from env vars; `database.go` accepts a plain `postgresql://` URL (pgx understands it natively) and appends `sslmode=require` for remote hosts; `redis_client.go` accepts a full `redis://` / `rediss://` URL.
- On a free/sleeping tier, the first request after idle takes ~30s; L1 starts empty at boot, so early traffic misses harder than steady-state traffic.

### Request/response contract, briefly

- `POST /shorten` — idempotent: same `long_url` always returns the same `short_code` (existing row, no rewrite).
- `GET /{short_code}` — `307` redirect; cache layers are branch-on-hit (a value read but never checked is dead code, not a cache).
- `GET /stats` — counters: `total_requests`, `l1_hits`, `l2_hits`, `db_hits`, `not_found`, and hit rates (single process, so a true aggregate).
- `POST /seed_cold?n=...` — bulk-insert N fresh codes directly into Postgres, returns the list; powers the browser's cold-path benchmark.

## Performance

Benchmarked on a personal laptop — Intel i5-1155G7 (11th Gen, 8 threads @ 2.5GHz), ~6.9GB usable RAM, NVMe SSD — actively running a full desktop session throughout testing, not a dedicated or isolated server. All numbers are from real `hey`/`vegeta` runs against a running instance. Full methodology, every intermediate result, and every wrong turn are in [`BENCHMARK.md`](./BENCHMARK.md).

### Go port vs Python original (measured side by side)

Same machine, same Postgres, same Redis, same request path, same tools:

| Path | Python (8 uvicorn workers) | Go (1 process) | Gap |
| --- | --- | --- | --- |
| **Hot** (cache hit) | ~16,000 req/sec, p50 ~4.6ms | **~85,000 req/sec, p50 ~0.7ms** | ~5.2× |
| **Cold** (guaranteed miss → PostgreSQL) | ~3,000 req/sec, mean ~33ms | **~32,000 req/sec, mean ~2.7ms** | ~10.7× |

One Go process replaces eight uvicorn workers. See `BENCHMARK.md` for the reasoning, the worker-model scaling numbers, and the caveats.

### Historical Python deployment

The original deployed Python version measured:

| Path | Throughput | Avg latency | Success rate |
| --- | --- | --- | --- |
| **Hot** (cache hit, realistic multi-key traffic) | ~20,400 req/sec | ~4.5ms | 100% |
| **Cold** (guaranteed cache miss, straight to PostgreSQL) | ~3,500 req/sec | ~28ms | 100% |

These are two genuinely different numbers, measured separately and on purpose — see "Notable engineering findings" below for why that distinction matters and how the cold-path number was obtained.

One additional result worth highlighting: an earlier, sync (non-async) version of this app collapsed under high concurrency — throughput fell to ~983 req/sec at 500 concurrent connections, down from a healthy ~6,300 req/sec at lower concurrency, with p99 latency blowing out to ~881ms. After the async rewrite, that collapse disappeared entirely: throughput stayed in the 15,000–22,000 req/sec range from 100 concurrent connections all the way up to 2,000, with latency degrading gracefully instead of falling off a cliff.

## Notable engineering findings

A few things this project actually uncovered, not just built:

- **A cache that was never being used.** An early version read from Redis on every redirect but never checked the result before falling through to Postgres — Redis was fully wired up and completely inert. Found via benchmarking, not code review; fixing it roughly quadrupled throughput.
- **A "dropped requests" mystery that wasn't real.** A consistent ~4% of requests appeared to fail at higher concurrency. Two plausible OS-level causes (thread pool exhaustion, kernel socket backlog limits) were tested and ruled out with direct evidence before the real cause turned out to be the load-testing tool rounding its own request count down to a clean multiple of concurrency — not a server-side bug at all.
- **A load-testing tool silently following a redirect to the real internet.** A multi-key benchmark reported 100% failure. The actual cause: the benchmarking tool followed the app's redirect by default, all the way out to the real `example.com` (used as filler destination data), and reported that unrelated site's 404 as if the app under test had failed.
- **A cold-path benchmark that exposed a real connection-limit bug the hot path could never have found.** Cache-hit traffic never touches the database connection pool at all, so a bug in the pool configuration sat invisible through every earlier benchmark. Running genuinely uncached traffic (thousands of never-before-seen short codes, seeded directly into Postgres) immediately produced `TooManyConnectionsError` — the app's connection pool, multiplied across worker processes, could request far more simultaneous connections than PostgreSQL's own `max_connections` allowed. Fixed by right-sizing the app's pool and raising the database's connection ceiling to match.
- **The collapse point disappeared entirely** after moving from a thread-pool-bound sync architecture to a fully async one — documented with a full before/after concurrency sweep.

Full writeups of all of these, including the dead ends, are in the running project log (linked below).

## Live demo

**Deployed: [https://infinity-vpko.onrender.com](https://infinity-vpko.onrender.com)** — free tier (Render web service + Neon Postgres + Upstash Redis); currently running the original Python deployment. Note it may take ~30s on first visit after idle time, since the free tier sleeps.

The frontend includes a "Run Benchmark" button that fires real concurrent requests from your own browser at the running backend and shows live, measured throughput and latency — not a static number, and with both **Hot path** (cache hit) and **Cold path** (guaranteed cache miss, straight to Postgres) modes. It's honestly labeled: browser-driven numbers will be noticeably lower than the table above, because browsers hard-cap concurrent connections per origin in a way dedicated load-testing tools aren't. The gap itself is part of the point — it's demonstrating what a real client can drive against this server, not trying to match a purpose-built benchmarking tool.

## Running it locally

```bash
git clone <repo-url>
cd infinity-go

# requires Go 1.26+ and a local PostgreSQL + Redis
go version
```

Set up PostgreSQL and Redis locally, then create `.env`:

```
DATABASE_URL=postgresql://<user>:<password>@localhost:5432/<dbname>
# optional for local dev; defaults to redis://localhost:6379/0
REDIS_URL=redis://localhost:6379/0
```

Run it:

```bash
go run ./app

# or build a binary and run it:
go build -o infinity ./app && ./infinity
```

Visit `http://127.0.0.1:8000` — the live benchmark tool is right there on the page.

A `Dockerfile` also exists and is what the Render deployment builds from (`docker build -t infinity . && docker run -p 8000:8000 infinity` runs it locally). `docker-compose.yml` was dropped in favor of the simpler direct-Dockerfile path.

## Benchmarking

```bash
# Single-key throughput (hot path)
hey -n 5000 -c 100 -disable-redirects http://127.0.0.1:8000/<short_code>

# Multi-key hot-path traffic: seed codes via the API, warm the caches, then attack
curl -s -X POST "http://127.0.0.1:8000/seed_cold?n=20" | jq -r '.codes[] | "GET http://127.0.0.1:8000/\(.)"' > targets.txt
vegeta attack -targets=targets.txt -rate=50 -max-workers=20 -redirects=-1 -duration=2s > /dev/null  # warm-up pass
vegeta attack -targets=targets.txt -rate=0 -max-workers=100 -redirects=-1 -duration=10s | vegeta report

# True cold-path throughput (guaranteed cache misses, direct to Postgres)
curl -s -X POST "http://127.0.0.1:8000/seed_cold?n=50000" | jq -r '.codes[] | "GET http://127.0.0.1:8000/\(.)"' > cold_targets.txt
vegeta attack -targets=cold_targets.txt -rate=0 -max-workers=100 -redirects=-1 -duration=5s | vegeta report
```

**Important for cold-path testing:** keep the attack's total request count under the number of codes you seeded, or `vegeta` will cycle back and start hitting already-cached codes — no longer a valid cold-path measurement.

## Project structure

```
app/
  main.go          — routes, request handling, cache orchestration, server lifecycle
  database.go      — pgx connection pool, SSL/URL handling, schema creation
  models.go        — row model (URL)
  schemas.go       — request/response shapes
  redis_client.go  — Redis (L2) client, connection via REDIS_URL env var
  l1_cache.go      — in-process LRU cache with TTL (L1)
  stats.go         — cache hit-rate counters
  utils.go         — short code generation
  frontend/index.html — live dashboard + in-browser benchmark tool
go.mod / go.sum    — module definition and pinned dependencies
BENCHMARK.md       — Go vs Python measurements, worker-model analysis, GOMAXPROCS notes
Dockerfile         — production image (what Render builds/deploys)
```

The original Python version's load-testing scripts (`loadtest.py`, `locustfile.py`, `gen_vegeta_targets.py`, `seed_cold_data.py`, `test_l1_ttl.py`) and its `BENCHMARK.md` lived in `testing/` and were **not** carried over into this port; they remain in the original repository, along with the long-form article writeup.

## Honest limitations

This is a portfolio/learning project, and these gaps are known rather than accidental:

- No authentication — anyone can create or look up links
- No rate limiting yet
- No automated tests yet
- No collision-retry on short code generation
- Single machine, single region — no horizontal scaling or CDN layer
- A small number of residual errors (well under 1% of requests) appeared during early cold-path load testing and have not yet been fully root-caused, though the dominant failure mode (database connection exhaustion) is confirmed fixed
- L1 cache TTL behavior has not been specifically load-tested at the expiry boundary
