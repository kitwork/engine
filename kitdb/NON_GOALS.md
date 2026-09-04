# KitDB 1.0 Scope Boundaries

The current milestone does not attempt to provide:

- unqualified general-purpose production readiness. The bounded single-node
  profile in `RELEASE_1_0.md` is the only production target;
- indefinite compatibility across future major versions. The `kitdb/1`
  machine-readable contract protects the 1.x line, while a future 2.x format
  requires an explicit migration and new release contract;
- general SQL parsing, PostgreSQL SQL/behavior compatibility,
  constraint-backed or source-declared index `DROP` through `DROP INDEX`, unbounded/online table
  `DROP`, source-declared table `DROP`,
  source-declared table/index rename,
  primary/source-declared constraint changes, cascading removal of dependent
  constraints, unbounded/online constraint addition or unique-constraint removal,
  single-field `CREATE UNIQUE INDEX`, or descending/partial index DDL;
  the SQL-light frontend now supports additive `CREATE TABLE`, ascending
  `CREATE INDEX`, multi-field `CREATE UNIQUE INDEX`, table-level composite
  `UNIQUE`/`FOREIGN KEY`, bounded `CHECK`, all immediate referential actions,
  bounded `DROP TABLE [IF EXISTS] ... [RESTRICT]` for SQL-created catalog
  structs, ordinary `DROP INDEX [IF EXISTS]` through resumable generation
  retirement, bounded `ALTER TABLE ... ADD/DROP CONSTRAINT` for catalog-owned
  named single/composite unique/foreign-key/check constraints, and bounded `ALTER TABLE`
  add/rename-column/drop-column/type changes, catalog-owned table rename with
  atomic incoming-reference retargeting, and ordinary catalog-owned index
  rename with stable physical identity through the durable Schema IR
  catalog plus a bounded SQL-precedence CRUD core with common predicates,
  atomic multi-row insert, and primary/single/composite-unique upsert targets,
  but it is deliberately not a general SQL/DDL engine;
- compiler-owned static struct types or generated codecs; the current Kitwork
  adapter builds deterministic runtime Schema IR with persisted field tags,
  while `kitdb/sql` owns the logical type and decodable schema contract and the
  transaction kernel owns its bounded catalog envelope and identity;
- multiple, non-equality, right/full/cross, hash, or merge
  joins; window aggregates, expressions as `GROUP BY` keys, disk-spilling aggregation,
  columnar execution, or a cost-based query optimizer. One snapshot-consistent
  indexed equality `INNER`/`LEFT JOIN` composes with bounded scalar and
  multi-field hash aggregates, computed expression projections/filters, and
  scalar `DISTINCT`; deterministic
  equality-prefix/range/forward-or-reverse-index-order explain is available.
  Explicit `ANALYZE` supplies exact row/index counts and fixed-memory estimated
  distinct prefixes only while its schema matches and its per-struct dirty
  marker is absent. It may break equal structural planner scores, but has no histograms,
  correlation model, calibrated I/O/CPU cost, or claim to a general
  relational/analytical optimizer;
- full SQLite SQL, full Hrana v3 compatibility, or PostgreSQL compatibility;
  the experimental remote surfaces are Hrana JSON and a loopback-only bounded
  PostgreSQL protocol 3.0 profile over the same SQL-light adapter. Speaking a
  client wire protocol is not a claim that KitDB implements the corresponding
  database's dialect, catalogs, types, or behavior;
- a general database-cluster catalog, automatic file discovery/adoption,
  database ownership transfer, or `DROP DATABASE ... WITH (FORCE)`. The
  loopback node profile persists bounded SQL-managed
  empty databases and point-in-time recovery forks in a reserved tenant-local
  KitDB catalog and supports metadata-only logical database rename. Empty
  creation either inherits the session's sole authorized
  source capability or requires `WITH CAPABILITY`; it does not invent a new
  credential authority. The catalog references the current source capability
  without storing its token and is not distributed topology, membership, or
  consensus authority;
- automatic destructive field/value migration, unbounded destructive DDL,
  lock-free/strict zero-pause DDL, generation-based unique-index or dependent-field
  changes, autonomous general DDL, rollback after target
  publication, or one atomic schema batch
  that also contains a multi-transaction resumable secondary-index build. The
  explicit safe executor atomically publishes bounded changes spanning several
  structs, but bounds the whole batch to 10,000 inspected/rewritten rows. The
  one-time ordered-index codec upgrade and
  index-only ordinary secondary-index addition/replacement/removal use
  resumable chunks with atomic single-struct publication. Foreground admission
  persists one accepted `KIBS` intent without opening a row cursor; the
  node-owned worker owns all row, cutover, and cleanup progress, reloads `KIBS`
  for every coalesced background dispatch, and retains no schema or cursor
  authority in memory. These transitions must currently be deployed separately
  from other logical schema changes;
- source-driven field removal/type conversion, primary-key changes, dependent
  field removal, or single-field reference rewrites when a struct contains
  data. Explicit SQL field drop/type changes up to 10,000 rows use one atomic
  migration. Larger changes are segmented only for a physically independent
  field: bounded admission, checksummed cursor, one-pass online validation and
  shadow materialization, source-schema dual-write, atomic cutover, bounded
  cleanup, and node-owned background repair/resume. The process-local scheduler
  reloads checksummed `KRMS` for every bounded chunk and retains no migration
  cursor or body. Admission and chunks remain
  writer-gated, but no one gate hold performs a full-table preflight. Indexed,
  unique, CHECK, or foreign-key-dependent large fields still require future
  generation protocols for their derived state. A pre-cutover shadow target can
  be inspected and cancelled through bounded restart-resumable cleanup; legacy
  in-place rewrites and post-cutover state cannot. Named composite-reference metadata changes use bounded
  existing-row validation and can share the same atomic schema transaction as
  their referenced struct. Explicit SQL primary-key replacement is a separate
  catalog-owned, dependency-free generation path: reads stay online, table
  writes are fenced, and row plus ordered-index generations publish together;
- deferred foreign-key checking, `MATCH FULL`, mutable primary keys, or
  unbounded referential work. `NO ACTION`/`RESTRICT`, `CASCADE`, `SET NULL`,
  and `SET DEFAULT` are immediate actions inside one record transaction; one
  action tree is capped at 10,000 affected rows and depth 64;
- MVCC writers, write-conflict resolution, or unbounded/persistent snapshots;
  concurrently prepared transactions are ordered by one commit owner and do
  not carry an MVCC read timestamp. The Kitwork record adapter now offers a
  fixed-snapshot optimistic transaction with read-your-writes and rejects a
  stale commit; that frontend check is deliberately not claimed as kernel
  MVCC;
- interactive transactions retained across HTTP requests or Hrana batons;
  Hrana accepts only a bounded 256-step `BEGIN ... COMMIT`/`ROLLBACK` batch
  contained in one request. The PostgreSQL wire profile supports bounded
  connection-scoped record transactions and one staged supported schema DDL,
  but not unbounded idle sessions, mixed data/schema transactions, savepoints,
  or deferred constraints;
- online/nonblocking compaction or compaction concurrent with commits;
- mixed-direction index layouts or descending index DDL; homogeneous `ASC` or
  `DESC` queries can traverse one ascending physical index forward or backward,
  while mixed directions remain a bounded in-memory sort;
- an in-place B-tree, mutable physical pages, or startup independent of active
  page count; persisted directories are flat sparse indexes;
- automatic background scrubbing or repair of a page that fails verification;
- page compression, physical space reclamation without compaction, or retained
  historical snapshots; obsolete generations are implementation debris, not a
  public time-travel API;
- kernel-owned retention timers, pruning on the commit path, a managed
  backup-anchor catalog, automatic anchor selection, low-latency `AS OF`
  queries, or reconstruction of transactions that predate the selected anchor
  or current retained-history base; bounded age/size policy is process-local,
  node-governed, checkpoint-coupled or explicitly scheduled, and durable pins
  always remain authoritative;
- memory independent of sparse-key cardinality or uncheckpointed WAL changes;
- unbounded `COPY`, file/reader streaming ingest, or a second bulk durability
  path; `createMany` accepts at most 10,000 already-materialized rows and commits
  ordinary relational batches of at most 256 with explicit resume offsets;
- search, analytics, vector indexes, or AI execution;
- an authenticated network replication transport, persistent topology
  discovery, synchronous primary commit acknowledgement, leader election,
  branching, merge, or distributed consensus; wire v1, the filesystem mailbox,
  and its optional registered link controller are host-operated, local,
  bounded, and one-way;
- host-wide resource accounting across independent node managers, operating
  system file-descriptor discovery, idle-time eviction, or per-tenant quotas;
  the current node layer bounds shared handles, reserved page cache, concurrent
  opens, idle LRU ownership, typed maintenance jobs, touched-path reservations,
  explicitly registered local replica links, and explicitly registered local
  production protection policies within one manager;
- a durable production-policy catalog, automatic policy discovery, native
  R2/S3/object-store clients, credential storage, alert delivery, or
  tenant-controlled backup paths. Production policy and publisher registration
  are host-owned and process-local; verified anchors and durable history pins,
  not scheduler memory, remain restart truth. The built-in verified directory
  publisher can target a separately provisioned volume or transfer spool but
  cannot prove that path is a distinct physical failure domain;
- encryption at rest, authentication, or authorization;
- direct multi-process read access while a writer is open;
- confinement beneath a Kitwork tenant root.

The embedding adapter, not the standalone kernel, will confine tenant paths.
This milestone opens one exclusive process writer, accepts a bounded set of
concurrently prepared transactions, group-commits admitted requests, and
serves concurrent reads through that process. Single-statement Kitwork writes
still serialize relational validation through a node-owned per-file gate shared
with row-migration and secondary-index maintenance. Explicit
record transactions run callbacks outside that gate, use a fixed snapshot plus
local overlay, and fail with a conflict if another write wins before commit;
they do not move conflict detection into the kernel. Incremental checkpoints bound ordinary write amplification,
but a legacy migration or the 32-segment limit still requires a full streaming
rewrite. Opt-in retained history supplies a verified ordered transaction stream
for projection, backup, and replica adapters; it is not itself a backup,
network transport, or historical snapshot API. `CreateBackupAnchor` supplies a
verified standalone baseline, but the kernel does not upload, retain, or select
anchors on the caller's behalf. The node Production Supervisor may compose that
primitive with a host-registered publisher whose success contract requires
destination read-back verification; it does not put transport credentials or
another durability authority in the kernel. `RestoreToTransaction` locally
materializes one explicit anchor plus a verified history range. It does not
discover remote artifacts, manage retention, repair gaps, or provide an online
historical-query index. `BootstrapReplica` and `CatchUpReplica` compose those
local primitives. Replica wire v1 serializes the same bounded batch contract,
and the filesystem mailbox adds crash-safe publication plus ACK cleanup without
adding a cursor sidecar. Neither layer runs a background worker, discovers
peers, crosses an authentication boundary, uploads artifacts, or changes
primary commit acknowledgement. The node layer can schedule one explicit
publish, apply, or acknowledge step. It may also compose those same steps for
an explicitly registered local link using one shared dispatcher, a fixed worker
pool, bounded exponential backoff, and bounded health snapshots. Registration
is process-local and must be supplied again after restart; the controller keeps
one batch in flight, retains no transaction bodies or cursor sidecar, and does
not discover or persist topology, own peer credentials, or cross a network
authentication boundary.
