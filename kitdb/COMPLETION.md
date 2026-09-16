# KitDB Database Completion Contract

The bounded real-project admission and operations profile is documented in
[PRODUCTION.md](PRODUCTION.md); this map must not broaden that production claim.

This document keeps KitDB focused on becoming a dependable database before it
becomes a larger data platform. A new subsystem is justified only when it
closes a missing step in the database journey below or consumes an already
stable contract as a replaceable projection.

## The required journey

A usable KitDB release must prove this sequence through both a local Go-facing
adapter and the supported remote profile:

```text
create schema -> insert -> select -> update -> transaction -> index
      -> restart -> verify -> backup -> restore -> reopen and query
```

Every step must use one Schema IR, one row representation, one catalog, and one
durability path. SQL, Kitwork `struct()`, Hrana, and future protocols are
frontends over those contracts, not independent databases.

`TestKitDBDatabaseReleaseGate` now executes that complete sequence against one
database, then checks the restored file through both the catalog-hydrated ORM
and a real authenticated Hrana endpoint. The same release step also runs a
real `lib/pq.CopyIn` atomicity/rollback journey. The engine verification command
runs those oracles and writes bounded evidence to
`.artifacts/kitdb-database-gate.json`. A separate focused step keeps WAL-tail,
backup/restore, and hard-process index recovery tests visible rather than
treating a clean close/reopen as crash-safety proof.

The host-owned Production Supervisor now closes the local recurring-protection
step without adding another durability authority: registered policies share one
bounded dispatcher/worker pool, rediscover fully verified anchors after restart,
resume the durable source pin, perform independent logical-digest restore
drills, retain only verified same-identity anchors, and expose path-free
`ready`/`degraded`/`unsafe` health. An optional host-owned publisher now adds
exact-anchor destination read-back proof to the same readiness contract; the
built-in bounded directory publisher covers a separately mounted volume or
transfer spool and resumes idempotently after manager restart. Durable policy
discovery, native R2/S3 transport and credential ownership, proof that a path is
physically off-host, and alert delivery remain deployment-layer gaps.

The latest promoted type contract is Schema IR v7 exact UUID: one canonical
value drives admission, casts, keys/FKs, PostgreSQL OID 2950 and 16-byte binary
wire values, while pre-v7 UUID aliases remain legacy text without a silent
rewrite. Its focused regression gate includes alternate spellings, malformed
input, unique/secondary lookups, catalog, migration and reopen.

Schema IR v8 adds one optional, explicit integer partition policy for the
experimental analytics projection. `CREATE TABLE ... PARTITION BY HASH/RANGE`
and `ALTER TABLE ... SET/DROP PARTITIONING` publish catalog metadata only: KROW
remains the authoritative row layout, incompatible KCOL generations become
stale, and reads fall back to KROW until an explicit projection refresh
publishes a matching generation. RANGE generations deterministically cluster
at most 8,192 projected rows inside each immutable KROW extent; dirty history
can therefore rebuild one extent while unchanged extents remain reusable. This
is bounded analytical routing/local KCOL layout, not physical KROW sharding,
global repartitioning, transparent background work, or a production claim for
general partitioned tables. HASH remains summary routing only.

RANGE KCOL v4 now has cancellation and abrupt-process-exit publication evidence,
plus an executable backing-array ceiling below 11 MiB at 128 projected columns.
Standalone embedded SQL and the read-only PostgreSQL wire expose `PRAGMA
analytics_status(table)` as one bounded, path-free view of exact snapshot
freshness, expected layout, partition identity, generation/chunk/row counts and
live/obsolete bytes. It performs no row scan, repair or refresh; runtime block
validation and KROW fallback remain authoritative.

`EXPLAIN ANALYZE SELECT` now runs the ordinary SELECT executor exactly once and
reports its actual path, monotonic planning/execution timing and structured
KROW/KCOL counters while preserving planning-only `EXPLAIN`. KROW observation
counts logical index entries, point-lookup attempts, candidate rows and
predicate-matched rows; KCOL observation retains chunk/block/row skip and exact
metadata counters. Kernel-owned query-local cursor evidence now adds main-file
page reads/bytes/decoded records, KitDB LRU hits/misses/bypasses, immutable
generation entries and WAL-overlay entries for scalar and KROW-batch execution,
without global atomics or observation work on ordinary scans. KCOL scan reports
now separately audit successful block-header and requested-column payload reads
and bytes. A bounded checksummed table-level partition block directory can
reject blocks before header I/O, is rebuilt from verified payload, upgrades old
generations through byte-range references, and fails open to KROW on mismatch.
`EXPLAIN ANALYZE` exposes both directory pruning and actual KCOL reads. These
are KitDB `ReadAt` observations, not hardware-I/O claims beneath the operating-
system page cache, and KCOL byte counters exclude projection-open metadata. One
Engine-owned, exact-watermark reader per projection kind now avoids reopening
and decoding that metadata on warm queries, is bounded to a 2 MiB serialized
directory per kind, drains before publication/close, and reports hit/miss/bypass
through `ExecutionStats` and `EXPLAIN ANALYZE`. The embedded result retains typed
statistics; the same three-column inspection surface is queryable through a
read-only PostgreSQL connection.

## Current capability matrix

| Area | Current evidence | Completion gap |
| --- | --- | --- |
| File and recovery | Versioned main file, WAL, checksums, dual superblocks, deterministic recovery and verification; machine-readable `kitdb/1` kernel/relational profiles freeze the 1.x readable/writable versions and hard transaction limits | Same-commit Windows/Linux release evidence, deployment-filesystem restore drills, hardware power-loss qualification, and the 24-hour KitDB canary required by `RELEASE_1_0.md` |
| Transactions | Atomic kernel transactions; `db.transaction(tx => ...)` with fixed-snapshot read-your-writes, a catalog revision bound to the same kernel snapshot, statement rollback, cancellation and optimistic data/schema conflict; one-request Hrana `BEGIN ... COMMIT` batches bounded to 256 steps; bounded PostgreSQL connection transactions share one lazy fixed snapshot across `SELECT`/`INSERT`/`UPDATE`/`DELETE`, publish one WAL transaction, expose failed state and SQLSTATE `40001`, and release on rollback, timeout, disconnect, or shutdown; the separate GUI DDL envelope stages exactly one supported `DROP TABLE`, `DROP INDEX`, `ALTER TABLE ... ADD/DROP CONSTRAINT`, `ALTER TABLE ... RENAME TO`, or `ALTER INDEX ... RENAME TO` until `COMMIT` | Savepoints, deferred constraints, retry policy/telemetry, and a wider independent-client compatibility matrix; schema DDL is not mixed with data transactions |
| Schema | Durable versioned Schema IR catalog (v2 stable fields/indexes, v3 sequence identity, v4 NUMERIC precision/scale, v5 temporal meaning/precision, v6 VARCHAR/CHAR length), allocation-light revision checks, atomic catalog-owned map replacement across independent ORM/Hrana/PostgreSQL proxies, stable struct/index identities and field tags, `struct()`, catalog hydration, atomic bounded multi-struct migration, additive SQL `CREATE TABLE`/`CREATE INDEX`/multi-field `CREATE UNIQUE INDEX`, bounded atomic `DROP TABLE` for SQL-created structs with `RESTRICT` semantics (and harmless `CASCADE` compatibility when nothing depends on the target), resumable generation retirement for ordinary `DROP INDEX` on SQL-created structs, metadata-only catalog-owned table/index rename with dependency retargeting and old-name reuse isolation, bounded atomic `ALTER TABLE ... ADD/DROP CONSTRAINT` for catalog-owned named single/composite unique, foreign-key, and check constraints, table-level named single/composite `UNIQUE`/`FOREIGN KEY`, field-tag `CHECK`, planner-gated `ALTER TABLE` add/rename-column/drop-column/type changes, checksummed shadow-row generations for physically independent fields beyond 10,000 rows, and explicit catalog-owned `ALTER PRIMARY KEY` with a table write fence plus atomic row/index-generation cutover; both generation paths have bounded admission, durable status, node-owned coalesced chunks, pre-cutover cancellation, restart, epoch publication, and cleanup evidence | Push-based catalog invalidation across processes, strict zero-pause scheduling, generation protocols for dependent unique/foreign-key rekeys and other dependent field changes, explicit source-declared destructive intent, online/segmented table drop and constraint validation/removal, constraint-backed/source-declared index drop or rename, source-declared primary/constraint changes, source-declared table rename, cascading removal of dependent constraints, and batch cutover after multi-transaction online index preparation |
| Typed rows | Checksummed tagged binary rows, unknown-tag preservation, strict scalar/JSON/array/vector/blob validation | Generated/static codecs remain optional future optimization |
| Constraints | One ordered primary key of one or more fields, not-null, generated or named single/composite unique, choice, defaults, bounded structured `CHECK`, and generated or named single/composite immediate `RESTRICT`/`NO ACTION`/`CASCADE`/`SET NULL`/`SET DEFAULT`; `.key()` preserves the single-field contract while `.key(1)`, `.key(2)`, ... declare a native composite physical row identity; catalog-owned unique/foreign-key/check addition and removal plus dependency-free primary-key replacement, dirty-row preflight, collision rejection, physical row/index publication and cleanup, PostgreSQL rollback/commit and SQLSTATE, action trees, restart hydration, rename-stable identity, rollback, and bounded migration validation have executable evidence | Deferred constraints, `MATCH FULL`, ordinary `UPDATE` of primary-key values, dependent primary rekeys/drop cascading, source-declared destructive intent, and larger segmented constraint-validation/removal jobs remain outside the current contract |
| CRUD | Create, point/equality reads, bounded scans, update and guarded delete through ORM and SQL-light; SQL `UPDATE SET` expressions and residual mutation filters share one snapshot/savepoint/constraint/index pipeline; atomic SQL inserts up to 256 rows, primary/single-field/composite-unique upsert targets, explicit transaction composition across structs, KitDB-only `createMany` for up to 10,000 rows in independently durable batches of at most 256 with exact resume/failure offsets, PostgreSQL text/CSV `COPY ... FROM STDIN` with frame-independent decoding, typed fields, atomic rollback, cancellation/drain behavior, bounded weighted admission globally and per app/database identity, and explicit byte/record/time bounds, plus a bounded CSV/JSONL `kitdbimport` orchestrator whose CRC32C-protected KIMP watermark commits with each chunk, whose SHA-256 prefix proof makes restart/replay deterministic, and whose durable status/verify/cancel/forget lifecycle preserves committed rows | `COPY TO`, binary COPY, server-side file paths, compressed/object-store readers, fleet-wide fairness across independent listeners, bulk update APIs, and joined updates/deletes remain outside the profile |
| Indexes | Primary and single/composite-unique point lookups plus versioned ordered secondary indexes; deterministic selection covers full equality, composite equality-prefix, next-field range, homogeneous ascending or descending index order, early stop, and bounded legacy rebuild; true reverse cursors merge immutable generations and WAL overlays without materializing the result; codec upgrades and ordinary secondary-index addition/replacement/removal use metadata-only admission independent of row count, checksummed physical generations, dual maintenance, planner gating, durable `PRAGMA index_status`, epoch-bound snapshots, atomic cutover, resumable cleanup, and node-owned coalesced background chunks that recover solely from `KIBS` | Lock-free/strict zero-pause admission and chunks, segmented/generation-based unique changes, partial-planner implication, and mixed-direction index layouts |
| Query | One bounded typed expression IR for scalar/projection/filter/order/JOIN/group/mutation evaluation; arithmetic, concatenation, `CASE`, common null/text/numeric functions, three-valued logic, planner-safe predicate extraction plus residual filters; bounded `DISTINCT`, ranges, index-provided forward/reverse order and early stop, streaming scalar and computed aggregates, multi-field `GROUP BY`/`HAVING`, up to seven snapshot-consistent indexed equality `INNER`/`LEFT JOIN` steps with explicit source/candidate/projection/order bounds, up to 16 exact-type `UNION ALL` branches sharing one snapshot with output-name/ordinal ordering and global paging, and non-recursive sequential CTEs/aliased derived tables sharing that snapshot under query-wide row/32-MiB/count/field/nesting materialization bounds; unordered non-distinct row/scalar shapes materialize directly with per-row admission before retained allocation, while every variable-cardinality buffered executor reserves before retention: `ORDER BY`/`DISTINCT` source/result/identity/Top-N state, scalar and batch `GROUP BY` keys/values/exact accumulators/HAVING scratch/results, indexed-JOIN per-source environments and outputs, and `UNION ALL` branch references; the final SELECT over materialized relations shares the ledger and `EXPLAIN ANALYZE` reports retained/peak bytes; aliases, metadata-only PostgreSQL Describe, planning-only `EXPLAIN`, and exactly-once `EXPLAIN ANALYZE` with actual path/timing, exact logical KROW work, audited query-local main-file cursor/page/cache counters and typed KCOL counters; the independent relational package owns exact DECIMAL/NUMERIC literals and constrained precision/scale behavior plus Unicode-counted VARCHAR(n), padded/blank-insignificant CHAR(n), casts, comparisons/keys, grouping, catalog typmods and PostgreSQL binary wire values; explicit ORM/SQL `ANALYZE` stores checksummed exact row/index-entry counts plus fixed-memory distinct-prefix sketches, while same-transaction per-struct dirty markers prevent stale estimates without invalidation from unrelated commits | Recursive CTEs, correlated subqueries, joins whose source/target is materialized, spillable buffered execution, duplicate-eliminating `UNION`, `INTERSECT`/`EXCEPT`, window aggregates, expressions as group keys, non-equality/right/full/cross joins, hash/merge join planning, complete PostgreSQL NUMERIC result-typmod/negative-scale rules, collations/locale-aware text ordering and pattern indexes, histograms/correlation and a calibrated cost/resource model, OR-aware index union/intersection, OS-level I/O/cache telemetry, spillable/columnar analytics, and broader function coverage |
| Search projection | Durable weighted `SEARCHABLE` Schema IR; standalone embedded/PostgreSQL execution over one pure-Go immutable-segment manager; single/multi/all-field BM25, Vietnamese analysis, `_score`/`_snippet`, bounded residual filters, exact checksummed `_cursor` search-after, source/layout/schema watermarks, retained-history mutation catch-up, fixed-snapshot rebuild, restart reuse, stale/tamper rejection, configurable hard budgets, bounded warm-reader fleet residency, race coverage, a mixed multi-database pgwire benchmark, and a 13.77-million-row existing-projection canary | Explicit-transaction semantics, phrase/prefix search-after, JOIN/group/aggregate composition, arbitrary ranking/order, fairness across independent listeners, projection backup policy, crash-fault campaigns at every watermark/index publication boundary, and broader concurrent production soak |
| Remote | Bearer-authenticated Hrana JSON v2/v3 subset and data API with bounded atomic request batches; loopback PostgreSQL wire over one file or a standalone bounded directory node with virtual maintenance discovery, lazy shared relational/search owners, dynamic `pg_database`, suffixless selection, LRU handle/page-cache governance, explicit warm database policy, count/byte-bounded idle projection residency, and fair finite statement admission globally/per database with metrics | Broader protocol/client compatibility, TLS/SCRAM, per-database principals for the standalone node, cross-listener scheduling, measured RSS/CPU/I/O admission, and independent release packaging; no handle-pinning transaction across HTTP requests |
| Operations | Checkpoint, verify, verified backup, exact restore, checksummed commit timestamps, verified-base point-in-time restore/fork, retained history, process-local age/byte retention policy with durable-pin pressure, and one-way replica primitives; node checkpoints enforce configured policy after publication through the same whole-segment prune path, while explicit maintenance remains available; `cmd/kitdb` exposes versioned JSON version/inspect/verify/backup/restore/restore-time commands without duplicating durability logic or creating missing source files; `kitdb-verify` and `kitdb-release` produce commit/platform/profile evidence and the release gate owns bounded race, repeated kernel/catalog/import/index/analytics hard-crash, explicit analytics corruption/upgrade recovery, and seeded replica-soak campaigns; a bounded host Production Supervisor composes local anchors, exact destination read-back through an optional publisher, logical-digest restore drills, retention, retry, and path-free readiness; its built-in directory publisher is immutable, bounded, idempotent across restart, and fail-closed for corruption/identity/fork errors; maintenance-only PostgreSQL `CREATE DATABASE name` creates an empty independently writable fresh-identity KitDB, implicitly using the sole authorized source capability or explicitly `WITH CAPABILITY name`; `CREATE DATABASE ... FROM ... AS OF TIMESTAMP ...` creates an independent recovery fork; catalog-v2 metadata-only `ALTER DATABASE ... RENAME TO ...` preserves physical storage and identity, updates authorized session views, and has before/after-publication hard-crash evidence while v1 catalogs remain readable; a bounded tenant-local KitDB catalog persists SQL-managed logical/storage names, database identity, capability reference, and `creating/active/dropping` state without secrets; startup reconciles interrupted create/drop and lazily re-exposes active cold databases; the create/drop, empty-create, and rename hard-process matrices prove recovery without clean shutdown; PostgreSQL/HTTP authorization resolves the current referenced capability and hides orphaned targets; guarded maintenance-only `DROP DATABASE [IF EXISTS]` removes main/WAL/history/lock files only after active-session, identity, lease, maintenance, replica, source-conflict, source-schema, and capability-dependent checks; one composed ORM/Hrana-to-restored-query release oracle with focused crash-boundary evidence | Cross-platform restore drills and reviewed release qualification; durable policy discovery, native object-store transport/credentials, publication alerts, ownership transfer, force-drop, and distributed topology remain absent |

## Ordering rule

Work proceeds in this order unless a failing safety test forces a lower-layer
repair:

1. Close schema and typed CRUD semantics.
2. Close transaction and index usability.
3. Close query correctness and bounded planning.
4. Prove restart, backup and restore as one release gate.
5. Package and document the standalone database surface.
6. Continue search, analytics, vectors, ingestion and distributed composition
   as rebuildable consumers of the committed transaction stream.

Node governance, replication and projections may continue when they protect or
exercise an existing database invariant. They must not redefine the source of
truth or postpone a missing item in the required journey.

## Evidence gate

A capability is complete only when source, deterministic tests, fault/restart
evidence, resource bounds, and user documentation agree. An architecture note,
benchmark, parser acceptance, or successful happy-path call alone is not a
completion claim.
