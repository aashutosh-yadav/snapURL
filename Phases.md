Smart thinking. Building it in parts but completing everything is the right approach. Here's how to divide it:

---

## Phase 1 — The Working Core
**Goal: A real, working URL shortener**

Build in this order:
- PostgreSQL schema (urls table, basic columns)
- Snowflake ID generator with clock skew handling
- Base62 encoding
- POST `/shorten` endpoint
- GET `/{code}` redirect endpoint
- Basic error handling (404, invalid)

**What you have at end of Phase 1:**
A fully working URL shortener. No caching yet, but correct and deployable. You can demo it.

---

## Phase 2 — Caching Layer
**Goal: Make it fast**

- LRU cache from scratch (doubly linked list + hashmap — no library)
- Plug it in as L1 in-process cache
- Add Redis as L2 cache
- Implement the 3-layer lookup (L1 → L2 → DB)
- Add hit rate metrics so you can actually see it working
- 301 vs 302 decision — implement and document why

**What you have at end of Phase 2:**
A fast URL shortener with two cache layers. You can now show cache hit rates in your demo.

---

## Phase 3 — URL Features
**Goal: Complete the product features**

- TTL / URL expiration
- Custom aliases
- Optimistic locking for alias contention (UniqueViolationError handling)
- Partial index on expires_at in PostgreSQL
- Expiry cleanup background job

**What you have at end of Phase 3:**
Feature-complete URL shortener matching real products like bit.ly.

---

## Phase 4 — Analytics Pipeline
**Goal: Async click tracking**

- Naive version first — synchronous click counter, see the latency problem yourself
- Then switch to BackgroundTask approach
- Redis Streams for durable click events
- Background aggregator that reads stream every 5 seconds
- Batch UPDATE to PostgreSQL
- GET `/analytics/{code}` endpoint

**Why naive first:**
When you implement the slow version and load test it, you'll *feel* why the async pipeline is necessary. That experience makes your interview explanation genuine.

---

## Phase 5 — Resilience + Scale
**Goal: Handle failure gracefully**

- Consistent hashing implementation with virtual nodes
- Probabilistic Early Expiration for cache stampede
- Test Redis going down — does L1 still serve traffic?
- Test PostgreSQL going down — does cache hold?
- Document every failure mode and what actually happened

---

## Phase 6 — Load Testing + Documentation
**Goal: Get real numbers**

- Use locust or k6 to load test
- Measure p99 latency at each phase
- Compare optimistic vs pessimistic locking with actual numbers
- Write the tradeoff document *after* building — not before
- Add the "what I struggled with" section (this is gold in interviews)

---

## Full Timeline

```
Month 2 Week 1  →  Phase 1 (Core)
Month 2 Week 2  →  Phase 2 (Caching)
Month 2 Week 3  →  Phase 3 (URL Features)
Month 2 Week 4  →  Phase 4 (Analytics)
Month 3 Week 1  →  Phase 5 (Resilience)
Month 3 Week 2  →  Phase 6 (Load Testing + Docs)
Month 3 Week 3+ →  Mini Redis project
```

---

## One Rule Per Phase

| Phase | Rule |
|---|---|
| Phase 1 | No caching at all — feel the raw DB latency first |
| Phase 2 | Build LRU yourself before touching Redis |
| Phase 3 | Break alias contention intentionally before fixing it |
| Phase 4 | Build sync analytics first, measure the problem, then fix it |
| Phase 5 | Actually kill Redis and PostgreSQL during testing |
| Phase 6 | Every number in your doc must come from a real test you ran |

---

The key insight is **feel the problem before solving it.** Every phase is designed so you hit the real engineering problem yourself, which means you can explain it authentically in interviews without memorizing anything.
