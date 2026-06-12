# Kitwork Cluster

> One Runtime. Many Roles. One Source of Truth.

Most frameworks begin with code.

Kitwork begins with a question.

## Why does software become more complicated as it grows?

At the beginning, everything is simple.

A single application. A single database. A single server.

Then reality arrives.

We need caching. We add Redis.

We need queues. We add RabbitMQ.

We need events. We add Kafka.

We need discovery. We add Consul.

We need orchestration. We add Kubernetes.

Every technology solves a problem.

Every decision makes sense.

Yet somehow, years later, the infrastructure becomes larger than the application itself.

The team spends more time operating the system than improving the product.

The architecture becomes a collection of technologies rather than a coherent platform.

Kitwork was born from the belief that there is another way.

---

# Runtime Before Framework

A framework answers:

> How should code be written?

A Runtime answers:

> How should code live?

Execution. Scheduling. State. Coordination. Resilience. Failure. Recovery.

These are Runtime concerns.

And they exist long before a web framework, an API router, or a template engine enters the picture.

---

# One Runtime, Many Roles

Kitwork intentionally avoids creating different kinds of servers.

There is no special Gateway binary. No special Coordinator binary. No special Worker binary.

There is only **Kitwork Runtime** — one binary, one engine, one language.

The only thing that changes is responsibility:

| Role | Responsibility | Held by |
| :--- | :--- | :--- |
| **Gateway** | Accept traffic, route to tenants, serve static & cached responses | Any node with a public address |
| **Coordinator** | Schedule jobs, assign tenants to nodes, watch heartbeats | Exactly one node at a time, by lease |
| **Worker** | Execute tenant scripts and background jobs | Every node, always |

A role defines responsibility. A Runtime defines capability.

Responsibilities move. Capabilities remain.

This is why a Kitwork cluster degrades instead of collapsing: any node can absorb any role, because every node already knows how.

---

# The Four Invariants

Philosophy becomes architecture only when it can be falsified.

These four rules are the contract. Any design that violates one of them is wrong, no matter how convenient.

### Invariant 1 — State outlives machines

The Database Cluster is the **only** memory of the system.

Node RAM may hold only what can be regenerated: caches, compiled bytecode, warm connections.

If deleting a node loses data, the design is broken.

**Corollary:** the cluster is exactly as available as its memory. A single-instance Postgres means a single point of failure — high availability of the Database Cluster (streaming replica + automatic failover) is therefore **phase one**, before any other cluster work. It is dishonest to call machines temporary while the memory lives on one machine.

### Invariant 2 — Correctness never rides the Bus

The Cluster Bus is allowed to lose messages. Therefore it may carry only things that tolerate loss:

heartbeats, cache invalidation, config hints, metrics, gossip.

Anything that must be *correct* — leader election, job claims, tenant assignment — goes through the Source of Truth, using primitives that cannot split-brain:

```sql
-- Coordinator election: a lease, not a vote.
-- One row. One holder. One expiry. Postgres is the referee.
UPDATE cluster_lease
SET holder = :node_id, expires_at = now() + interval '10 seconds'
WHERE name = 'coordinator'
  AND (holder = :node_id OR expires_at < now());
```

The node that wins the row is the Coordinator. The node that loses does nothing.

No consensus protocol to implement. No election bugs to debug at 3 AM.

The day Kitwork outgrows lease-based election is a good day — it means the product won. Until then, do not build Raft.

### Invariant 3 — Lose efficiency before losing availability

Normal operation:

```text
Client → Gateway → Coordinator → Worker
```

Worker unavailable — the Coordinator executes:

```text
Client → Gateway → Coordinator (execute)
```

Coordinator unavailable — the Gateway executes:

```text
Client → Gateway (execute)
```

Severe cluster failure — serve what the memory still holds:

```text
Client → Gateway → Database Cluster (static & cached responses)
```

Performance may decrease. The system continues. The Runtime adapts.

### Invariant 4 — Every workload is bounded

This is where the language and the cluster become one idea.

Kitwork scripts cannot contain `while`. Cannot recurse forever. Every execution is metered by energy and terminated at its limit.

This is not a style preference. It is the precondition for Invariant 3:

a Coordinator can safely absorb Worker traffic **only because** no workload can run unbounded. Degradation without bounded execution is just a slower way to die.

The language is the cluster's immune system.

---

# Database Cluster

Every distributed system requires memory.

Not memory in RAM. Memory in identity — what the system is, what happened, what must survive.

The Database Cluster stores:

* Business data
* Cluster state: node registry, leases, tenant assignment
* Jobs and their outcomes
* Events
* Configuration

The node registry is deliberately boring:

```sql
CREATE TABLE cluster_node (
    id          text PRIMARY KEY,   -- stable node identity
    address     text NOT NULL,      -- how peers reach it
    roles       text[] NOT NULL,    -- capabilities it advertises
    heartbeat   timestamptz NOT NULL,
    version     text NOT NULL,      -- runtime version, for rolling upgrades
    started_at  timestamptz NOT NULL
);
```

A node is alive if its heartbeat is fresh. A node is gone if it is not. There is no third state.

Machines are temporary. State is permanent.

The cluster is not defined by the servers that run it. It is defined by the state it preserves.

---

# Cluster Bus

A cluster is not a collection of machines. A cluster is a conversation.

The Cluster Bus is the nervous system: heartbeats, scheduling hints, tenant migration signals, cache invalidation, system events.

Its delivery guarantee is honest: **at-most-once**. Messages may be lost, and the system must remain correct when they are — because correctness lives in the Database Cluster (Invariant 2), the Bus only makes the cluster *faster*, never *right*.

Transport: the Kitwork SSE tunnel (two-way channel) — the same primitive offered to tenants, used by the platform itself. One mechanism, two audiences.

The Database Cluster is the memory.

The Cluster Bus is the nervous system.

The Runtime is the organism.

---

# Tenant State and Migration

A tenant can move between nodes only if the node holds nothing the tenant cannot lose.

The boundary is explicit:

| Lives in node RAM (disposable) | Lives in Database Cluster (permanent) |
| :--- | :--- |
| Compiled bytecode | Tenant data |
| `.cache()` entries | Jobs and schedules |
| VM pools, connections | Counters, sessions, anything behind `state.get/set` |

Scripts that keep counters in VM globals work on one node and silently break on two.

The platform must offer tenants a first-class `state` API backed by the Database Cluster, so the correct way is also the easy way.

Migration then becomes unremarkable: update the assignment row, invalidate via the Bus, cold-boot on the new node in milliseconds. Nothing to copy, because nothing precious was there.

---

# What Kitwork Is Not Trying To Be

Kitwork is not trying to compete with Redis, replace Kafka, or become another framework.

Those are implementation details of a different philosophy.

The objective is a platform where every component shares one worldview:

understandable after years of growth, operable after years of complexity, continuity over perfection.

---

# Roadmap

Each phase is useful on its own. No phase requires the next.

1. **Memory first** — Database Cluster HA: streaming replica, tested failover. *(Invariant 1 made real.)*
2. **Identity** — `cluster_node` registry + heartbeat writes from every runtime.
3. **One referee** — lease-based Coordinator election on Postgres. *(Invariant 2 made real.)*
4. **Nervous system** — Cluster Bus over the SSE tunnel: heartbeat gossip, cache invalidation.
5. **Movement** — tenant `state.get/set` API, then tenant migration.
6. **Degradation drills** — kill nodes on purpose, in production, on a schedule. Invariant 3 is only true if it is rehearsed.

---

# Final Thought

A strong system is not a system that never fails.

A strong system is a system that continues operating after failure occurs.

Kitwork exists to build that system.

One Runtime. Many Roles. One Source of Truth.
