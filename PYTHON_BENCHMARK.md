# Infinity (Python): Historical Benchmark Record

This document preserves the benchmark results, commands, and findings from the
**original Python/FastAPI implementation** of Infinity. It is kept for
reference and comparison; it does not describe the Go code in this repository.

- Current Go implementation: [`README.md`](./README.md)
- Like-for-like Go vs Python measurements: [`BENCHMARK.md`](./BENCHMARK.md)
- Long-form engineering writeup: [`ARTICLE.md`](./ARTICLE.md)

The original Python source, its `testing/` scripts, and its own `BENCHMARK.md`
live in the original Python repository. The commands below reference those
historical paths.

## Test environment

| Item | Value |
| --- | --- |
| CPU | Intel i5-1155G7 (11th Gen, 8 threads @ 2.5GHz) |
| RAM | ~6.9GB usable |
| Disk | NVMe SSD |
| OS state | Full desktop session running throughout (Chrome, editor, etc.) — not a dedicated or isolated server |
| Postgres | Local for local runs; Neon for the deployed run |
| Redis | Local for local runs; Upstash for the deployed run |
| App | FastAPI + uvicorn, 8 workers locally; 2 workers on the free Render tier |
| Clients | `hey`, `vegeta`, `loadtest.py`, Locust |

The Python app used the same three-level design as the Go port: an in-process
LRU cache (L1, 60s TTL) → Redis (L2, 3600s TTL) → PostgreSQL. The important
difference is the worker model: each uvicorn worker was a separate OS process
with its **own** L1 cache and its **own** `/stats` counters, so `/stats`
reported whichever worker answered that request rather than a true aggregate.

## Headline results (historical Python deployment)

Free-tier deployment: Render (app) + Neon (Postgres) + Upstash (Redis), all in
AWS US East 2 (Ohio).

| Path | Throughput | Avg latency | Success rate |
| --- | --- | --- | --- |
| **Hot** (cache hit, realistic multi-key traffic) | ~20,400 req/sec | ~4.5ms | 100% |
| **Cold** (guaranteed cache miss, straight to PostgreSQL) | ~3,500 req/sec | ~28ms | 100% |

These are two genuinely different numbers, measured separately and on purpose.
The cold-path number required codes that had never been requested before; see
"Cold-path measurement" below.

One additional result: an earlier, sync (non-async) version collapsed under
high concurrency — throughput fell to ~983 req/sec at 500 concurrent
connections, down from a healthy ~6,300 req/sec at lower concurrency, with p99
latency blowing out to ~881ms. After the async rewrite, that collapse
disappeared: throughput stayed in the 15,000–22,000 req/sec range from 100
concurrent connections all the way up to 2,000, with latency degrading
gracefully instead of falling off a cliff.

## Local Python results (same machine as the Go comparison)

These are the fresh Python runs used for the side-by-side comparison in
[`BENCHMARK.md`](./BENCHMARK.md). They are not the same session as the
historical deployment numbers above, and should not be mixed with them.

### Hot path

`hey -n 20000 -c 100 -disable-redirects http://127.0.0.1:<port>/<warm_code>`

| Implementation | Throughput | Average latency | p50 | p95 | p99 |
| --- | --- | --- | --- | --- | --- |
| Python, 8 uvicorn workers | ~16,000 req/sec | 5.4–6.0ms | 4.6–5.3ms | 11.4–12.5ms | 16.2–18.4ms |

Three consecutive rounds: 17,481 / 15,678 / 16,228 req/sec.

### Cold path

60,000 freshly seeded, disjoint codes. `vegeta attack -rate=0 -max-workers=100
-redirects=-1 -duration=4s`.

| Implementation | Throughput | Mean latency | p50 | p95 | p99 |
| --- | --- | --- | --- | --- | --- |
| Python, 8 workers | ~3,016 req/sec | 32.9ms | 29.2ms | 54.7ms | 133.8ms |

Both cold runs were 100% successful. The attack total stayed below the seed
count, so the client never cycled back onto warmed codes.

## Python benchmark history

Selected results from the original optimization work, in the order they were
measured.

### Worker scaling

| Workers | Req/sec | Avg latency | p99 |
| --- | --- | --- | --- |
| 4 | 6,562 | 14.5ms | 21.9ms |
| 8 | ~8,300 | ~11.2ms | ~22.8ms |

Matching worker count to the 8 available CPU threads produced a repeatable
~27% throughput increase with no code changes.

### Async + L1 cache rewrite

| Version | Req/sec | Avg latency | p99 |
| --- | --- | --- | --- |
| Pre-fix (buggy cache) | ~1,550 | ~58ms | ~130ms |
| Sync, cache fixed, 4 workers | ~6,400 | ~15.2ms | ~25ms |
| Sync, cache fixed, 8 workers | ~8,300 | ~11.2ms | ~22.8ms |
| **Async + L1 cache, 8 workers** | **~17,139** | **~5.4ms** | **~15.0ms** |

This was roughly 11× the original buggy version and ~2.1× the best sync result.

### Multi-key traffic with vegeta

20 keys, Zipf skew 1.2, 10-second attack:

```
Requests: 204,819 over 10s
Requests/sec: 20,480
Success ratio: 100%
Avg latency: 4.5ms
p99: 11.6ms
```

`/stats` immediately after the run:

```
{"total_requests":30655,"l1_hits":30601,"l2_hits":45,"db_hits":9,
 "l1_hit_rate":0.9982,"l2_hit_rate":0.0015,"db_hit_rate":0.0003}
```

99.82% of requests were served from L1, confirming that the throughput number
was a genuine cache-hit result rather than an artifact of the client.

## Cold-path measurement

Cold-path numbers are only valid if every request is a genuine cache miss.
Seeding codes through the normal `/shorten` endpoint would have been slow and
would have polluted the `/stats` counters, so `seed_cold_data.py` bulk-inserted
codes **directly into Postgres**, bypassing both caches.

The first cold run exposed a real bug the hot path could never have found:

```
Requests/sec: ~1,510–1,693
Avg latency: 59ms
Success rate: 91.6% — 712 requests failed with HTTP 500
```

The traceback was:

```
asyncpg.exceptions.TooManyConnectionsError: remaining connection slots are
reserved for roles with the SUPERUSER attribute
```

The app's pool was `pool_size=20, max_overflow=20` — up to 40 connections per
worker, or 320 across 8 workers, against Postgres's default
`max_connections=100`. Hot-path traffic never touched the pool, so no amount of
cache-hit load testing could have found it.

The fix was to right-size the pool to `pool_size=8, max_overflow=4` (12 per
worker, 96 across 8 workers) and raise `max_connections` to 200. A clean rerun
produced:

```
Requests/sec: 3,508–3,537
Avg latency: 28.264ms
p50: 23.2ms | p99: 136.9ms
Success rate: 100.00% — zero errors
```

An earlier "improved" run that reported ~7,400 req/sec was discarded: it sent
36,990 requests against only 20,000 seeded codes, so vegeta had cycled back
onto already-warmed codes and was no longer measuring the cold path.

## Benchmark commands (original Python version)

These commands apply to the historical Python app and its `testing/` scripts.
They are preserved as a record, not as instructions for the Go port.

```bash
# Single-key throughput (hot path)
hey -n 5000 -c 100 -disable-redirects http://127.0.0.1:8000/<short_code>

# Multi-key, realistic (Zipf-weighted) hot-path traffic, with cache hit-rate visibility
python testing/loadtest.py -n 5000 -c 100 -k 20 --skew 1.2

# Same load via Locust -- lower tail latency + built-in p50/p95/p99
locust -f testing/locustfile.py --headless -u 100 -r 100 -t 15s \
  --only-summary --num-keys 20 --skew 1.2

# Multi-key hot path at full (non-Python-bottlenecked) throughput
python testing/gen_vegeta_targets.py -k 20 --skew 1.2 -o testing/targets.txt
vegeta attack -targets=testing/targets.txt -rate=0 -max-workers=100 \
  -redirects=-1 -duration=10s | vegeta report

# True cold path (guaranteed cache misses, direct to Postgres)
python testing/seed_cold_data.py -n 50000 -o testing/cold_targets.txt
vegeta attack -targets=testing/cold_targets.txt -rate=0 -max-workers=100 \
  -redirects=-1 -duration=5s | vegeta report
```

**Important for cold-path testing:** keep the attack's total request count
under the number of codes you seeded, or `vegeta` will cycle back and start
hitting already-cached codes — no longer a valid cold-path measurement.
`seed_cold_data.py` printed a reminder of this every time it ran.

**Important for redirect benchmarks:** `-disable-redirects` (hey) and
`-redirects=-1` (vegeta) are mandatory. Without them, the client follows the
app's 307 to the real `example.com` and reports that unrelated site's latency
and status as if they came from Infinity.

## Original Python testing tools

| Tool | Purpose |
| --- | --- |
| `testing/loadtest.py` | Custom async multi-key load tester, Zipf-weighted. Useful for hit-rate visibility; bottlenecked by its own Python overhead for raw throughput. |
| `testing/locustfile.py` | Locust-based load tester with built-in p50/p95/p99 and a tighter tail. |
| `testing/gen_vegeta_targets.py` | Seeds short codes via the API and writes a Zipf-weighted vegeta target file. |
| `testing/seed_cold_data.py` | Bulk-inserts unique short codes directly into Postgres and writes a vegeta target file guaranteed to produce cache misses. |
| `testing/test_l1_ttl.py` | Proves the L1/L2 TTL behaviour end-to-end (must run with `--workers 1`). |

## Caveats

- **Python-specific.** These numbers depend on the interpreter, the GIL,
  uvicorn workers, async SQLAlchemy, and `redis.asyncio`. They are not a
  baseline for the Go implementation's expected performance.
- **Not the current implementation.** The Go port runs a single process with
  goroutines; its benchmark results are in [`BENCHMARK.md`](./BENCHMARK.md).
- **Per-worker state.** Each Python worker had its own L1 cache and `/stats`
  counters, so hit-rate figures came from one worker, not a service-wide sum.
- **Local vs deployed.** The historical deployment numbers (~20.4k hot /
  ~3.5k cold) and the local side-by-side numbers (~16k hot / ~3k cold) come
  from different sessions and traffic shapes. Do not compare them directly.
- **Loopback and local infrastructure.** Client and server shared a machine,
  and Postgres/Redis were local during local runs. Remote deployments add
  network latency and reduce the gaps.
- **The machine was not idle.** A full desktop session was running throughout
  testing, same as the Go benchmarks.
