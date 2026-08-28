# Benchmarks

Performance benchmarks for the URL shortener service. All tests run locally against `http://127.0.0.1:8000`.

---

## Baseline — DB Lookup (No Cache)

This is all for the realational database because we are using postgress.(just for MVP , not considerd scalling). 
Redirect resolution hitting the database directly via B-tree index. No in-memory caching layer.

**Tool:** [`hey`](https://github.com/rakyll/hey)  
**Command:**
```
hey -n 5000 -c 100 -disable-redirects http://127.0.0.1:8000/orAXTk
```

### Summary

| Metric           | Value       |
|------------------|-------------|
| Total requests   | 5,000       |
| Concurrency      | 100         |
| Total time       | 15.75 secs  |
| Requests/sec     | 317.45      |
| Avg latency      | 305.0 ms    |
| Median (p50)     | 287.5 ms    |
| p95              | 494.2 ms    |
| p99              | 657.3 ms    |
| Fastest          | 9.1 ms      |
| Slowest          | 1036.2 ms   |
| Status           | 307 ✓ (all) |

### Latency Distribution

```
  9ms  [1]     |
112ms  [124]   |■■■
214ms  [950]   |■■■■■■■■■■■■■■■■■■■■
317ms  [1874]  |■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■  ← peak
420ms  [1337]  |■■■■■■■■■■■■■■■■■■■■■■■■■■■■■
523ms  [540]   |■■■■■■■■■■■■
625ms  [119]   |■■■
728ms  [19]    |
831ms  [15]    |
933ms  [11]    |
1036ms [10]    |
```

### Time Breakdown (avg)

| Phase       | Time      | % of total |
|-------------|-----------|------------|
| DNS + dialup | 0.3 ms   | ~0.1%      |
| Request write | 0.0 ms  | ~0%        |
| **Response wait** | **304.6 ms** | **~99.9%** |
| Response read | 0.1 ms  | ~0%        |

> Nearly all latency is server-side. Network and I/O are negligible.

### Notes

- The distribution peaks at ~317ms with a right tail reaching 1s+ — classic queuing behavior under high concurrency, not raw DB speed
- `resp wait` consuming 99.9% of latency points to worker/connection pool contention at c=100, not B-tree lookup overhead
- The long tail (p99 = 657ms vs p50 = 287ms) suggests some requests queue behind busy workers

---


## Multi-Worker Benchmark — 4 Workers

### Setup

* Backend: FastAPI (Uvicorn)
* Workers: 4
* Database: PostgreSQL (local)
* Test Tool: hey
* Endpoint: `/ {short_code}` (redirect)
* Redirects disabled to measure backend latency only

**Command:**

```
hey -n 5000 -c 100 -disable-redirects http://127.0.0.1:8000/4FmAo3
```

---

### Results

| Metric         | Value       |
| -------------- | ----------- |
| Total Requests | 5,000       |
| Concurrency    | 100         |
| Total Time     | 2.60 sec    |
| Requests/sec   | 1918.83     |
| Avg Latency    | 49.5 ms     |
| p50            | 47.0 ms     |
| p95            | 80.7 ms     |
| p99            | 115.6 ms    |
| Fastest        | 1.4 ms      |
| Slowest        | 175.4 ms    |
| Status         | 307 ✓ (all) |

---

### Latency Distribution

```
0.001 [1]     |
0.019 [116]   |■■
0.036 [843]   |■■■■■■■■■■■■■■
0.054 [2409]  |■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■
0.071 [1165]  |■■■■■■■■■■■■■■■■■■■
0.088 [298]   |■■■■■
0.106 [90]    |■
0.123 [31]    |■
0.141 [25]    |
0.158 [14]    |
0.175 [8]     |
```

---

### Observations

* Significant performance improvement compared to single-worker setup
* Average latency reduced from ~300ms → ~50ms (~6x improvement)
* Throughput increased from ~317 req/sec → ~1900 req/sec (~6x improvement)
* Tail latency (p99) reduced drastically (~650ms → ~115ms)
* Latency distribution is tight with minimal long-tail behavior

---

### Analysis

* The primary bottleneck in the baseline system was **worker contention**, not database performance
* Increasing worker count allowed parallel request handling, reducing queueing delays
* Database lookups are fast and not yet a limiting factor under current load
* System demonstrates good scaling behavior with increased concurrency

---

### Conclusion

* Multi-worker configuration significantly improves system performance
* Current architecture (FastAPI + PostgreSQL) is sufficient for moderate traffic (~2k RPS)
* Introducing Redis at this stage would be a **premature optimization**

---

### Next Steps

* Test higher concurrency (200–500 users)
* Evaluate performance under cold-cache conditions
* Benchmark write-heavy workloads (`/shorten`)
* Introduce Redis caching when database becomes a bottleneck

### Problem with workers 

* Memory 
  4 workers → 4x memory usage

* DB connections 
  4 workers → multiple DB connections  

* CPU limit 
  4 CPU cores → ideal ≈ 4 workers  

* Workers dont make you app faster they make it more capable of handling multiple requests simultaneously .


## High Concurrency Stress Test — c=500

### Results

| Metric      | Value   |
| ----------- | ------- |
| RPS         | ~983    |
| Avg Latency | ~339 ms |
| p99         | ~881 ms |

---

### Observations

* Throughput decreased significantly compared to lower concurrency levels
* Average latency increased ~5x compared to c=200
* Tail latency (p99) exceeded 800ms, indicating severe queueing
* Response wait time dominates total latency, suggesting request backlog

---

### Analysis

* System has reached saturation point at high concurrency (c=500)
* Worker processes are overloaded, leading to request queue buildup
* CPU/worker capacity is the primary bottleneck
* Database is not the limiting factor at this stage

---

### Conclusion

* Optimal operating range: ~200–250 concurrent users (~2500 RPS)
* Beyond this point, performance degrades due to worker saturation
* Scaling requires:

  * increasing worker count
  * adding CPU resources
  * or horizontal scaling across multiple instances



# Redis Caching — Benchmark & Performance Analysis

---

## 📊 Test Setup

* Tool: `hey`
* Endpoint: `/ {short_code}` (redirect)
* Redirects disabled
* Environment:

  * FastAPI + Uvicorn (4 workers)
  * PostgreSQL (local)
  * Redis (local)
* Load: 5000 requests, concurrency = 100

---

# 🚀 Baseline — Without Redis

| Metric      | Value   |
| ----------- | ------- |
| RPS         | ~1919   |
| Avg Latency | ~49 ms  |
| p50         | ~47 ms  |
| p95         | ~80 ms  |
| p99         | ~115 ms |

### Observations

* High throughput and low latency
* Tight latency distribution (low variance)
* Database lookups are already very fast
* No significant queuing under this load

---

# ⚠️ Redis (Cold Cache)

| Metric      | Value   |
| ----------- | ------- |
| RPS         | ~1323   |
| Avg Latency | ~66 ms  |
| p99         | ~339 ms |

### Observations

* Performance degraded compared to baseline
* High tail latency due to cache misses
* Requests hit database + Redis simultaneously
* Initial cache population adds overhead

---

# ✅ Redis (Warm Cache)

| Metric      | Value       |
| ----------- | ----------- |
| RPS         | ~1500–1650  |
| Avg Latency | ~57–61 ms   |
| p50         | ~52–58 ms   |
| p95         | ~85–112 ms  |
| p99         | ~125–150 ms |

### Observations

* Performance stabilized after cache warm-up
* Majority of requests served from Redis
* Reduced variance compared to cold cache
* Slight improvement over cold cache, but still below baseline

---

# 📈 Comparative Summary

| Scenario     | RPS   | Avg Latency | p99     |
| ------------ | ----- | ----------- | ------- |
| No Redis     | ~1919 | ~49 ms      | ~115 ms |
| Redis (Cold) | ~1323 | ~66 ms      | ~339 ms |
| Redis (Warm) | ~1550 | ~58 ms      | ~130 ms |

---

# 🧠 Key Insights

## 1. Database is not the bottleneck

* Indexed lookups in PostgreSQL are extremely fast
* Local deployment minimizes latency
* Caching does not significantly reduce response time

---

## 2. Redis introduces overhead

Each request adds:

* Additional network call (even on localhost)
* Serialization/deserialization cost
* Extra system calls

Result:

* Slight increase in latency
* Reduction in throughput

---

## 3. Cold cache is significantly worse

* Cache misses trigger both Redis and DB operations
* Leads to higher latency and tail spikes
* Initial requests suffer the most

---

## 4. Warm cache stabilizes performance

* High cache hit rate reduces DB usage
* Latency becomes more consistent
* Tail latency improves compared to cold cache

---

## 5. Redis does not improve performance in this setup

Because:

* Single-node architecture
* Local database with low latency
* Small dataset and simple queries

Conclusion:

> Redis is not beneficial when the database is already fast and not under load.

---

## 6. Primary bottleneck is worker/CPU capacity

From previous experiments:

* Increasing workers significantly improved performance
* High concurrency leads to queueing, not DB slowdown

---

# ⚖️ Final Conclusion

* Redis caching is correctly implemented but **not required at current scale**
* It introduces additional overhead without meaningful gains
* Performance optimization should focus on:

  * worker scaling
  * concurrency handling
  * CPU utilization

---

# 🚀 When Redis becomes useful

Redis will provide clear benefits when:

* Database is remote (network latency increases)
* Database becomes CPU-bound
* Read traffic is very high (10k+ RPS)
* Multiple application instances are deployed
* Query complexity increases

---

# 🧠 Final Takeaway

> Performance optimizations must be driven by measured bottlenecks, not assumptions.

Redis is a powerful tool, but its effectiveness depends entirely on system context.
