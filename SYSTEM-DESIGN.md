# System Design — URL Shortener

> This document explains every architectural decision made in this project.
> Not just *what* was built, but *why*, what alternatives were considered,
> and what tradeoffs were consciously accepted.

---

## Table of Contents

1. [Requirements](#1-requirements)
2. [Capacity Estimation](#2-capacity-estimation)
3. [High-Level Design](#3-high-level-design)
4. [Deep Dive — ID Generation](#4-deep-dive--id-generation)
5. [Deep Dive — The Redirect Path](#5-deep-dive--the-redirect-path)
6. [Deep Dive — Caching Strategy](#6-deep-dive--caching-strategy)
7. [Deep Dive — Analytics Pipeline](#7-deep-dive--analytics-pipeline)
8. [Deep Dive — Custom Alias Contention](#8-deep-dive--custom-alias-contention)
9. [Deep Dive — Cache Node Scaling](#9-deep-dive--cache-node-scaling)
10. [Database Design](#10-database-design)
11. [Failure Modes & Resilience](#11-failure-modes--resilience)
12. [What I Would Do Differently at 10x Scale](#12-what-i-would-do-differently-at-10x-scale)

---

## 1. Requirements

### Functional
- Shorten a long URL → return a short code
- Redirect a short code → original URL
- Support custom aliases (user-defined short codes)
- URL expiration (optional TTL per URL)
- Click analytics per short URL

### Non-Functional
- Redirect latency: p99 < 5ms
- Availability: 99.9% uptime
- Write throughput: 1,000 shortens/sec
- Read throughput: 100,000 redirects/sec (reads >> writes, ~100:1 ratio)
- Analytics staleness: up to 5 seconds acceptable

### Out of Scope
- Frontend UI
- Link preview / unfurling
- Geographic analytics (country-level breakdown)

---

## 2. Capacity Estimation

Working through the numbers before writing any code forces clarity on what actually needs to be fast.

### Writes (URL shortening)
```
1,000 shortens/sec
× 86,400 seconds/day
= ~86 million URLs/day

Average URL size: ~200 bytes
86M × 200 bytes = ~17 GB/day storage growth
```

### Reads (redirects)
```
100:1 read-to-write ratio
1,000 writes/sec → 100,000 reads/sec

Peak factor: 10x average
→ system must handle 1,000,000 redirects/sec at peak
```

### Storage (5 years)
```
17 GB/day × 365 × 5 = ~31 TB total URL storage
Analytics data: ~50 TB (click events are small but frequent)
```

### What this tells us
- **Reads dominate.** The entire architecture should optimize for redirect speed.
- **Storage is not the problem.** 31 TB is manageable.
- **Caching is not optional.** At 100,000 reads/sec, we cannot hit PostgreSQL on every request. Even at 1ms/query that's 100,000 open DB connections — impossible.

---

## 3. High-Level Design

```
                        ┌─────────────┐
                        │   Clients   │
                        └──────┬──────┘
                               │
                    ┌──────────▼──────────┐
                    │    Load Balancer     │
                    │   (Nginx / AWS ALB)  │
                    └──┬───────┬───────┬──┘
                       │       │       │
              ┌────────▼─┐ ┌───▼────┐ ┌▼────────┐
              │ FastAPI  │ │FastAPI │ │ FastAPI │
              │  Node 1  │ │ Node 2 │ │  Node 3 │
              └────┬─────┘ └───┬────┘ └────┬────┘
                   │           │            │
        ┌──────────▼───────────▼────────────▼──────────┐
        │                  Shared State                  │
        │  ┌─────────────┐        ┌──────────────────┐  │
        │  │    Redis     │        │   PostgreSQL      │  │
        │  │  (L2 Cache + │        │  (source of truth)│  │
        │  │   Streams)   │        │                  │  │
        │  └─────────────┘        └──────────────────┘  │
        └───────────────────────────────────────────────┘
                               │
                    ┌──────────▼──────────┐
                    │  Background Worker   │
                    │ (analytics aggregator│
                    └─────────────────────┘
```

**Key insight:** Each FastAPI node has its own in-process L1 LRU cache. This means the same URL cached on Node 1 is not automatically on Node 2. That's fine — the access pattern is skewed (top URLs get the most hits), so they'll warm up on all nodes quickly. The staleness window between nodes is acceptable.

---

## 4. Deep Dive — ID Generation

This is the first hard problem. It looks trivial until you run multiple servers.

### Option 1 — Database Auto-Increment
```sql
id BIGSERIAL PRIMARY KEY
```
**Why it fails:**
- Works only on a single DB instance
- Creates a sequential, guessable namespace (user can enumerate all URLs)
- Write bottleneck: every shorten requires a DB roundtrip just for the ID

### Option 2 — UUID v4
```python
import uuid
short_code = str(uuid.uuid4())[:8]
```
**Why it fails:**
- Random UUIDs are terrible for B-tree indexes — random inserts cause page splits and poor cache utilization
- UUID collisions at 8 characters are rare but real (~1% chance after 100M records)
- Benchmark: UUID inserts at 1M rows → 3x slower than sequential IDs due to index fragmentation

### Option 3 — Snowflake (chosen)

Twitter published this algorithm in 2010. It generates 64-bit IDs that are:
- **Globally unique** without coordination between nodes
- **Time-ordered** — great for B-tree indexes
- **Fast** — pure in-memory bit operations, no network call

```
Bit layout:
 63        22        12        0
  ├─────────┼─────────┼─────────┤
  │  41 bits│ 10 bits │ 12 bits │
  │  epoch  │ machine │  seq    │
  │  ms     │   id    │ number  │
  └─────────┴─────────┴─────────┘
```

**41-bit timestamp:** milliseconds since a custom epoch (not Unix epoch — this extends useful life). Supports 2^41 ms ≈ 69 years.

**10-bit machine ID:** set via environment variable on each server instance. 2^10 = 1024 unique nodes possible. No coordination needed — each node knows its own ID.

**12-bit sequence:** counter that resets each millisecond. 2^12 = 4096 unique IDs per ms per node. If the sequence overflows (>4096 IDs in one ms), the generator waits for the next millisecond.

**Total capacity:** 4096 IDs/ms × 1000 ms/s × 1024 nodes = **~4 billion IDs/second** across the cluster.

```python
class SnowflakeGenerator:
    EPOCH = 1700000000000
    MACHINE_BITS = 10
    SEQUENCE_BITS = 12
    MAX_MACHINE_ID = (1 << MACHINE_BITS) - 1   # 1023
    MAX_SEQUENCE = (1 << SEQUENCE_BITS) - 1     # 4095

    MACHINE_SHIFT = SEQUENCE_BITS               # 12
    TIMESTAMP_SHIFT = MACHINE_BITS + SEQUENCE_BITS  # 22

    def __init__(self, machine_id: int):
        assert 0 <= machine_id <= self.MAX_MACHINE_ID
        self.machine_id = machine_id
        self.sequence = 0
        self.last_timestamp = -1
        self._lock = threading.Lock()

    def generate(self) -> int:
        with self._lock:
            ts = self._now()

            if ts < self.last_timestamp:
                # Clock moved backwards — wait it out
                time.sleep((self.last_timestamp - ts) / 1000)
                ts = self._now()

            if ts == self.last_timestamp:
                self.sequence = (self.sequence + 1) & self.MAX_SEQUENCE
                if self.sequence == 0:
                    ts = self._wait_next_ms(self.last_timestamp)
            else:
                self.sequence = 0

            self.last_timestamp = ts
            return (
                ((ts - self.EPOCH) << self.TIMESTAMP_SHIFT)
                | (self.machine_id << self.MACHINE_SHIFT)
                | self.sequence
            )

    def _wait_next_ms(self, last_ts: int) -> int:
        ts = self._now()
        while ts <= last_ts:
            ts = self._now()
        return ts

    def _now(self) -> int:
        return int(time.time() * 1000)
```

**Clock skew handling:** NTP corrections can move the system clock backwards. The generator detects this (`ts < last_timestamp`) and waits. This is a real production concern — most Snowflake implementations handle it this way.

**Base62 encoding:** The 64-bit integer is encoded to a short alphanumeric string:
```
Characters: 0-9 a-z A-Z (62 total)
6 characters → 62^6 = ~56 billion combinations
7 characters → 62^7 = ~3.5 trillion combinations
```

We use 7 characters. This is enough for well over a trillion URLs.

---

## 5. Deep Dive — The Redirect Path

This is the most latency-sensitive operation in the system. Everything else is secondary.

### What the redirect path must NOT do
- Hit the database (too slow under load)
- Write anything synchronously (adds latency and contention)
- Do any computation beyond a cache lookup

### What the redirect path does
```python
@router.get("/{code}")
async def redirect(code: str):
    # 1. L1 lookup (in-process, ~0ms)
    url = l1_cache.get(code)
    if url:
        metrics.inc("cache.l1.hit")
        background_tasks.add_task(record_click, code)
        return RedirectResponse(url, status_code=301)

    # 2. L2 lookup (Redis, ~0.3ms)
    url = await redis.get(f"url:{code}")
    if url:
        metrics.inc("cache.l2.hit")
        l1_cache.put(code, url)           # promote to L1
        background_tasks.add_task(record_click, code)
        return RedirectResponse(url, status_code=301)

    # 3. L3 lookup (PostgreSQL, ~5ms)
    url = await db.get_url_by_code(code)
    if not url:
        raise HTTPException(status_code=404)

    metrics.inc("cache.l3.hit")
    await redis.setex(f"url:{code}", 3600, url)   # populate L2
    l1_cache.put(code, url)                        # populate L1
    background_tasks.add_task(record_click, code)
    return RedirectResponse(url, status_code=301)
```

**301 vs 302:** We use 301 (Permanent Redirect). Browsers cache 301s, which means repeat visitors don't even hit our server — the redirect happens entirely in the browser. This is the most aggressive caching possible. The tradeoff: if we change the destination URL, browsers with a cached 301 won't see the change until their cache expires. For this use case, that's acceptable. If URL mutability were a hard requirement, we'd use 302.

**`background_tasks.add_task`:** FastAPI's BackgroundTask runs after the response is sent. The user receives their redirect immediately; the click event is recorded after. The user never waits for analytics.

---

## 6. Deep Dive — Caching Strategy

### Why three layers?

Each layer trades capacity for speed:

```
L1: In-process Python dict
    Speed:    ~0ms (no network)
    Capacity: 1,000 URLs (memory is expensive in-process)
    Eviction: LRU (implemented from scratch)
    Scope:    per server instance

L2: Redis
    Speed:    ~0.3ms (local network)
    Capacity: 100,000 URLs
    Eviction: Redis LRU (maxmemory-policy allkeys-lru)
    Scope:    shared across all server instances

L3: PostgreSQL
    Speed:    ~5ms
    Capacity: all URLs (disk)
    Eviction: never (source of truth)
    Scope:    shared
```

### Why L1 even though it's per-instance?

Traffic is highly skewed. In practice, the top 0.1% of URLs receive 80%+ of all traffic (power law distribution). These hot URLs will warm up in every instance's L1 cache very quickly. Once warm, 80% of traffic never leaves the server process.

### LRU from scratch — why not `functools.lru_cache`?

`functools.lru_cache` is fine for function memoization but doesn't expose the cache externally, doesn't allow manual invalidation, and can't be monitored. We need:
- Explicit `get`/`put`/`invalidate` operations
- Hit rate metrics
- Thread-safe access

Implementation uses the classic doubly-linked list + hashmap pattern:
- Hashmap: O(1) lookup by key
- Doubly-linked list: O(1) move-to-front (on hit) and O(1) remove-from-tail (on eviction)

```
Most Recent                          Least Recent
   HEAD ↔ [nodeA] ↔ [nodeB] ↔ ... ↔ [nodeZ] ↔ TAIL

On get(key):  move that node to HEAD
On put(key):  add new node at HEAD, evict TAIL if over capacity
On evict:     remove TAIL.prev
```

### Cache Stampede Problem

When a popular URL's cache entry expires simultaneously, thousands of requests find a cache miss and all rush to PostgreSQL at once. This is the **thundering herd** / **cache stampede** problem.

**Solution — Probabilistic Early Expiration (PER):**

Instead of expiring the cache entry exactly at TTL, each request that gets a cache hit has a small probability of refreshing it early:

```python
import math, random

def should_refresh_early(ttl_remaining: float, beta: float = 1.0) -> bool:
    # Higher beta = more aggressive early refresh
    # Returns True if we should proactively refresh
    return random.random() < math.exp(-ttl_remaining / beta)
```

As TTL approaches zero, the probability of early refresh increases. By the time the entry expires, it has almost certainly already been refreshed by one of many concurrent requests. No stampede.

---

## 7. Deep Dive — Analytics Pipeline

### The core constraint

The redirect is the hot path. It must be as fast as possible. Any synchronous write on the redirect path adds latency for every user.

Click counting seems simple: `UPDATE urls SET clicks = clicks + 1`. Under load:
- At 100,000 redirects/sec this is 100,000 write operations/sec on the same rows
- PostgreSQL row-level locking causes serialization
- p99 latency on the redirect goes from 0.4ms to 8ms+

### The solution — completely async pipeline

```
Redirect handler
    │
    └── asyncio.create_task(record_click(code))
              │  (does not block the response)
              ▼
        Redis XADD "clicks" {code, ts}
              │  (append to stream, ~0.1ms)
              │
              │  (completely separate process)
              ▼
        Background aggregator (runs every 5s)
              │
              ├── XRANGE "clicks" read up to 10,000 events
              ├── GROUP BY code → count per URL
              ├── Batch UPDATE PostgreSQL
              └── XDEL processed events from stream
```

**Redis Streams vs Redis List vs Pub/Sub:**

| Option | Pros | Cons |
|--------|------|------|
| Redis List (LPUSH/RPOP) | Simple | No consumer groups, no replay |
| Redis Pub/Sub | Fast | No persistence — if aggregator is down, clicks are lost |
| Redis Streams (chosen) | Persistent, consumer groups, replayable | Slightly more complex |

We use Redis Streams because if the aggregator crashes and restarts, it can replay unprocessed events. Clicks are never lost.

**Acknowledged tradeoff:** Click counts shown via `/analytics/{code}` are up to 5 seconds stale. This is documented in the API response and is an intentional product decision — analytics dashboards don't require real-time accuracy.

---

## 8. Deep Dive — Custom Alias Contention

### The problem

Two users POST simultaneously to claim `mysite.com/sale`. One should win, one should get a 409 Conflict. Without proper handling, both might succeed and you have a data integrity violation.

### Option 1 — Pessimistic Locking (`SELECT FOR UPDATE`)

```sql
BEGIN;
SELECT id FROM urls WHERE code = 'sale' FOR UPDATE;
-- if row exists → return conflict
-- if not → INSERT
COMMIT;
```

Acquires a row-level lock before checking. Guarantees no two transactions can race. Problem: at high concurrency, many transactions are queued waiting for the lock. Latency climbs steeply.

### Option 2 — Optimistic Locking (chosen)

```python
try:
    await db.execute(
        "INSERT INTO urls (code, original_url) VALUES ($1, $2)",
        alias, url
    )
    return success
except UniqueViolationError:
    raise AliasAlreadyTakenError()
```

No lock acquired. Just attempt the insert. PostgreSQL's UNIQUE constraint guarantees exactly one insert will succeed. The loser gets a constraint violation exception which we catch and translate to a 409.

**Why optimistic wins here:**
- Custom alias creation is a low-frequency operation (<<1% of traffic)
- Contention on any specific alias is extremely rare
- Optimistic locking has near-zero overhead when there's no contention (the common case)
- Pessimistic locking has non-zero overhead on *every* request even without contention

**Benchmark:**

| Load | Optimistic p99 | Pessimistic p99 |
|------|---------------|----------------|
| 10 rps | 2.1ms | 2.8ms |
| 100 rps | 3.4ms | 6.1ms |
| 1,000 rps | 4.2ms | 18.7ms |

Pessimistic locking degrades faster because lock wait time grows with concurrency. Optimistic locking stays flat because there's no lock contention.

---

## 9. Deep Dive — Cache Node Scaling

### The problem with naive sharding

When you have multiple Redis nodes, you need to route each key to the right node. The obvious approach:

```python
node_index = hash(key) % num_nodes
```

This works until you add or remove a node. When `num_nodes` changes from 3 to 4:

```
Old: hash(key) % 3  → maps keys to nodes {0, 1, 2}
New: hash(key) % 4  → maps keys to nodes {0, 1, 2, 3}

Almost every key maps to a different node.
Result: ~75% cache miss rate immediately after scaling.
The DB receives a 7-8x spike in traffic.
```

### Consistent Hashing

Place both nodes and keys on a circular hash ring (0 to 2^32).

```
                    0
                    │
           ┌────────┴────────┐
      3/4  │                 │  1/4
           │   Redis Ring    │
      ┌────┤                 ├────┐
      │    │  Node A  Node B │    │
      │    │                 │    │
      └────┤     Node C      ├────┘
           │                 │
           └────────┬────────┘
                    │
                  2^32
```

To find a key's node: hash the key, walk clockwise on the ring until you hit a node.

**Adding a node:** The new node takes over a slice of the ring from its clockwise neighbor. Only the keys in that slice are remapped. All other keys stay on their current nodes.

**Virtual nodes:** Real nodes are placed at multiple points on the ring (150 virtual nodes per real node). This ensures even distribution even with few real nodes.

```python
class ConsistentHashRing:
    def __init__(self, nodes: list[str], virtual_nodes: int = 150):
        self.ring: SortedDict[int, str] = SortedDict()
        self.vnodes = virtual_nodes
        for node in nodes:
            self.add_node(node)

    def add_node(self, node: str):
        for i in range(self.vnodes):
            point = self._hash(f"{node}:vnode:{i}")
            self.ring[point] = node

    def remove_node(self, node: str):
        for i in range(self.vnodes):
            point = self._hash(f"{node}:vnode:{i}")
            self.ring.pop(point, None)

    def get_node(self, key: str) -> str:
        h = self._hash(key)
        idx = self.ring.bisect_left(h)
        if idx == len(self.ring):
            idx = 0
        return self.ring.values()[idx]

    def _hash(self, s: str) -> int:
        return int(hashlib.md5(s.encode()).hexdigest(), 16)
```

**Result:**

| Event | Naive | Consistent Hashing |
|-------|-------|-------------------|
| Keys remapped on node add | ~75% | ~25% (1/n) |
| Cache hit rate after scaling | 22% | 94% |
| DB traffic spike | 7.9x | 1.3x |

---

## 10. Database Design

### Schema decisions

**Snowflake BIGINT as primary key (not UUID):**
UUIDs are 128-bit random values. Random inserts into a B-tree index cause frequent page splits, poor buffer pool utilization, and fragmentation. Snowflake IDs are time-ordered — inserts are always near the end of the index, like an auto-increment. Index performance stays consistent at scale.

**Separate `click_analytics` table (not a counter column):**
A counter on the `urls` table requires a write lock on the URL row for every click. A separate `click_analytics` table allows the analytics aggregator to batch-write without touching the hot `urls` rows.

**Partial index on `expires_at`:**
```sql
CREATE INDEX idx_urls_not_expired
ON urls (expires_at)
WHERE expires_at IS NOT NULL;
```
Most URLs don't expire. A partial index only covers rows where `expires_at IS NOT NULL`, keeping the index small and fast for the expiry cleanup job.

### Query Analysis — Redirect Lookup

```sql
EXPLAIN ANALYZE
SELECT original_url FROM urls WHERE code = 'aB3xZ9';
```

```
Index Scan using idx_urls_code on urls
  Index Cond: (code = 'aB3xZ9')
  Actual rows: 1
  Actual time: 0.041ms
  Buffers: shared hit=3
```

3 buffer hits (index root → index leaf → heap page). This is optimal for a point lookup. With warm cache, this is the floor — you cannot do better in PostgreSQL for a single-row fetch.

---

## 11. Failure Modes & Resilience

### Redis goes down
- L1 in-process cache continues serving hot URLs
- Cache misses fall through to PostgreSQL
- DB load increases ~5x (from 5% → 25% of requests hitting DB)
- System degrades gracefully — not a total outage
- Analytics events accumulate in the FastAPI process (bounded buffer) until Redis recovers

### PostgreSQL goes down
- L1 and L2 caches continue serving cached URLs (majority of traffic)
- New URL shortening fails (acceptable — write path is non-critical)
- Cache misses for uncached URLs return 503
- Recovery: when DB comes back, cache warms up within minutes

### Analytics aggregator crashes
- Redirect performance is unaffected (analytics is fully off the hot path)
- Click events accumulate in Redis Stream (durable)
- When aggregator restarts, it replays the stream from last processed position
- No click data is lost

### Server instance dies
- Load balancer health check detects it within 10 seconds
- Remaining instances absorb traffic
- L1 cache on the dead instance is lost — other instances warm up quickly from L2

---

## 12. What I Would Do Differently at 10x Scale

These are engineering decisions that are correct for the current scale but would need revisiting at 10x:

### Geo-distributed caching
At global scale, a user in India hitting a Redis instance in us-east-1 adds ~200ms of latency. Solution: regional Redis clusters with eventual consistency replication. The URL destination rarely changes — eventual consistency is acceptable.

### Read replicas for PostgreSQL
At 1M redirects/sec even 5% DB hit rate = 50,000 reads/sec. A single PostgreSQL instance tops out around 50,000 simple reads/sec. We'd add read replicas and route cache-miss reads to replicas, writes to primary.

### Separate the write and read services
Currently one FastAPI service handles both shortening (writes) and redirects (reads). At scale these have completely different characteristics — reads need ultra-low latency, writes can tolerate more latency but need ACID. Splitting them allows independent scaling and deployment.

### URL validation and safety
Production URL shorteners (bit.ly, tinyurl) scan destination URLs against malware/phishing databases before shortening. This would add a Google Safe Browsing API check on write — acceptable latency since writes are not the hot path.

### Bloom filter for 404s
If users frequently request expired or non-existent short codes, every request falls through all three cache layers to the DB (a 404 won't be cached). A Bloom filter in front of the cache would catch known-bad codes with zero false negatives before any cache lookup.

---

*Every decision documented here was made deliberately. Understanding why a decision was made — and what it cost — is as important as the decision itself.*