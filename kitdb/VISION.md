# KitDB Vision

KitDB starts as the smallest database kernel that larger systems can trust.
The kernel owns typed durable state, transactions, recovery, indexes,
snapshots, and an ordered change history. Search, analytics, synchronization,
file ingestion, and AI features remain independent modules connected through
explicit adapters.

## Principles

1. Correctness precedes performance and features.
2. The trusted kernel stays small and deterministic.
3. Go is the implementation and public backend boundary.
4. Kitwork integrates with KitDB, but KitDB does not depend on Kitwork.
5. On-disk data is versioned from its first byte.
6. Derived indexes and projections never become the source of truth.
7. AI may propose plans, but deterministic code validates and commits them.
8. Performance claims require repeatable benchmarks and resource bounds.
9. Recovery behavior is executable evidence, not only documentation.
10. A feature that cannot be disabled and verified does not enter the trusted
    path.

## Direction

```text
KitDB kernel
    |
    +-- relational schema and typed query IR
    +-- row storage and secondary indexes
    +-- ordered change feed
    +-- search adapter -> independent search engine
    +-- analytics adapter -> independent vector engine
    +-- sync adapter -> local, remote, and backup transports
    +-- AI adapter -> typed, authorized, explainable plans
```

The initial milestones now prove durability, immutable checkpoint recovery,
bounded read snapshots, ordered range cursors, an opt-in Kitwork `struct()`/ORM
adapter with a persisted typed catalog and secondary indexes, and an
authenticated Hrana SQL-light adapter tested with an official libSQL client.
The next layer is compiler-owned Schema IR, explicit schema migration, a richer
storage-neutral query planner, and the independent search projection. SQL and
Hrana remain adapters rather than becoming KitDB's internal model.
