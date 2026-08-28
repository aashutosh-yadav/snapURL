## Technologies Used in Distributed Systems

    Common backend tools:
    Communication
    REST APIs
    gRPC
    Message queues
    Messaging systems
    Kafka
    RabbitMQ
    Distributed storage
    Cassandra
    DynamoDB
    Hadoop
    Caching
    Redis
    Memcached
    Container orchestration
    Kubernetes
    Docker

---

## Complete Tech Stack — URL Shortener

---

### 🐍 Core Backend
| Tool | Version | Purpose |
|------|---------|---------|
| **Python** | 3.11+ | Main language |
| **FastAPI** | Latest | API framework, WebSocket, BackgroundTasks |
| **Uvicorn** | Latest | ASGI server to run FastAPI |
| **Pydantic v2** | Latest | Request/response validation, settings |

---

### 🗄️ Database
| Tool | Version | Purpose |
|------|---------|---------|
| **PostgreSQL** | 15+ | Primary database — all URLs, users, analytics |
| **SQLAlchemy** | 2.0 (async) | ORM for DB queries |
| **asyncpg** | Latest | Async PostgreSQL driver |
| **Alembic** | Latest | Database migrations |

---

### ⚡ Cache & Messaging
| Tool | Version | Purpose |
|------|---------|---------|
| **Redis** | 7+ | L2 cache + Redis Streams for click events |
| **redis-py** | Latest | Async Python Redis client |

---

### 🔐 Auth
| Tool | Version | Purpose |
|------|---------|---------|
| **python-jose** | Latest | JWT token creation and validation |
| **passlib** | Latest | Password hashing framework |
| **bcrypt** | Latest | Hashing algorithm used by passlib |

---

### 📊 Monitoring & Observability
| Tool | Version | Purpose |
|------|---------|---------|
| **Prometheus** | Latest | Scrapes and stores metrics |
| **prometheus-fastapi-instrumentator** | Latest | Auto-instruments FastAPI with metrics |
| **Grafana** | Latest | Visualizes metrics from Prometheus |

---

### 🧪 Testing
| Tool | Version | Purpose |
|------|---------|---------|
| **pytest** | Latest | Unit and integration tests |
| **pytest-asyncio** | Latest | Async test support |
| **httpx** | Latest | Test FastAPI endpoints |
| **Locust** | Latest | Load testing — generates benchmark numbers |

---

### 📦 Infrastructure
| Tool | Version | Purpose |
|------|---------|---------|
| **Docker** | Latest | Containerize each service |
| **Docker Compose** | Latest | Run all services together locally |

---

### 🛠️ Dev Tools
| Tool | Purpose |
|------|---------|
| **python-dotenv** | Load `.env` files locally |
| **loguru** | Clean structured logging |


---

### How They All Connect

```
Request comes in
      │
   FastAPI (Uvicorn)
      │
      ├── Pydantic validates input
      ├── python-jose checks JWT
      │
      ├── L1: in-process LRU (pure Python)
      ├── L2: Redis (redis-py)
      └── L3: PostgreSQL (SQLAlchemy + asyncpg)
                │
                └── Alembic manages schema

Click event
      │
      └── Redis Stream (redis-py)
                │
                └── Background aggregator → PostgreSQL

Metrics
      │
      └── prometheus-fastapi-instrumentator
                │
                └── Prometheus scrapes /metrics
                          │
                          └── Grafana displays it

Tests
      │
      ├── pytest + httpx → unit + integration
      └── Locust → load testing → your benchmark numbers
```

---

## IMPORTANT DSA FOR SYSTEM DESIGN 

    Hash Tables (Very Important)
    Consistent Hashing (Extremely Important)

    Trees
    Especially:
    B-Trees
    B+ Trees

    Heaps / Priority Queues

    Queues

    Graph Algorithms

    LRU Cache Algorithm

    Trie (Prefix Tree)

    Rate Limiting Algorithms


## 15 Key Concepts in Distributed Systems
Distributed systems are complex and fascinating, and understanding their key concepts is crucial for building scalable, fault-tolerant, and high-performance applications. Let's understand the fundamental concepts that underpin distributed systems and their importance in modern software development.

Nodes: 

    In a distributed system, nodes refer to the individual computing devices or servers connected through a network. Each node operates independently and can communicate with other nodes to perform specific tasks. Nodes can range from physical machines to virtual instances in the cloud.

Communication Protocols:

    Communication protocols define the rules and conventions for data exchange between nodes in a distributed system. Common communication protocols include HTTP, TCP/IP, UDP, and gRPC. Choosing the right protocol is essential for efficient and reliable communication.

Concurrency:

    Concurrency is the ability of a distributed system to execute multiple tasks simultaneously. Concurrent execution can lead to performance improvements, but it also introduces challenges related to managing shared resources and handling synchronization.

Consistency:

    Consistency refers to the property of a distributed system where all nodes have the same view of data and agree on the order of operations. Achieving strong consistency in distributed systems can be challenging due to factors like latency, network partitions, and replication.

Replication:

    Replication involves maintaining multiple copies of data or services across different nodes in a distributed system. Replication improves fault tolerance and data availability. However, ensuring consistency between replicas is a key challenge in distributed systems.

Partitioning:

    Partitioning, also known as sharding, is the practice of dividing data into smaller subsets and distributing them    different nodes. Sharding enhances scalability as each node handles a portion of the data, allowing the system to handle increased workloads efficiently.

Fault Tolerance:

    Fault tolerance is the ability of a distributed system to continue functioning correctly even in the presence of failures or errors. Techniques like replication, failover mechanisms, and redundancy are used to enhance fault tolerance in distributed systems.

CAP Theorem:

    The CAP (Consistency, Availability, Partition Tolerance) Theorem states that in a distributed system, it is impossible to simultaneously achieve all three properties: Consistency, Availability, and Partition Tolerance. System architects must make trade-offs based on their application's requirements.

Eventual Consistency:

    Eventual Consistency is a concept where, in a distributed system, all replicas will eventually converge to a consistent state, given enough time and in the absence of new updates. Eventual consistency relaxes strict consistency requirements to improve system availability and performance.

Distributed Transactions:

    Distributed transactions involve coordinating multiple operations across different nodes to ensure data integrity and consistency. Distributed transactions are challenging due to the need to handle potential failures and ensure atomicity, consistency, isolation, and durability (ACID properties) across distributed resources.

Distributed Coordination and Consensus:

    In distributed systems, coordinating actions across multiple nodes can be challenging. Distributed consensus algorithms, like Paxos and Raft, enable nodes to reach an agreement on shared decisions, ensuring consistency in the face of failures.

Fault Detection and Recovery:

    Detecting failures and recovering from them is crucial for maintaining system availability. Techniques like heartbeat mechanisms, health checks, and automated failover systems help identify and handle node failures.

Load Balancing:

    Load balancing distributes incoming network traffic across multiple nodes to ensure each node handles an equitable share of the workload. Load balancing enhances performance and prevents individual nodes from becoming overloaded.

Distributed Transactions:

    Distributed transactions involve coordinating multiple operations across different nodes as part of a single transaction. Ensuring data consistency and integrity across distributed resources is a challenging task in distributed systems.

Geographical Distribution:

    Geographical distribution involves deploying nodes across multiple data centers or regions, enabling better performance and disaster recovery capabilities.