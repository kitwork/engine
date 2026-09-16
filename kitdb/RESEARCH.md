# KitDB Research Map

This document records architecture lessons that may be useful to KitDB and the
evidence required before adopting them. It is not a feature checklist, release
roadmap, or on-disk format commitment. [VISION.md](VISION.md) remains the
canonical product direction; [FORMAT.md](FORMAT.md) and executable tests remain
the authority for implemented storage behavior.

## Objective

KitDB should become stronger without making every database pay for every
capability. The target is one small trustworthy transaction kernel serving
many sovereign databases, with optional data engines built from ordered
history:

```text
KitDB transaction truth
    |
    +-- WAL, recovery, snapshots, catalog, KROW mutation segments
    +-- ordered transaction history
            |
            +-- backup and replica adapters
            +-- full-text search projection
            +-- KCOL analytical projection
            +-- vector and graph projections
```

One runtime may provide all of these capabilities. They do not need to share
one correctness boundary.

## Fixed boundaries

The following rules are architecture commitments, not research hypotheses:

1. WAL, the canonical main file, and the transactional catalog are primary
   truth.
2. Search, analytics, vectors, graphs, and materialized views are derived state
   with an explicit source transaction and measurable lag.
3. A derived projection must be disposable and rebuildable without changing
   primary data.
4. Cold databases must not retain an index, cache, worker, or native process
   merely because another tenant uses that feature.
5. Every queue, cache, open handle, background task, segment set, and retry
   loop needs a bound or backpressure policy.
6. The pure-Go baseline must remain usable and correct without an optional
   accelerator.
7. A physical single-file representation is a packaging option, not a reason
   to merge independent durability responsibilities.

## Start from the current system

Research must solve the implementation that exists, not an older imagined
one. Format v3 already makes normal checkpoints incremental: it appends the
sorted WAL overlay and a manifest while retaining immutable older segments. A
full streaming rewrite occurs during legacy migration or when the bounded
32-segment set is compacted.

Retained history provides a verified contiguous transaction stream. Verified
anchors, local restore to an explicit retained transaction, durable named
transaction/checksum pins, and explicit whole-segment pruning are implemented.
Explicit local one-way replica bootstrap and catch-up are also implemented.
Process-local age/byte retention is implemented as typed node maintenance after
checkpoint publication, reusing whole-segment pruning and treating durable pins
as authority. Artifact selection, network transport, and broader continuous
scheduling remain node work; replacing the primary index is not.

## Adoption order

| Order | Area | Status |
| --- | --- | --- |
| Baseline | Verified standalone backup anchor | implemented |
| 1 | Restore by transaction | implemented |
| 2 | Retention/version pins | implemented |
| 3 | Local one-way replica bootstrap and catch-up | implemented |
| 4a | Host-wide shared handles, lease ownership, idle LRU, page-cache/open bounds | implemented in `kitdb/node` + Kitwork Engine |
| 4b | Host-wide memory, file-descriptor, fairness, and maintenance-I/O governance | next node evidence gate |
| 5 | Selective large-value separation | benchmark candidate |
| 6 | KCOL analytical projection and vectorized scans | projection candidate |
| 7 | BM25, exact vector search, then optional ANN | projection work |
| Experiment | ART and PGM-style indexes | adopt only after a measured win |

The order is intentional. A faster index does not compensate for an untested
restore or an unbounded tenant resource cost.

## Continuous backup and replicas

Streaming ordered history is the most immediately useful lesson from systems
such as Litestream, database log shipping, and immutable object storage.

A KitDB backup design should contain:

```text
verified snapshot anchor at transaction N
    + contiguous immutable transaction chunks after N
    + manifest naming the database identity and valid chain
    + checksums and previous-chunk linkage
    + a restore verifier that rejects gaps and wrong identities
```

Transactions should be grouped into bounded chunks. One transaction must not
automatically become one remote object. Chunk size, maximum delay, retry state,
and local spool size need explicit bounds.

The durability modes must remain honest:

```text
asynchronous backup
    local commit acknowledges after local durability
    remote RPO equals measured replication lag

synchronous replication
    commit acknowledges only after the configured remote durability boundary
    higher latency and availability cost are explicit
```

No asynchronous object-store adapter may claim remote RPO zero. Backup is not
complete until restore from a snapshot plus transaction chunks is tested.

## Multi-tenant resource governance

The most important optimization for thousands of databases is often avoiding
work, not making one hot lookup faster.

The first `kitdb/node` manager now classifies database handles by lifecycle:

```text
cold
    no open handle, page cache, projection reader, or worker

warm
    bounded metadata and an open handle admitted by a global budget

hot
    workload-justified cache and projection readers under tenant quotas
```

Implemented controls include:

- a bounded shared-handle registry with lexical path canonicalization;
- explicit leases that prevent eviction during an operation;
- a global-per-manager open-handle and reserved page-cache budget;
- a least-recently-used idle list plus explicit `TrimIdle`;
- bounded concurrent opens and context-cancelable admission waits; and
- fleet lifecycle and reservation counters that inspect no tenant data;
- PostgreSQL-listener query admission bounded globally and per authenticated
  database, with a finite queue, weighted fairness and atomic metrics; and
- explicit warm database policy plus count/byte-bounded idle KCOL/search reader
  residency, LRU cooling and path-free observability.

Remaining controls include:

- host-wide file-descriptor and total-memory accounting across managers;
- measured per-database memory beyond page-cache reservations;
- measured resident-memory and payload admission beyond conservative directory
  and page-cache ceilings;
- background checkpoint, compaction, projection, and backup I/O budgets;
- fairness across independent listeners/processes and CPU/I/O rate policy;
- idle-time policy and bounded maintenance scheduling; and
- observable projection/replica lag and background queue depth.

An optional structure with zero queries must have near-zero resident cost.

## Selective large-value separation

### Lesson

WiscKey demonstrates that separating keys from large values can reduce the
bytes rewritten while sorting or compacting an LSM tree. It also demonstrates
the cost: extra indirection, less-local range scans, value-log garbage
collection, and a larger crash-consistency surface.

### KitDB adaptation

KitDB should not place every value in a value log by default. A candidate
design may externalize only values or fields above an explicit threshold:

```text
small typed fields        KROW inline
large TEXT/BYTES/BLOB     immutable blob reference
```

A durable reference must identify the blob region, offset, length, codec, and
checksum. Blob bytes may be written before the transaction that references
them; a failed transaction can leave reclaimable orphan bytes but must never
publish a dangling reference. Reclamation must respect snapshots, retained
versions, backup anchors, and replica watermarks.

Content-addressed immutable chunks may be a better fit than a universal
append-only value log when deduplication, verification, and transport matter.
That choice remains an experiment.

### Evidence gate

Adopt separation only when representative benchmarks show that:

- full compaction is materially dominated by rewriting large values;
- saved write amplification exceeds blob metadata and garbage collection;
- point and range reads remain within their latency budgets;
- crash, orphan, checksum, reclamation, and restore behavior are proven; and
- a tenant with only small values pays negligible additional cost.

## KCOL analytical projection

### Lesson

PAX improves cache locality by placing values of the same attribute together
inside a page. Modern analytical systems go further by using independently
readable column chunks, statistics, compression, and vectorized execution.
Data layout alone does not provide analytical performance.

### KitDB adaptation

KitDB should keep canonical mutation segments row-oriented and construct a
rebuildable blocked-column projection from a snapshot or ordered history:

```text
KCOL segment
    header and schema identity
    source transaction watermark
    stable row ordinals or key chunk
    null and visibility bitmaps
    fixed-width typed vectors
    variable-width offsets plus byte chunks
    per-column min/max/null statistics
    independently checksummed physical blocks
```

The first reader should evaluate predicate columns into a selection vector and
materialize projected columns afterward. Point lookup remains the row store's
job. Version one should use explicit projection declarations; workload-driven
`AUTO` layout would make format behavior, debugging, and benchmarks harder to
reproduce.

Calling this format KCOL avoids implying that it must be classic page-local
PAX. Its row-group and column-block sizes are benchmark parameters, not fixed
truths.

There is no assumed physical layout that dominates both row-oriented OLTP and
column-oriented OLAP. KitDB's diagonal path is one logical engine with more
than one workload-specific representation, joined by an explicit transaction
boundary rather than by compromising the canonical bytes.

### Execution before layout

KCOL must not be the first analytical experiment. First introduce a typed,
bounded batch representation at the record/query layer and run vectorized
operators over the existing KROW scan. This separates the gain from batched
execution from the gain produced by column locality:

```text
tuple-at-a-time KROW
        |
        v
vectorized KROW through KBatch
        |
        v
vectorized KCOL through the same operators
```

KBatch belongs above the trusted byte kernel. Its first form should use typed
contiguous vectors, validity bitmaps, selection vectors, cancellation, and an
explicit memory budget. A `[][]any` batch that preserves per-value allocation
would not establish the execution model being tested.

For the same query and committed boundary, KROW is the correctness oracle.
Random insert, update, and delete histories must satisfy differential tests:

```text
result(KROW at T) == result(KCOL at N plus delta N+1..T)
```

An exact result at transaction `T` requires a complete, verified delta chain,
stable row identity, and correct visibility of base rows updated or deleted
after `N`. A missing retained range, mismatched schema identity, corrupt block,
or excessive catch-up cost must cause a KROW fallback or projection rebuild,
never a partial analytical result. Snapshot consistency already exists in the
row kernel; cross-representation reconciliation is a separate contract that
must be property-tested and fuzzed.

The base/delta merge cadence is an operating control, not a fixed constant.
Merging too rarely makes every query scan a large row delta; merging too often
spends fleet I/O rewriting derived state. Initial KCOL scope should therefore
favor large append-mostly fact tables such as orders, events, logs, and click
streams. Update-heavy entity tables remain on KROW until an immutable
row-group plus delete/update-vector design proves its cost.

KCOL is allowed to use separate physical files or chunks while remaining one
logical KitDB capability. A one-file package is a later publication choice,
not a reason to couple projection recovery to the primary format.

### Evidence gate

Compare KROW and KCOL on the same typed corpus for:

- full-row point lookup;
- projected point lookup;
- high- and low-selectivity filters;
- count, sum, min, max, grouping, sorting, and top-K;
- cold and warm cache behavior;
- projection build throughput, lag, bytes per row, and peak memory;
- base/delta reconciliation cost and merge-induced foreground latency;
- differential equality with KROW across randomized mutation histories;
- cold-tenant overhead and fleet behavior with many projections disabled;
- schema evolution, cancellation, corruption, and rebuild behavior.

No fixed multiplier such as "5-10x faster" may be claimed before those
measurements exist.

## Mutable overlay indexes

The current Go map is the baseline for point lookup. It should not be replaced
because another structure has stronger theoretical properties.

Candidates serve different workloads:

```text
Go map
    point-heavy mutable overlay

sorted packed slice
    small overlays or immutable batches

Adaptive Radix Tree
    large mutable overlays requiring ordered, range, or prefix operations
```

An ART experiment should avoid one interface and heap allocation per node. An
arena-backed representation with compact offsets is more relevant to KitDB's
memory and GC goals. It must still beat the simpler alternatives at the key
sizes and mutation counts KitDB actually sees.

Measure lookup, insert, delete, ordered iteration, prefix seek, checkpoint
preparation, bytes per key, allocations, and GC work at multiple overlay sizes.
Results from non-Go main-memory systems are evidence to investigate, not a RAM
savings guarantee for KitDB.

## Learned indexes for immutable segments

A PGM-style index predicts a bounded key-position range and then performs a
local search. It does not produce an exact byte offset for free.

This approach is relevant only when:

- a segment contains enough sorted keys for sparse-directory memory to matter;
- keys have a binary comparable representation and learnable distribution;
- model construction is deterministic and cheap at seal time; and
- the bounded last-mile search beats the existing sparse directory.

The first experiment should target immutable numeric or monotonic KitID keys
and compare against binary search over the packed page directory. It must not
enter the mutable overlay or public format before demonstrating a meaningful
end-to-end win. Small tenant segments should continue using the simpler index.

## Search and AI projections

AI-native does not mean putting HNSW in the transaction kernel. Search state
should consume the same ordered history contract as analytics and replicas:

```text
transaction history
    +-- BM25 projection
    +-- exact flat vector projection
    +-- quantized vector projection when measured
    +-- ANN graph only above a workload-defined threshold
```

The existing pure-Go BM25 engine is the first reusable search foundation.
Exact vector scan should establish correctness, recall, filtering semantics,
and a benchmark baseline before approximate nearest-neighbor structures are
introduced. HNSW is optional derived state with explicit memory, build, delete,
recall, and rebuild budgets.

Hybrid retrieval, provenance, schema-aware ingestion, explainable plans, and
authorized AI operations are more central to KitDB's AI-native direction than
choosing one vector graph algorithm.

## Benchmark policy

The first standalone [file projection experiment](relational/PROJECTIONS.md)
now exercises typed KROW batches and blocked fixed-width KCOL batches through
the same aggregate operators, plus direct BM25 reads from a packed sidecar.
It remains opt-in and snapshot-only: no incremental freshness, text/grouped
columnar execution or production format commitment is implied.

Every candidate needs a baseline and a workload matrix. At minimum record:

- database and tenant count;
- records per database and key/value distributions;
- operation mix and concurrency;
- cold-cache and warm-cache results;
- p50, p95, and p99 latency plus throughput;
- resident and peak bytes per open and cold tenant;
- allocations, GC work, file descriptors, and goroutine count;
- logical bytes, physical bytes, read amplification, and write amplification;
- checkpoint, compaction, reopen, verification, backup, and restore time;
- projection build lag, failure recovery, and rebuild time.

Results must identify hardware, filesystem, durability settings, dataset seed,
and benchmark revision. A microbenchmark win does not authorize an on-disk
format change when end-to-end latency, recovery, or tenant density regresses.

## Claims KitDB must not make without evidence

- checkpoint or compaction is always ten times faster;
- PAX or SIMD makes analytics five to ten times faster;
- ART always saves forty to fifty percent of memory;
- a learned index opens or searches in zero nanoseconds;
- asynchronous cloud backup has remote RPO zero;
- object storage costs nothing;
- one executable or one physical file proves correctness; or
- combining many famous algorithms makes KitDB universally faster.

Use workload-specific language instead: describe the mechanism, measured
conditions, tradeoffs, and failure boundary.

## Research decision template

Before promoting an experiment, write down:

```text
Problem:
Current baseline:
Target workload and tenant shape:
Owning layer:
Primary or derived state:
Format and compatibility impact:
Memory, disk, CPU, and concurrency bounds:
Crash, corruption, cancellation, and retry behavior:
Fallback and rebuild path:
Benchmark and reference model:
Adoption and rejection thresholds:
```

Rejecting an elegant design after honest measurement is progress. KitDB's
advantage should come from choosing the smallest sufficient mechanism for each
workload while preserving one understandable source of truth.

## Primary references

- WiscKey: <https://www.usenix.org/conference/fast16/technical-sessions/presentation/lu>
- PAX: <https://www.cs.cmu.edu/~natassa/courses/15-721/papers/pax.pdf>
- APAX and AMAX: <https://arxiv.org/abs/2111.11517>
- Adaptive Radix Tree: <https://db.in.tum.de/~leis/papers/ART.pdf>
- PGM-index: <https://pgm.di.unipi.it/>
- HNSW: <https://arxiv.org/abs/1603.09320>
- Litestream architecture: <https://litestream.io/how-it-works/>
