# Go port: benchmark findings and worker-model notes

This document records what was actually measured after porting Infinity from
Python (FastAPI) to Go, and what those measurements mean. Every number below
came from a real run on the development laptop, with the same PostgreSQL and
Redis instances, the same request path, and the same load-testing tools used
against both implementations.

Nothing here is theoretical: if a claim wasn't observed in a run, it isn't
listed as a finding.

## Test environment

| Item | Value |
| --- | --- |
| CPU | Intel i5-1155G7 (11th Gen, 8 logical threads @ 2.5GHz) |
| RAM | ~6.9GB usable |
| Disk | NVMe SSD |
| OS state | Full desktop session running throughout (Chrome, editor, etc.) — not a dedicated or isolated server |
| Postgres | Local, `max_connections = 200` |
| Redis | Local |
| Go | 1.27.1 |
| Python | FastAPI + uvicorn, 8 workers |
| Clients | `hey` (hot path), `vegeta` v12 (cold path) |

Both implementations ran the same architecture: L1 in-process LRU with a 60s
TTL → L2 Redis with a 3600s TTL → PostgreSQL. The Go port is a behavior
preserving rewrite, so any difference below is language/runtime, not design.

## Headline results

### Hot path — L1 cache hit

`hey -n 20000 -c 100 -disable-redirects http://127.0.0.1:<port>/<warm_code>`

| Implementation | Throughput | Average latency | p50 | p95 | p99 |
| --- | --- | --- | --- | --- | --- |
| Python, 8 uvicorn workers | ~16,000 req/s | 5.4–6.0ms | 4.6–5.3ms | 11.4–12.5ms | 16.2–18.4ms |
| Go, 1 process | **~85,000 req/s** | **1.1ms** | **0.7–0.8ms** | **3.3ms** | **5.1–5.3ms** |

Roughly **5.2× the throughput** and **~6× lower p50 latency**, from a single
process instead of eight. Three consecutive rounds per implementation:

| Round | Python | Go |
| --- | --- | --- |
| 1 | 17,481 req/s | 85,138 req/s |
| 2 | 15,678 req/s | 82,487 req/s |
| 3 | 16,228 req/s | 85,183 req/s |

### Cold path — guaranteed cache miss, straight to PostgreSQL

Unique, never-before-requested codes, so every request is a genuine L1 + L2
miss. `vegeta attack -rate=0 -max-workers=100 -redirects=-1 -duration=4s`.

| Implementation | Throughput | Mean latency | p50 | p95 | p99 |
| --- | --- | --- | --- | --- | --- |
| Python, 8 workers | ~3,016 req/s | 32.9ms | 29.2ms | 54.7ms | 133.8ms |
| Go, 1 process | **~32,100 req/s** | **2.7ms** | **2.2ms** | **6.4ms** | **9.0ms** |

Roughly **10.7× the throughput**. Both runs were 100% successful.

> **Methodology note.** An earlier cold attempt was contaminated: the client
> cycled back onto already-warmed codes, which inflated both numbers and made
> the comparison meaningless. The results above use 60,000 freshly seeded,
> disjoint codes per implementation, and the attack total stays below that
> count. This is the same trap the original Python project hit and documented.

## Why the Go version is faster

1. **No interpreter, no GIL.** Python pays bytecode dispatch, object
   allocation, and reference counting on every request. The Go handler is
   compiled machine code, and the runtime schedules goroutines across all
   cores without a global lock.

2. **Goroutines instead of process-per-worker.** 100 concurrent connections
   are ~100 goroutines of a few KB each inside one process. The Python setup
   needs 8 separate OS processes, each with its own interpreter, event loop,
   and (importantly) its own L1 cache.

3. **Less framework per request.** The Python request traverses Starlette's
   middleware stack, Pydantic response-model validation, and SQLAlchemy ORM
   machinery. The Go path is a `ServeMux` lookup, a mutex-guarded map read,
   and a 307 write. Most of Python's ~4.5ms p50 was overhead, not work —
   Go's p50 is ~0.7ms.

4. **Cheaper database round-trips.** `pgx` speaks the Postgres wire protocol
   directly and reuses prepared statements over a small pool. `asyncpg`
   behind SQLAlchemy adds a translation layer per query. This shows up most
   on the cold path (33ms → 2.7ms mean).

5. **Cheaper Redis calls.** `go-redis` reuses pooled connections with no
   asyncio task/await overhead per call. Redis was never the bottleneck;
   the client-side plumbing was.

6. **Lower allocation pressure.** Go allocates little per request and
   collects in parallel. CPython builds and tears down an object graph per
   request under the GIL, which is measurable at 16k req/s.

## Worker model: what `--workers` means in each language

The original Python deployment ran `uvicorn --workers 8`. The Go port runs a
**single process**. That difference is not an oversight — it follows from how
each runtime uses CPU.

### Python

Each worker is a separate OS process with its own interpreter and GIL. To use
8 cores, you must run 8 processes. The costs:

- 8× the interpreter memory
- 8 separate L1 caches, so the same traffic produces a lower effective hit rate
- 8 separate sets of `/stats` counters (the original README's caveat)

### Go

One process already uses every core: goroutines are multiplexed onto
`GOMAXPROCS` OS threads by the runtime scheduler. Adding processes duplicates
state without adding CPU capacity.

### Measured scaling

Benchmarked against a throwaway database, with `SO_REUSEPORT` so several
processes could share one port (benchmark-only patch; the shipped app binds a
single port).

| Setup | Hot path | Cold path |
| --- | --- | --- |
| 1 process, default `GOMAXPROCS` (8) | ~78–81k req/s | ~20k req/s |
| 4 processes, default `GOMAXPROCS` | ~81–83k req/s | — |
| 1 process, `GOMAXPROCS=1` | ~76–79k req/s | ~20k req/s |
| 4 processes, `GOMAXPROCS=1` | ~84–91k req/s | **~41k req/s** |
| Python, 8 workers | ~16k req/s | ~3k req/s |

What this shows:

- **Hot path is not CPU-bound at these rates.** One process with a single
  core (`GOMAXPROCS=1`) still reached ~77k req/s. The limit is the client and
  loopback, not the server: during a 4-process run the server processes sat
  at roughly 7% CPU, and two parallel `hey` clients totaled ~83k — the same
  as one. Treat ~80–90k as the ceiling of this test setup, not of the server.
- **Cold path scales with processes.** Each miss blocks waiting on Postgres,
  so more independent connection pools mean more requests in flight:
  ~20k → ~41k req/s with 4 processes.
- **Processes cost database connections.** Each process gets its own pool
  (`MaxConns = 12` in this port). Four processes = up to 48 connections
  against Postgres's 200 limit. This is exactly the class of bug the original
  project found on the cold path, where `pool_size × workers` exceeded
  `max_connections`. Scale processes only after checking the arithmetic.

### Recommendation

- Run **one Go process** by default. It uses all available cores.
- Add processes only for **cold-heavy or DB-latency-bound** traffic, and
  divide the pool budget accordingly.
- Do not copy `--workers 8` from the Python deployment. In Go it mostly
  duplicates L1 caches and `/stats` counters.
- If you scale out to separate containers, Redis becomes the shared cache
  layer and `/stats` becomes per-replica again.

## `GOMAXPROCS` in practice

`GOMAXPROCS` is the number of OS threads the Go runtime will use to execute
Go code simultaneously. It defaults to the number of logical CPUs
(`runtime.NumCPU()`) — 8 on this laptop.

```go
runtime.GOMAXPROCS(0) // read current value
runtime.GOMAXPROCS(4) // set it
```

Set it explicitly in one situation that matters for deployment: **containers
with CPU limits**. Go reads the host's CPU count, not the cgroup quota, so a
container limited to 1 CPU on an 8-core host may default to `GOMAXPROCS=8`
and thrash. Match it to the limit:

```dockerfile
ENV GOMAXPROCS=1
```

Co-located processes should split the cores (e.g. 4 processes on 8 cores →
`GOMAXPROCS=2` each). Setting it above `NumCPU()` only makes the scheduler
time-slice more threads onto the same cores.

## Caveats on all of the above

- **Loopback, local database.** Client and server share a machine, and
  Postgres is local. Real deployments add network latency to every cache miss
  and every Redis call; the relative gaps will shrink.
- **The Go port has not been deployed or re-benchmarked remotely.** The
  README's ~20k/~3.5k historical figures are from the Python deployment; the
  numbers in this document are local measurements of both implementations on
  the same hardware, on the same day.
- **Hot-path numbers are client-limited.** `hey` is a single-threaded Go
  client. The ~80–90k plateau appears with both `hey` and `vegeta` and with
  multiple parallel clients, which is why it is attributed to the test setup
  rather than the server.
- **Cold-path runs must not cycle back onto warmed codes.** Keep total
  requests under the number of seeded codes, or the measurement silently
  becomes a hot-path measurement.
- **Benchmark runs created and removed throwaway data.** The multi-process
  tests used a separate `url_shortener_bench` database and Redis DB 15, both
  dropped afterwards. The application database was left at its pre-benchmark
  row count, apart from a handful of rows from functional smoke tests.

## Reproducing

```bash
# start the app
cd infinity-go && go run ./app

# hot path: warm one code, then hammer it
CODE=$(curl -s -X POST http://127.0.0.1:8000/shorten \
  -H 'Content-Type: application/json' \
  -d '{"long_url":"https://example.com/bench"}' | jq -r .short_code)
curl -s -o /dev/null http://127.0.0.1:8000/$CODE
hey -n 20000 -c 100 -disable-redirects http://127.0.0.1:8000/$CODE

# cold path: unique code per request, never requested before
curl -s -X POST "http://127.0.0.1:8000/seed_cold?n=20000" \
  | jq -r '.codes[] | "GET http://127.0.0.1:8000/\(.)"' > /tmp/cold.targets
vegeta attack -targets=/tmp/cold.targets -rate=0 -max-workers=100 \
  -redirects=-1 -duration=5s | vegeta report
```

`-disable-redirects` / `-redirects=-1` are mandatory: without them the client
follows the 307 out to `example.com` and reports that unrelated site's
latency and status as if they came from this app.
