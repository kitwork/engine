# KitDB Vision

This is the canonical statement of KitDB's product and engineering direction.
Roadmaps and physical designs may change. The intent and decision rules in this
document should change only through an explicit design decision.

## Founding thesis

KitDB is not an attempt to produce a smaller clone of SQLite, PostgreSQL,
DuckDB, TiDB, or any search engine. It starts from a smaller question:

> What is the smallest trustworthy data kernel from which a much larger data
> system can be composed?

The answer must be lightweight enough for one tenant, one local application,
one edge device, or one small node, while remaining strong enough to provide
typed data, relational constraints, transactions, recovery, indexes, history,
and remote access. A small database is not a weak database. "Small" means a
small trusted core, bounded ownership, explicit resource costs, and layers that
can be replaced without risking the source of truth.

KitDB begins inside Kitwork because Kitwork supplies a real multi-tenant host,
compiler, VM, schema authoring language, protocol surface, and workloads. It is
not merely a private storage implementation for Kitwork. The kernel remains
independent so it can become a useful open-source Go database on its own.

## Product identity

KitDB aims to be:

- embedded-first and remote-capable;
- pure-Go at the correctness boundary;
- relational and typed without making SQL its internal model;
- safe for one isolated tenant database and composable across many nodes;
- transactional for primary state and projection-oriented for other workloads;
- useful as a database before requiring a cluster or managed service;
- inspectable, benchmarkable, and recoverable without hidden infrastructure;
- a foundation for ingestion, search, analytics, synchronization, backup, and
  AI-oriented data operations.

KitDB should be PostgreSQL-shaped at the compatibility edge without becoming a
PostgreSQL implementation internally. Familiar SQL, type names, catalogs,
errors, and wire behavior reduce adoption cost; KitDB-native transactions,
recovery, projections, and resource ownership remain free to improve on that
shape. A compatibility claim must name the supported profile and evidence, not
imply full PostgreSQL behavior.

Likewise, KitDB may use a single file without copying SQLite's page model and
learn analytical execution from DuckDB without placing columns in the
transaction kernel. Compatibility and packaging are adapters. Correct data
semantics are the product.

## Learn without inheriting

KitDB comes later and should use that advantage. Existing systems are evidence,
not opponents and not templates that must be copied whole.

| System family | Lessons worth carrying forward |
| --- | --- |
| SQLite, libSQL | embedded ownership, WAL recovery, portable files, simple deployment, remote protocol ergonomics |
| PostgreSQL | relational semantics, constraints, transactions, indexes, observability, operational honesty |
| DuckDB | vectorized execution, column pruning, local analytical efficiency, direct data ingestion |
| Lucene, Tantivy | immutable search segments, postings, scoring, merge policy, bounded readers |
| FoundationDB | a small ordered kernel, layers, deterministic simulation, backpressure, shadow validation |
| CockroachDB, TiDB, YugabyteDB | ranges, placement, replication boundaries, row representations, independent analytical projections |

An adopted idea must become simpler in KitDB, fit its ownership model, and be
proved by tests and measurements. A famous design is not automatically the
right design. A novel design is not automatically better. We preserve old
truths about durability and transactions while remaining willing to replace
old assumptions about deployment, authoring, and composition.

## Diagonal scaling

"Scale diagonally" is the project shorthand for scaling by composition of
small sovereign nodes. It is a product and ownership model, not a claim that
vertical and horizontal scaling no longer exist.

A KitDB node should be independently useful and should own a bounded unit such
as a tenant, subspace, key range, replica, or derived projection. More nodes can
then add capacity by:

- placing different tenants or ranges on different nodes;
- isolating noisy workloads and failure domains;
- colocating hot data with the application that owns it;
- moving search or analytical projections away from transaction nodes;
- replicating ordered history to backup or read nodes;
- coordinating only where a cross-node invariant actually requires it.

```text
tenant or range
      |
      v
transaction node ---- ordered history ----> replica or backup node
      |                                      search node
      +------------------------------------> analytics node
```

The local node must not pay a permanent distributed-systems tax. Cluster mode
must be an added composition layer, not a rewrite of local semantics. Adding
nodes should increase aggregate capability without requiring one shared giant
database, one global cache, or one native search process to absorb every
tenant's workload.

## Database first, data system later

The build order matters. KitDB must first become a dependable database. Search,
analytics, synchronization, file ingestion, and AI features should then inherit
its typed state and ordered history rather than invent separate sources of
truth.

```text
CSV, Markdown, folders, streams, APIs
                 |
                 v
        deterministic ingestion
                 |
                 v
     KitDB typed transactional state
                 |
          ordered change history
                 |
        +--------+---------+-----------+
        v                  v           v
   search projection  column projection  sync/backup
        |                  |           |
        +------------------+-----------+
                           v
                    AI tools and plans
```

Ingestion adapters may parse a CSV file, watch a folder of Markdown files,
normalize APIs, or consume events. They produce typed records or mutations.
They do not bypass the transaction kernel. Search, columnar analytics, vector
indexes, graphs, and materialized views are rebuildable projections with an
explicit source transaction and lag. They do not become primary truth.

This design lets the current pure-Go search engine survive and evolve as an
independent projection engine. The same boundary permits a future analytical
engine to use vectors and column segments without turning the row store into a
compromised hybrid format.

## One schema contract

KitDB owns one deterministic Schema IR with stable struct and field identities,
logical types, defaults, constraints, references, indexes, search declarations,
and migration intent. SQL DDL and Kitwork `struct({ ... })` are two authoring
frontends for that same contract. Neither is a second catalog authority.

Kitwork's compact form remains a design compass and the first high-pressure
customer of the contract. It should not force application authors to repeat a
logical struct, table descriptor, validation schema, and agent schema for the
common case. At the same time, a standalone KitDB client must be able to create,
inspect, and use the same schema through SQL without loading Kitwork.

The compact syntax is not the storage representation. Its adapter normalizes
the declaration into Schema IR before publication. This internal separation
adds safety without adding author ceremony and lets both frontends share the
same type validation, catalog metadata, planner input, and migration rules.

```text
Kitwork struct({ ... }) ----+
                            |
SQL DDL -> bind -----------+----> deterministic KitDB Schema IR
                                      |
                                      +-- validation and binary row codec
                                      +-- relational catalog and migrations
                                      +-- storage-neutral Query IR
                                      +-- indexes and projection declarations
                                      +-- API and authorized agent shapes
```

The same database core should ultimately have three first-class doors: an
embedded Go API, PostgreSQL wire, and the Kitwork adapter. Hrana or later
protocols may add more doors. They share catalog, binder, typed plans, and
storage; only transport and authoring ergonomics differ. Arbitrary SQL text is
still parsed into typed plans rather than becoming the kernel's source model.

The Kitwork door is an in-process pure-Go path, not a protocol loopback.
`struct()` and the fluent query builder compile directly to KitDB Schema IR and
typed Query IR, then call the same relational owner used after standalone SQL
binding. They must not render SQL only to parse it again, serialize records as
transport JSON, call PostgreSQL/Hrana/HTTP, reopen the file, or maintain a
second relational executor under `work`. The small VM-value adapter is a
necessary language boundary; catalog, planner, constraints, migrations, row
codec and transaction semantics belong to KitDB. KitDB never imports Kitwork.

## AI-native, precisely

AI-native does not mean adding an embedding column and calling KitDB a vector
database. It means the data system is structured so an AI can understand,
propose, inspect, and operate it without being trusted to violate invariants.

An AI should work with stable schema identities and typed plans. It may propose
a query, migration, index, ingestion mapping, repair, branch, or mutation. The
system must then deterministically:

1. validate types, constraints, authority, and resource bounds;
2. explain the plan, affected data, cost, and destructive consequences;
3. run it in a bounded transaction, snapshot, branch, or shadow engine;
4. verify the result against explicit invariants;
5. commit through the same durable path used for non-AI work;
6. record enough history to audit or recover the operation.

AI is never the durability algorithm, authorization boundary, checksum, lock,
or final source of truth. AI-assisted engineering lets the project explore and
test more designs, but it raises rather than lowers the evidence required for a
correctness claim.

## Kernel and layers

```text
KitDB trusted kernel
    |
    +-- versioned file format and checksums
    +-- transaction ordering and durable journal
    +-- immutable checkpoints and deterministic recovery
    +-- bounded reads, snapshots, and resource accounting
    +-- ordered change history
    |
Record layer
    |
    +-- Schema IR, typed rows, references, constraints
    +-- secondary indexes, migration, Query IR
    |
Projection layer
    |
    +-- full-text search
    +-- columnar analytics
    +-- vectors, graphs, and materialized views
    |
Node layer
    |
    +-- tenant and range placement
    +-- backup, synchronization, and replicas
    +-- admission control, quotas, routing, and protocols
```

Only the trusted kernel decides whether primary bytes are committed. Every
higher layer consumes explicit contracts. A projection must be disposable and
rebuildable. A remote protocol must preserve the same transaction semantics as
an embedded call. A distributed layer must preserve local correctness rather
than replace it with eventual guesses.

## Safety before scale

KitDB should earn strength in this order:

1. deterministic bytes and versioned formats;
2. checksummed writes, fsync ordering, atomic publication, and recovery;
3. transactions, constraints, snapshots, and bounded concurrency;
4. verification, fault injection, reference models, and repeatable benchmarks;
5. history, backup, restore, and one-way replication;
6. projections and background maintenance with explicit watermarks;
7. range movement, replication, and cross-node coordination.

MVCC, LSM trees, B+Trees, Raft, Merkle trees, SIMD, mmap, and native libraries
are possible tools, not founding requirements. They enter only when a measured
workload and a verified invariant justify their complexity.

Pure Go is preferred because it keeps builds, debugging, portability, and
failure ownership understandable. Native acceleration may exist behind an
optional adapter, but KitDB correctness and its baseline capabilities must not
depend on an opaque native engine. One executable is a valuable distribution
goal; one physical file is a packaging choice, not proof of simplicity or
durability.

## Engineering principles

1. Correctness precedes performance and features.
2. The trusted kernel stays small, deterministic, and reviewable.
3. Go is the implementation and public backend boundary.
4. Kitwork integrates with KitDB, but KitDB does not depend on Kitwork.
5. On-disk data is versioned from its first byte.
6. Derived indexes and projections never become the source of truth.
7. AI proposes typed plans; deterministic code authorizes and commits them.
8. Performance claims require repeatable benchmarks and resource bounds.
9. Recovery behavior is executable evidence, not only documentation.
10. Every queue, cache, transaction, snapshot, segment set, and background task
    needs an explicit bound or backpressure policy.
11. A feature that cannot be disabled, observed, and verified does not enter
    the trusted path.
12. Local operation remains first-class as remote and distributed layers grow.
13. Tenant isolation is an ownership boundary, not merely a filter in queries.
14. Public format and protocol claims must be documented well enough for an
    independent implementation to validate them.
15. We do not claim superiority from architecture diagrams; we demonstrate it
    with failure tests, compatibility tests, and honest benchmarks.

## Decision filter

Before a major feature enters KitDB, answer these questions:

1. Which user or operator problem does it solve now?
2. Does it belong in primary truth, the record layer, a projection, or a node
   service?
3. What is the smallest invariant and API that preserve future replacement?
4. What memory, disk, CPU, cardinality, and concurrency bounds apply?
5. What happens on short write, failed sync, crash, corruption, cancellation,
   overload, and retry?
6. Can a deterministic test or reference model prove the behavior?
7. Does a single local node remain understandable and useful afterward?
8. Are we learning a proven idea, or copying complexity without its workload?
9. Can the result eventually be documented and maintained as open source?

If these questions do not have concrete answers, the feature remains a
prototype outside the trusted kernel.

## Incubation and open source

KitDB remains under `engine/kitdb` while file formats, APIs, fault tests, and
the Kitwork pilot are changing together quickly. It should move to a separate
repository when the boundary is stable enough that extraction reduces coupling
instead of creating release overhead. The package already enforces the most
important boundary: it does not import the Kitwork VM, runtime, tenant,
database, or search packages.

The open-source target requires documented formats, reproducible tests,
portable builds, explicit compatibility policy, benchmark datasets, and no
mandatory managed service. A future KitDB cloud or node coordinator should be
built from the same public kernel and protocols available to everyone else.

## Current direction

The current milestones prove durable immutable generations, dual-superblock
recovery, bounded read snapshots, ordered cursors, typed binary rows, stable
field tags, a core-owned transactional schema catalog, a compact Kitwork
`struct()` adapter that can hydrate catalog-only definitions, safe schema
planning, secondary indexes, a resumable physical-generation lifecycle, strict
Schema IR value enforcement, a bounded
commit coordinator, a public record transaction with fixed-snapshot
read-your-writes and optimistic conflict, and an authenticated SQL-light remote adapter whose first
additive `CREATE TABLE` path commits into that same catalog and hydrates both
the live ORM and a reopened generation. Ordinary SQL `CREATE INDEX` now reuses
the same checksummed, bounded and crash-resumable index builder, including
existing-row backfill and planner gating, instead of creating a protocol-owned
index path. Catalog-owned `ALTER TABLE ... RENAME TO` now treats the name as
mutable metadata over one immutable struct ID and retargets every catalog-owned
incoming reference atomically. Ordinary `ALTER INDEX ... RENAME TO` persists
one immutable physical index ID and remaps generation metadata without a
rebuild; reuse of either released name receives a fresh identity. Source-owned
schema and unfinished maintenance remain fail-closed. Independent frontends now
compare one allocation-light catalog revision at their entry points: unchanged
data commits retain immutable Schema IR pointers, while a changed catalog is
validated and atomically replaces the complete catalog-owned graph under the
file gate. Record transactions bind that revision to their kernel snapshot and
conflict rather than changing schema mid-callback. `ALTER TABLE ADD COLUMN`, `RENAME COLUMN`, `DROP COLUMN`, and
`ALTER COLUMN ... TYPE` also route through that planner. Source drift still
cannot authorize data loss; only explicit SQL grants one field-scoped
destructive intent. Drop dependency checks, deterministic casts, full bounded
row/constraint validation, and one-WAL publication leave the old catalog
untouched on any failure. Physically independent drop/type changes beyond
10,000 rows now use a bounded admission scan, checksummed durable cursor, and
disjoint shadow row generation. Source reads continue and source-schema CRUD
dual-writes both generations while node-owned maintenance validates and
materializes bounded 2,048-row/8-MiB chunks in one pass. A bad row pauses at the previous
durable cursor and can be repaired before resume. The final transaction
publishes catalog, audit, active generation, row-layout epoch, and cleanup intent
together; accepted ALTER, restart hydration, and compatible repair writes wake
one coalesced task that resumes both backfill and retired-prefix cleanup. Admission
and chunks still briefly hold the writer gate. Durable status exposes progress,
while pre-cutover cancellation first disables dual-write and then removes the
unpublished target through resumable bounded chunks. Strict zero-pause
scheduling, autonomous general-DDL progress, rollback after
publication, and dependent-field generation protocols remain unclaimed. A bounded Hrana batch can now carry one complete
`BEGIN ... COMMIT` or `ROLLBACK` transaction in one request without pinning a
database handle across HTTP requests. The local PostgreSQL adapter may pin that
same record-transaction mechanism to one bounded connection: a lazy fixed
snapshot provides repeatable reads and read-your-writes, one commit emits one
ordinary WAL transaction, data or catalog advancement produces an optimistic
conflict, and timeout/disconnect follows the same rollback path. This adds no
transaction semantics to the wire layer and does not make DDL part of the
record transaction; PostgreSQL remains an adapter over the kernel contract.
It also proves opt-in immutable
transaction history and verified
standalone backup anchors plus exact restore from an anchor and retained
transaction range. Durable named transaction/checksum pins now protect replica
and projection watermarks, while explicit whole-segment pruning advances
history without permitting a crash-created active gap. Explicit local one-way
replica bootstrap and catch-up now reuse those anchors, pins, checksums, and the
ordinary target WAL rather than introducing a second durability mechanism. A
bounded transport-neutral replica protocol v1 separates source batch reads,
idempotent target apply, and exact cursor acknowledgement; local catch-up uses
that same protocol instead of retaining a private direct-event path. A
deterministic pure-Go wire v1 now carries canonical WAL frames behind a
CRC32C-checked header and a SHA-256 envelope. Its first filesystem mailbox
stages, syncs, exclusively publishes, applies, acknowledges, and cleans batches
without creating a second target cursor or kernel background worker. The node
governor now composes that mailbox into three separately schedulable publish,
apply, and acknowledge jobs. It reserves every touched database or mailbox,
keeps one managed batch in flight, removes crash-left staging under exclusive
mailbox ownership, survives manager restart between stages, and exposes bounded
label-free pressure and lag counters. An optional local link controller now
reuses those exact jobs behind one shared dispatcher and a fixed worker pool.
It bounds registered topology and concurrent cycles, separates active and idle
cadence, applies capped retry backoff, exposes explicit per-link health only on
named inspection, and drains with the manager. Configuration is process-local;
restart reconstructs progress solely from the source pin, target WAL cursor,
and mailbox rather than a second cursor file. The first deterministic fault lab
now kills a child process without defers at six controller boundaries:
registration, batch publication, partial target WAL commit, ACK publication,
source-pin publication, and mailbox cleanup. Every case must reopen under a new
manager, converge to an exact no-change boundary, pass full source/target
verification, and leave no mailbox pressure. A seeded, bounded opt-in soak
repeats that oracle across an evolving source model.
An initial pure-Go host-wide node governor now shares handles by canonical
path, pins active use with leases, evicts cold idle handles by LRU, bounds
aggregate reserved page cache and concurrent opens, and applies
context-cancelable backpressure instead of retaining every touched tenant
forever. The same owner now runs a lazy, bounded maintenance pool: typed
checkpoint, verification, verified-backup, exact-boundary history-prune,
replica-catch-up, and filesystem replica jobs coalesce, reserve every touched
path, use bounded concurrency, preserve weighted priority, and drain on
shutdown. Reservation-only destinations and mailboxes serialize filesystem
ownership without consuming database handles or cache. A backup job can
safely resume an already-published anchor, proves source identity, seals retained
history through its transaction, and publishes a durable transaction/checksum
pin. That same anchor is the managed replica bootstrap. Catch-up reserves source
and target together, then reuses kernel checksum proofs and the target's ordinary
WAL; no second durability path exists. Managed pruning never widens the requested
boundary, remains blocked by the oldest pin, and can retry an already-published
base to finish physical debris cleanup. Kitwork uses soft and hard WAL thresholds
so normal checkpoints leave the request path while growth still has an explicit
backpressure ceiling. Commit publication and WAL durability do not depend on
that scheduler.

The same governor now accepts one stable Kitwork relational row-migration
driver. The file entry owns the relational gate shared by every lease, so a
foreground validation/commit and a background `KRMS` chunk cannot cross even
when they originate in different app runtimes. Each dispatch reloads durable
state, commits one bounded chunk, releases its lease, and rejoins the back of
the weighted queue. No tenant goroutine, in-memory migration cursor, or second
journal is introduced; restart, repair, cancellation, and cutover remain
defined entirely by catalog, row-generation metadata, and `KRMS` WAL records.

One separate stable secondary-index driver applies the same ownership rule to
codec upgrades and ordinary index generations. Foreground planning commits one
accepted metadata-only `KIBS` state without opening a row cursor; each node
dispatch reloads it, advances exactly one bounded build or cleanup chunk under
the file gate, releases the lease, and requeues at background priority only
after durable progress. Accepted DDL, catalog hydration after restart, and
compatible shadow-index writes are wake hints, not authority. Planner gating,
generation cutover, cleanup, and `PRAGMA index_status` remain defined entirely
by catalog, `KIGM`, and `KIBS` records.

The record frontend now also owns one deterministic access planner consumed by
both execution and `explain()`: primary, unique, composite equality-prefix,
next-field range, forward/reverse index-order, and fallback scan reports cannot drift
from the path actually taken. Secondary indexes use a versioned ordered codec;
legacy codec upgrades and ordinary index additions, replacements, and removals
advance through durable bounded chunks, remain invisible to planning until
atomic generation publication, and resume after reopen. CRUD maintains source
and shadow generations during replacement; epoch-bound kernel snapshots and
resumable cleanup preserve a complete reader view.
Covered homogeneous ascending and descending orders stop after the requested
limit. Reverse cursors seek sparse pages backward and merge immutable
generations plus the WAL overlay without materializing a forward result; mixed
directions still use bounded in-memory sorting. Explicit `ANALYZE` scans one
fixed optimistic snapshot and writes a checksummed derived record containing
exact table/index counts and fixed-memory distinct-prefix sketches. Later row
mutations mark only their own struct dirty in the same transaction, while
unrelated commits leave its statistics usable. Only a matching schema hash with
no committed or transaction-local dirty marker can influence planning; all stale states fail open to the deterministic heuristic. These estimates
choose between already-correct access paths and do not imply a calibrated cost
model. Streaming scalar
`COUNT`/`SUM`/`AVG`/`MIN`/`MAX` evaluates multiple SQL aggregates in one pass.
The SQL-light adapter also owns a bounded hash-group operator: direct scalar
group keys, aggregate/alias-aware `HAVING`, alias ordering, explicit 8-key/
16-aggregate/32-projection shape bounds, a 10,000-group intermediate ceiling,
and the ordinary 60/120 result bounds. Scalar `SELECT DISTINCT` lowers to the
same bounded operator instead of introducing another execution path. It is a row-engine
reporting primitive, not a columnar or spillable analytics claim. The remote execution plan now retains
SQL-precedence predicate trees, parentheses, `AND`/`OR`/`NOT`, common set/range/
pattern/null predicates, and three-valued NULL logic rather than flattening
expressions. A bounded typed expression IR also serves scalar SELECT,
computed row/JOIN/group projections, expression ordering, arithmetic,
concatenation, `CASE`, and a small deterministic function set. Planner-safe
conjuncts continue to choose indexes while the shared evaluator checks any
remaining `WHERE`/`HAVING` expression. Guarded `UPDATE`/`DELETE` use that same
residual evaluator inside one fixed-snapshot record transaction, collect at
most 10,000 matching rows, and publish constraints, rows, and indexes together.
`UPDATE SET` expressions all observe the original row; bounded `RETURNING` is
validated before mutation. Single-struct expression sorts retain an explicit 10,000-row input
ceiling; joined sorts remain under the 20,000-pair join ceiling, and both keep
the 1,000-row remote result cap. Atomic 256-row SQL inserts and
primary/single-field/composite-unique upsert targets reuse
the record transaction and statement savepoints rather than adding a bulk-write
durability path. The KitDB-only `createMany` API composes that same path into
independently durable batches of at most 256, accepts at most 10,000 rows per
call, and returns exact retry offsets without retaining an unbounded input or
introducing a second journal. PostgreSQL `COPY ... FROM STDIN` is the streaming
counterpart for independent clients: text and CSV frames decode directly into
the same typed row/constraint/index transaction, one COPY remains atomic, and
transport byte, record, transaction, and lifetime ceilings fail closed. Large
imports must orchestrate repeated bounded COPY transactions with an external
resume cursor rather than weakening statement rollback. Stable
`KITDB_TRANSACTION_CONFLICT` errors make optimistic retries machine-readable
through both `.safe()` and Hrana. No calibrated cost, histogram, window, or
general analytical execution claim is implied.

The same relational transaction now closes the immediate constraint loop.
Single and composite references support `NO ACTION`/`RESTRICT`, `CASCADE`,
`SET NULL`, and `SET DEFAULT`; descendants reuse ordinary mutation and index
maintenance, while one savepoint, a 10,000-row tree ceiling, depth bound, and
cycle guard keep failure atomic and resource-bounded. CHECK declarations from
compact `field.check()` or SQL DDL become one field-tag expression IR, compile
before publication, use three-valued SQL truth, survive restart/rename, and
validate existing rows during migration. This is constraint completion, not a
trigger system or deferred-constraint claim.

The SQL-light adapter also composes two structs through one bounded indexed
nested-loop `INNER` or `LEFT JOIN`. Both sides read the same kernel snapshot
or record-transaction overlay, the joined side must expose a primary, unique,
or leading active index field, and explicit source/pair/result ceilings prevent
an accidental repeated full scan from becoming tenant-wide resource debt.
The joined stream feeds the same scalar aggregate, hash-group, `HAVING`,
ordering, and `DISTINCT` operators as a single struct. Multi/non-equality/hash/
merge joins remain outside this milestone.

The active product priority is the database-completion line: finish one
coherent local and remote journey through schema, typed CRUD, indexes,
transactions, restart, backup, and restore before adding another projection or
distributed subsystem. The next record/frontend milestones are strict
zero-pause shadow admission, generation DDL for indexed/unique/dependent fields,
large constraint-validation protocols, partial-index implication semantics,
richer predicates, histograms/correlation and calibrated resource plans,
reader-backed streaming ingest, spillable projection analytics, and transaction
conflict telemetry/client compatibility evidence;
destructive DDL remains gated by explicit intent, dependency analysis,
migration planning, and recovery evidence.

The next replica safety milestone promotes the deterministic fault lab into a
long-running CI and qualified-filesystem campaign before reusing the same codec
behind URL/token transport. Physical power-cut evidence remains a separate
hardware qualification. Persistent topology, object-storage shipping, and
cross-node orchestration stay later. In parallel, the node milestone extends
ownership into measured total memory, operating-system file-descriptor,
admission-rate, and noisy-neighbor governance.
Online dual-write backfill,
compiler-owned Schema IR, richer storage-neutral planning, and independent
search and analytical projections follow behind that safety line. Distributed
range ownership comes only after one node is measurable, recoverable,
resource-bounded, and trustworthy.
