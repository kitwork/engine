# Experimental File Projections

Status: opt-in experiment, not the default storage engine or a 1.0 format
promise. No canonical KROW, catalog, WAL, or recovery envelope is changed.
No new module dependency is required.

## Layout

```text
test/
  data.kitdb           canonical rows, schema and ordinary indexes
  data.kitdb.wal       canonical transaction log
  data.kitdb.lock      canonical process ownership
  data.kitdb.analytics  one optional file with immutable columnar generations
  data.kitdb.search    one optional immutable BM25 snapshot file
```

The sidecars contain entries for multiple tables. They are regular files,
not directories disguised by an extension. The search file contains existing
native segment bytes, opened using section readers over one physical container
file; queries do not extract an archive or open one OS handle per segment. On
Windows, where Go serializes positioned reads on one `os.File`, the relational
reader uses two bounded handles for that same immutable file to match its
two-query admission gate. Other platforms retain one handle. Both paths share
one decoded directory and one native search-reader cache; this is not a second
copy of the projection or a tenant-facing tuning option.

Search construction still uses a temporary `.search-build-*` directory for the
existing bounded replacement writer and its identifier validation. It is
removed after success or ordinary failure/cancellation. Process termination
can leave unpublished temporary artifacts; automatic orphan cleanup is not
implemented. Do not delete unfamiliar files as a cleanup shortcut.

The existing mutable search-directory manager remains the default. Opt-in
refresh refuses to replace a legacy `.search` directory or symlink. There is
no automatic migration or deletion of existing user indexes. An explicit
offline `pack-search` operation can adopt an already-current, deletion-free
legacy index without scanning or tokenizing KROW. It verifies the exact source
transaction, database identity, schema, row generation/epoch and committed
index generation before atomically publishing the packed file. Missing, stale,
corrupt, or tombstoned input fails closed; use a normal legacy SEARCH catch-up
or a full `refresh-projections` rebuild first.

To compare with the mutable directory mode after creating a `.search` file,
turn off the experimental option and set a different `--search-root` directory.
Do not point the legacy writer at the packed file.

## Try It

From the engine repository, with an existing test database that is not open in
another process:

```sh
go run ./cmd/kitdb refresh-projections /path/to/test/data.kitdb

go run ./cmd/kitdb refresh-projections --analytics-only /path/to/test/data.kitdb

go run ./cmd/kitdb pack-search /path/to/test/data.kitdb

go run ./cmd/kitdb projections /path/to/test/data.kitdb

go run ./cmd/kitdb query --batch-aggregates /path/to/test/data.kitdb \
  "SELECT SUM(price), AVG(rating), COUNT(*) FROM products WHERE price >= 25 AND price < 75 AND enabled = true"

go run ./cmd/kitdb query --experimental-projections /path/to/test/data.kitdb \
  "SELECT SUM(price), AVG(rating), COUNT(*) FROM products WHERE price >= 25 AND price < 75 AND enabled = true"

go run ./cmd/kitdb query --experimental-projections /path/to/test/data.kitdb \
  "SELECT id, name, _score FROM products WHERE * SEARCH 'blue widget' LIMIT 20"

go run ./cmd/kitdb query --experimental-projections /path/to/test/data.kitdb \
  "PRAGMA analytics_status(products)"
```

`query` reports `execution.Path`: `krow-batch`, `kcol-batch`, or
`search-snapshot`. `execution.Fallback` explains an unusable analytics sidecar.
`ChunksScanned`/`ChunksSkipped` report routing at the immutable extent level.
`RowsScanned`/`Batches` describe decoded vector work,
`RowsSkipped`/`BatchesSkipped` describe blocks rejected from their statistics,
and `RowsFromMetadata`/`BatchesFromMetadata` describe blocks answered without
reading vector payloads. For accelerated GROUP BY, `execution.Groups` is the
number of groups formed before HAVING/LIMIT; grouped execution can skip blocks
but does not yet form groups from metadata. Discarded partial KCOL work is not
included in the final counters. `ProjectionCacheHits`,
`ProjectionCacheMisses`, and `ProjectionCacheBypasses` distinguish a warm
verified container lease from an open/directory-decode and from a directory
that deliberately exceeds the residency bound. Search execution additionally
reports `SearchReaderCacheHits`, `SearchReaderCacheMisses`, and
`SearchReaderCacheBypasses`; these refer to the native packed BM25 reader, not
the outer `.search` container.
Unchanged scalar/indexed paths currently omit these experimental statistics.

To use a PostgreSQL client, set `KITDB_TOKEN` and run:

```sh
go run ./cmd/kitdb serve --experimental-projections --readonly \
  --database test --listen 127.0.0.1:5442 /path/to/test/data.kitdb
```

Connect to database `test`, user `kitdb`, the configured token, SSL disabled.
This existing PostgreSQL listener is loopback-only, not a TLS/SCRAM service.
The same SELECT syntax works; no extra query dialect is needed. Stop the
listener before refreshing via the offline CLI. A host can instead call the
Go method on its already-owned Engine:

```go
db, err := relational.OpenWithOptions(path, relational.Options{
    ExperimentalProjections: true,
    // Explicit and per Engine. Zero keeps the default open/query/close path.
    SearchReaderCacheBytes: 16 << 20,
})
// Handle err; close db after all requests drain.
report, err := db.RefreshProjections(ctx)

// Or refresh columnar without creating, checking or replacing search storage.
report, err = db.RefreshAnalytics(ctx)

// Or adopt the exact current mutable search generation without decoding KROW.
packed, err := db.PackSearchProjection(ctx)
```

`PackSearchProjection` is a migration bridge, not a second ongoing write path.
The legacy search root must be distinct from `<database>.search`; databases in
a `.data` directory already use the historical sibling `search/` root. Other
layouts must pass a separate `SearchRoot` when building and packing the legacy
index. Packing still reads and verifies every immutable source segment and
copies its bytes, so it is bounded-memory but not zero-I/O. Its report exposes
the source transaction, table/document/segment totals, source and destination
bytes, and `CanonicalRowsScanned` (always zero).

`PreflightProjections(ctx)` is the equivalent standalone Go API. It captures one
canonical read snapshot and reports every analytics/search projection as
`missing`, `stale`, `invalid`, or `ready` without refreshing it or scanning KROW.
The report contains no host path. Analytics inspection reads the container
directory and KCOL/chunk headers. Search inspection reads the packed manifest,
each fixed-size segment header, and the eight-byte sparse-directory prefix
needed to reserve reader memory. It deliberately does not load sparse term
dictionaries or payloads. A `ready` preflight is therefore bounded admission
evidence, not a substitute for an offline deep verification campaign.
`reader_capacity_bytes` is the conservative per-table reservation used by the
packed reader cache; it excludes file payload, OS page cache, and per-query
working memory.

Process-local cache statistics separately expose native search readers and
physical search file handles. This keeps the Windows concurrency adaptation
observable without counting a second handle as a second resident reader or
doubling its capacity reservation. Trimming or closing the projection cache
drains leases and closes every handle before replacement/removal.

Callers may apply the same check before an Engine becomes visible:

```go
db, err := relational.OpenWithContext(ctx, path, relational.Options{
    ExperimentalProjections: true,
    ProjectionOpenPolicy:    relational.ProjectionOpenValidate,
})
```

The policies are:

| Policy | Admission behavior |
| --- | --- |
| `lazy` | Do not inspect projections during open; query-time checks remain authoritative |
| `validate` | Reject an existing structurally invalid sidecar; allow missing/stale state to use the safe KROW fallback |
| `require-ready` | Require every supported analytics and search projection to be present and exact-watermark ready |

Projection policy never changes KROW recovery, WAL publication or transaction
durability. A rejected Engine releases its kernel handle. `kitdb query` and
`kitdb serve` expose the policy as `--projection-open-policy`; it requires
`--experimental-projections` for any mode stricter than `lazy`.

Refresh is explicit and can be expensive. Ordinary `ANALYZE` does not start it.
Text-compatible fields opt into dictionary projection through the independent
catalog contract, either at creation or by metadata-only ALTER:

```sql
CREATE TABLE clicks (id BIGINT PRIMARY KEY, utm TEXT ANALYTICS);
ALTER TABLE clicks ALTER COLUMN utm SET ANALYTICS;
ALTER TABLE clicks ALTER COLUMN utm DROP ANALYTICS;
```

Kitwork lowers `text().analytics()` and `choice(...).analytics()` into that same
Schema IR flag; it is not a separate Kitwork-only storage rule. Numeric and
boolean fields retain their existing projection eligibility. There is still no
SQL statement that implicitly refreshes a sidecar on each write.

`RefreshAnalytics` and `--analytics-only` publish only `.analytics`; their report
has an empty `SearchFile` and zero `SearchTables`. They work beside an existing
legacy `.search` directory without migrating or deleting it. They share the
same per-Engine build admission and snapshot/publication checks as the full
refresh. The default command and `RefreshProjections` still build both files
from one source snapshot. Neither form schedules itself or runs on each write.

## Analytics Status Inspection

Standalone embedded SQL and the PostgreSQL listener expose one bounded,
read-only inspection statement:

```sql
PRAGMA analytics_status(products);
```

It returns one row with `status`, `fresh`, the predicted `query_path`, expected
layout, partition policy, source/current transaction watermarks, projected row
and chunk counts, chunk version, snapshot generation, and live/obsolete byte
accounting. Stable states are `disabled`, `unsupported`, `missing`, `stale`,
`invalid`, and `ready`. Only `ready` sets `fresh = true` and reports
`query_path = 'kcol-batch'`; every other state reports the safe KROW path.
`reason` is path-free so PostgreSQL clients do not receive a host filesystem
name. The statement also works on a read-only listener and observes the fixed
snapshot of an explicit transaction.

This PRAGMA does not refresh or repair anything, and it never scans canonical
rows. It opens one container handle and checks the bounded snapshot directory,
source/catalog/schema/layout watermarks, KCOL table header, chunk coverage and
partition metadata. It intentionally does not read every block payload merely
to display status. Refresh verifies reused payloads; query execution validates
the blocks it consumes and still falls back to KROW on a stale or unusable
projection. `ready` therefore means eligible at this exact snapshot, not a
replacement for an offline full-file verification campaign.

To inspect one real query rather than projection eligibility, use:

```sql
EXPLAIN ANALYZE
SELECT COUNT(*), SUM(price)
FROM products
WHERE category = 'keyboard';
```

The SELECT runs exactly once with its normal snapshot and resource limits.
KCOL execution exposes actual chunk/block/row pruning and metadata-answer
counters; stale or invalid snapshots expose the KROW path and fallback reason.
Plain `EXPLAIN` remains planning-only, and one-query timings are observations
rather than benchmark evidence.

## Incremental Source Decoding

Columnar refresh now divides each table into contiguous physical row-key ranges
of up to 8192 rows (eight 1024-row vector batches). The ranges cover gaps as well
as existing rows. INSERT into a previously empty gap, DELETE of the last row in
a range, and delete/insert key moves cannot fall outside this coverage. A growing
range splits on rebuild. These chunks do not create one OS file per chunk. A
declared partition policy adds a routing summary to each chunk; it does not
change the canonical row-key range or transaction ownership.

For each explicit refresh:

1. Capture one canonical catalog/data snapshot and its exact history cursor.
2. Validate the previous sidecar's source identity, table schema hash, row
   generation/epoch and chunk coverage. Unrelated catalog changes need not
   invalidate otherwise compatible tables for the purpose of rebuilding.
3. If the cursor changed, prove the interval from the old cursor to the captured
   cursor through retained history, including both cursor checksums. Mark all
   row PUT/DELETE ranges dirty; ignore transactions newer than the captured
   snapshot. An unchanged exact cursor requires no history walk.
4. Rebuild dirty/new/incompatible ranges from KROW. Validate unchanged KCOL groups,
   validating all column checksums, block statistics, validity flags, finite
   floats, boolean encoding and row counts, not just the columns requested by a
   query.
5. In a generational container, reference validated unchanged ranges in place and
   append rebuilt ranges plus a new directory. Publish through the other checked
   root slot, only after data/directory sync. Initial construction, v1 upgrade,
   and compaction use a temporary container and drained replacement instead.
   A reuse verification failure abandons the attempt and retries once from the
   same canonical snapshot without reuse.
   Cancellation and destination write failures return immediately, without that
   rebuild retry.

Changed-source reuse requires retained history covering that interval. A host
can enable it with `Kernel: kitdb.OpenOptions{RetainHistory: true}` when opening
the Engine; `serve --retain-history` also enables the existing kernel facility.
An existing retained-history directory is recognized on reopen, including by
the offline refresh command. Refresh does not enable retention implicitly.
Disabled/pruned/unproven history falls back to a full source-snapshot rebuild.
This implementation does not add persistent pins: concurrent pruning may force
that fallback, but must never permit reuse from an incomplete proof.

`ProjectionReport` (also returned as CLI JSON) exposes:

| Field | Meaning |
| --- | --- |
| `AnalyticsSourceRows` | KROW rows decoded during this refresh, including a discarded copy/rebuild attempt |
| `AnalyticsReusedRows` | Rows referenced or copied into the resulting generation |
| `AnalyticsBuiltChunks` / `AnalyticsReusedChunks` | Chunk counts in that generation, including empty ranges |
| `AnalyticsCopiedBytes` | Old KCOL group bytes copied, not total output bytes or physical I/O |
| `AnalyticsReferencedBytes` | Old KCOL group bytes kept in place, without copying |
| `AnalyticsWrittenBytes` | Bytes submitted to file writes, including roots/directories and a discarded attempt; not SSD/device write amplification |
| `AnalyticsFileBytes` / `AnalyticsObsoleteBytes` | Resulting container length and bytes outside the current live representation |
| `AnalyticsPublication` | `append`, `rewrite`, `compact`, or `unchanged` |
| `AnalyticsFallback` | Why reuse was abandoned, if applicable |

These counters describe successful refreshes (including a recovered retry).
An error can leave incomplete counters and unreachable append bytes; it is not
evidence that no I/O or publication occurred.

**Both source decoding and payload publication can now be incremental.** The
`KSNAP002` container keeps unchanged chunks at their physical offsets; logical
KCOL section readers join their extents without materializing the section.
Below the compaction threshold, a refresh with an identical snapshot validates
old groups and writes zero bytes. Changed-source append writes new KCOL headers,
rebuilt groups, a complete bounded directory, and one root page. It still reads
and verifies all reused KCOL bytes; this is not O(changed bytes) total work.

Explicit refresh compacts when obsolete bytes exceed the larger of live
container bytes and 1 MiB. It verifies/copies surviving groups and rebuilds dirty
ranges into a temporary file. Disk use then temporarily includes both containers.
The threshold is not a hard disk quota or a latency guarantee; an append can
cross it and the next refresh compacts. File-root recovery, replacement and
extent rules are described in `internal/snapshotfile/FORMAT.md`. Search still
uses the existing `KSNAP001` temporary-file writer. No automatic scheduler or
per-table query watermark has been added.

Chunk metadata is bounded by 16384 chunks per generation and a 4 MiB estimated
key/descriptor budget, within the container's existing 8 MiB directory limit.
The change visitor falls back after 100000 operations or 64 MiB of operation
key/value bytes between the cursors. These are not hard total-I/O or RSS limits:
`WalkHistory` checkpoints first and verifies the retained chain, including older
segments. That checkpoint can hold the kernel writer gate. Large retained
histories and concurrent writes need separate latency measurements before
automatic refresh is enabled.

## Refresh Policy (Target)

The agreed direction is bounded asynchronous batching, not synchronous columnar
maintenance on every INSERT and not periodic full-table rebuilds. The current
implementation above appends/refers to chunk contents with threshold compaction.
This section specifies the remaining scheduler and delta-aware query design,
not capabilities already available on ordinary writes.

- Canonical rows and ordinary indexes commit through the existing transaction
  and durable WAL boundary. Columnar work must not become an extra condition for
  acknowledging that commit. A RAM-only pending buffer is not recovery evidence.
- Changes make affected chunks dirty. Track INSERT, UPDATE and DELETE, including
  corrections to old timestamps and moves between chunk/partition boundaries.
  A chunk being old or quiet is not proof that its logical rows will never change.
- Batch eligibility combines changed-row/byte thresholds with the age of the
  oldest unapplied change. A quiet-period debounce may coalesce work but cannot
  reset that age indefinitely under continuous writes. No changes means no
  rebuild. Actual lag must be observable when resource limits prevent the target
  cadence; a timer is not a hard freshness or latency guarantee.
- Build only selected columns in affected chunks at a fixed source snapshot.
  Reuse unchanged immutable chunks. Publish a new generation only after its
  required bytes and metadata are complete; keep old generations until their
  readers release them. This does not require a separate file per chunk.
- A future current-snapshot query may combine sealed KCOL with source changes
  only when coverage, row identity, versions, updates and tombstones prove no
  missing rows or double counting. Otherwise fall back to canonical KROW. The
  current code always takes that fallback when the exact watermark differs;
  it must never silently serve an older analytics answer between refreshes.
- Source commit events may wake/coalesce maintenance, but expensive builds stay
  outside commit listeners. Durable history or a fixed-snapshot rebuild must
  repair missed events. Pin required history while consuming it and release
  pins after publication or abandonment. Do not invent a second durability log.
- A future host/node scheduler owns bounded shared workers, cancellation and
  I/O/memory budgets, rather than one unbounded queue or timer per cold tenant.
  Columnar and search may have different cadences and independent watermarks.

Chunk identity/coverage, differential mutations, restart, append publication and
compaction process-exit boundaries now have tests. Before scheduling automatic
refresh, add orphan-file recovery, narrower history proof/freshness, fleet
resource budgets and bounded-work/lag measurements. A process-exit test is not
a power-loss durability proof. Do not put explicit refresh on a frequent timer
as a substitute for this work.

## Partition Routing and Local Clustering (Implemented Experiment)

Schema IR v8 supports one explicit integer partition field per table. SQL and
the Kitwork authoring DSL lower into the same stable-field-tag contract:

```sql
CREATE TABLE clicks (
  id BIGINT PRIMARY KEY,
  user_id INTEGER NOT NULL,
  duration INTEGER NOT NULL
) PARTITION BY HASH (user_id);

CREATE TABLE events (
  id BIGINT PRIMARY KEY,
  sequence_id BIGINT NOT NULL,
  value INTEGER NOT NULL
) PARTITION BY RANGE (sequence_id);

ALTER TABLE events SET PARTITION BY HASH (sequence_id);
ALTER TABLE events DROP PARTITIONING;
```

```javascript
const clicks = struct({
  id: serial().key(),
  user_id: int().notNull().partition("hash"),
  duration: int(),
});
```

`partition("hash")` uses a persisted deterministic SplitMix64 contract with 64
buckets. Each analytics chunk stores the buckets represented by its non-null
values plus exact NULL/min/max metadata. `partition("range")` stores exact
NULL/min/max metadata without a hash mask. RANGE also publishes KCOL chunk
layout v4: rows are ordered by the partition value inside each immutable KROW
extent, with NULL values last, before they are emitted as 1,024-row blocks.
Every projected column follows the same deterministic permutation. Before
reading any KCOL block header, the planner may reject a complete chunk when an
AND filter proves it cannot match. A compact table-level directory can then
apply the same conservative proof to each physical block before its header is
read. Selected blocks continue through the existing
header-statistics/metadata/vector ladder. `GROUP BY` uses the same routing and
clustered blocks.

The block directory uses one fixed 26-byte entry per physical block and one
byte buffer per table, rather than one heap object per chunk. It stores exact
NULL/min/max plus the HASH mask already represented by the chunk summary. The
checksummed snapshot directory protects both levels. Refresh recomputes a
reused chunk's summaries and directory from verified payload before referencing
or copying it. A malformed or mismatched value makes the projection unusable;
execution falls back to canonical KROW rather than risking a false negative.
Legacy projections without the optional directory continue through block
headers. When the complete directory fits its bound, refresh can publish a
metadata generation referencing existing KCOL bytes instead of rewriting them.

This is analytical routing, not physical KROW sharding: WAL, lock, primary key,
transactions and recovery remain unchanged. HASH helps only when a chunk's
values occupy fewer than all 64 buckets; a fully mixed bitmap correctly causes
no pruning, and HASH does not yet reorder blocks. RANGE chunk routing helps when
physical key order correlates with the partition field. Independently, local
RANGE clustering narrows block min/max even when values were mixed inside an
extent. It never moves a row across KROW extent boundaries, so retained-history
dirty tracking can rebuild one affected extent and reuse the others.

Bare `.partition()`, unknown strategies, multiple partition fields, non-integer
fields and temporal shorthands such as `"monthly"` are rejected. Temporal
partitioning waits for a canonical temporal vector/ordering contract; KitDB does
not guess from a field name or silently reinterpret timestamps. Omitting a
partition declaration remains valid and still uses internal chunking.

`ALTER TABLE ... SET PARTITION BY` publishes only a new Schema IR policy; it
does not scan or rewrite KROW in the DDL transaction. The schema hash and
catalog revision make every older analytics generation stale immediately, so
queries fail open to KROW until `RefreshAnalytics` publishes a complete new
generation. A changed policy deliberately prevents old-chunk reuse because its
routing summaries prove a different contract. `DROP PARTITIONING` removes only
that policy and likewise requires an explicit refresh; it never drops rows or
physical child partitions. Dropping the active partition field is refused.

Refresh currently remains explicit and all-or-nothing at generation
publication. There is no node-owned background partition build, durable build
cursor, global cross-extent cluster/sort, physical child partition, or
zero-pause migration claim yet.

RANGE v4 publication has focused cancellation and hard-process evidence. A
canceled append after new bytes exist does not advance the published generation;
an abrupt child-process exit at the same staging boundary reopens the previous
root, serves the newer canonical snapshot through KROW fallback, and permits a
subsequent successful refresh. These tests prove process-crash behavior on the
tested filesystem, not power-loss behavior below `fsync`.

The RANGE builder also has an executable memory-shape gate: at the 128-column
ceiling, source vectors for one 8,192-row extent, one 1,024-row output batch and
the permutation array account for 10,682,368 backing-array bytes, below 11 MiB.
It rejects a decoder capacity above one extent. This is a builder allocation
bound, not total process RSS; Go object headers, the kernel page cache, the
snapshot reader and concurrent queries are governed separately.

### Partition Routing Measurement

The checked-in `BenchmarkAnalyticsRangePartitionPruning` compares the same
warm KCOL aggregate with and without the RANGE chunk summary. Source rows are
checkpointed before timing so the result does not include cloning a large
uncheckpointed KROW overlay. On the current Windows/i7-11850H test host, with
131072 rows in 16 chunks and a predicate selecting only the last chunk (100
queries, three runs):

| Path | Latency/op | Heap/op | Allocs/op | Chunks rejected |
| --- | ---: | ---: | ---: | ---: |
| KCOL block statistics only | 0.599-0.879 ms | about 69 KiB | 266 | 0 |
| RANGE chunk routing + KCOL | 0.408-0.587 ms | about 72 KiB | 271 | 15 |

The median observed speedup was about 1.3x. Partition summaries are decoded as
inline manifest values, so opening a partitioned snapshot does not allocate one
heap object per chunk. This is a warm-cache engineering
measurement, not a cross-machine guarantee and not refresh/build throughput.
The remaining small allocation difference comes from selecting extent
descriptors. A mixed HASH bitmap or uncorrelated RANGE field may reject no
chunks and should be expected to perform like the block-statistics path rather
than this best case. Reproduce with:

```sh
go test ./kitdb/relational -run ^$ \
  -bench ^BenchmarkAnalyticsRangePartitionPruning$ -benchtime=100x -count=3
```

### RANGE Block Clustering Measurement

`BenchmarkAnalyticsRangeBlockClustering` begins with 131,072 rows whose eight
partition values are interleaved in every block. The unpartitioned projection
must decode every row. RANGE v4 groups those values inside each of 16 extents;
the same query rejects 114,688 rows and answers the remaining 16,384 from exact
block metadata. The table directory rejects 112 physical blocks before header
I/O, leaving one metadata-only header per extent. On the same host (30 queries,
three independently rebuilt fixtures, measured 2026-09-02):

| Layout | Latency/op | Heap/op | Allocs/op |
| --- | ---: | ---: | ---: |
| Mixed KCOL blocks | 9.01-14.51 ms | 69.4-69.6 KiB | 261 |
| RANGE v4 + block directory | 0.329-0.567 ms | 85.6-85.9 KiB | 265-266 |

The observed median speedup was about 16.3x. This is a deliberately favorable
filter distribution; the extra manifest buffer increases per-query allocation
slightly, and the production measurement below is the stronger bound.

```sh
go test ./kitdb/relational -run ^$ \
  -bench ^BenchmarkAnalyticsRangeBlockClustering$ -benchtime=30x -count=3
```

The opt-in `BenchmarkShoppingProductionAnalytics` also ran against a verified
30.05 GB copy containing 13,773,074 `shopping` rows. The fresh projection held
1,682 shopping chunks. Metadata-only `RANGE(id)` rejected 1,138 chunks for
`id >= 20000000000`, but latency remained variable at 0.267-0.403 seconds versus
0.355-0.422 seconds without partition routing because block min/max already
rejected the same 9.33 million rows. With the prior RANGE chunk layout v4 and
`RANGE(category)`, the
common-category aggregate fell from 0.501-0.881 seconds to 0.140-0.148 seconds.
Rows decoded fell from 11,133,202 to 3,121,152 and 322 chunks were rejected;
the result remained exactly 1,820,338 rows and the same integer sum/average.
The unrelated high-id workload did not regress in these runs. This evidence
sets the boundary clearly: local clustering is valuable when the declared key
matches the analytical filter, while one key cannot optimize every workload.
Reproduce only on a verified disposable copy:

```sh
KITDB_SHOPPING_BENCHMARK=/path/to/shopping-copy.kitdb \
go test ./kitdb/relational -run ^$ \
  -bench ^BenchmarkShoppingProductionAnalytics$ -benchtime=3x -count=3
```

## Execution and Bounds

- The public artifact is `.analytics`; `KCOL` is its internal blocked-column
  encoding. KCOL v3 keeps fixed-width integer, float and boolean vectors with
  explicit NULL flags and adds opt-in dictionary-encoded UTF-8 text. Each text
  block stores one strictly ordered dictionary plus nullable uint32 row codes;
  there is no process-global dictionary or canonical text copy outside the
  rebuildable sidecar. At most 128 fields, 1024 rows and 64 MiB of encoded
  payload are accepted per block. The writer preflights every dictionary before
  allocation and uses one reusable text arena for the whole block, so the bound
  cannot multiply by the number of declared text fields.
- KCOL payload format is v3. Unpartitioned and HASH analytics chunks use
  manifest layout v5. RANGE uses layout v6 to require deterministic ascending
  partition values with NULLs last inside each KROW extent. An older RANGE
  manifest becomes stale and is rebuilt from KROW rather than trusted as
  clustered data.
- Every KCOL v3 block header carries its exact row count, per-column payload
  length, NULL count, and numeric min/max plus signed 128-bit integer sum where
  applicable. Text currently exposes exact NULL statistics, not header min/max.
  The group header and each payload have CRC32C protection. Readers still accept
  numeric KCOL v1/v2, but relational projection reuse requires v3 and rebuilds
  older sidecars from KROW rather than mixing contracts.
- Partitioned tables may carry one checksummed block-directory byte buffer in
  the manifest. It is capped at 1 MiB across a generation and is all-or-none per
  table. Each entry includes the verified physical block length, so variable
  text dictionaries preserve exact range selection and pre-header partition
  pruning. If the cap is reached, KitDB omits that optional table directory and
  keeps chunk/header routing; canonical KROW and KCOL payloads are unchanged.
- One open relational Engine retains at most one verified container reader for
  `.analytics` and one for `.search`, and only when that container's complete
  serialized directory is at most 2 MiB. Every lease still checks the exact
  source cursor, catalog revision, schema hash and row generation/epoch. Larger
  directories remain usable but are opened and decoded per query rather than
  becoming unbounded tenant-resident memory. A projection publication takes the
  exclusive reader gate, drains leases, closes only that kind's cached handle,
  and then publishes; `Engine.Close` releases both handles. Warm lease lookup is
  allocation-free, while cold loading remains correctness-equivalent to the old
  per-query path.
- Ungrouped analytics follows one correctness-first ladder per block: reject it
  when all AND filters prove no row can match; answer from metadata when all
  rows provably match and every aggregate has an exact metadata representation;
  otherwise decode and scan the requested vectors. Metadata can answer COUNT,
  integer SUM/AVG, and integer/float MIN/MAX. Text COUNT can use exact NULL
  metadata when filters cover the block; text MIN/MAX decode the dictionary
  vector. Float SUM/AVG deliberately scan so the existing numeric semantics are
  preserved. Grouped analytics uses the same zone-map rejection but scans every
  block that may contribute a group.
- Both KROW-batch and KCOL-batch feed the same typed filter/aggregate loops.
- Ordinary physical-table SELECT uses a per-query projected KROW decoder. It
  validates every field frame and the complete row checksum while decoding only
  tags needed by projection, predicates, equality conditions, and ordering.
  Buffered ordering retains only output/order fields. CRUD rewrites continue to
  use the full decoder and preserve unknown tags, so this optimization changes
  neither canonical KROW nor WAL.
- RANGE build memory is bounded to one 8,192-row extent plus one output batch.
  At the 128-column ceiling, vector/order buffers stay near 11 MiB per active
  builder; rows are never accumulated for a global in-memory sort.
  KROW's TLV envelope and checksum are validated without creating a complete
  row map. Legacy rows use the existing compatibility decoder.
- Supported accelerated SQL: ungrouped `COUNT`, numeric `SUM`/`AVG`, numeric,
  boolean and declared-text `MIN`/`MAX`, AND-combined field/literal comparisons
  and IS NULL/IS NOT NULL. Grouped execution additionally supports one or more
  integer/boolean/declared-text group fields, including NULL and GROUP BY without
  an aggregate. Its HAVING (including
  bound parameters), projected ORDER BY, OFFSET and LIMIT reuse scalar SQL
  post-processing; LIMIT does not stop aggregation early.
- Existing primary/unique/secondary access paths take precedence over a full
  column scan. A complete non-partial ordered index additionally serves the
  narrow `leading GROUP BY fields + optional COUNT(*)` shape directly from key runs,
  including relational transaction-overlay mutations, without KROW or KCOL.
  A second covering-index path supports filtered or unfiltered `COUNT`, `SUM`,
  `AVG`, `MIN`, and `MAX`, plus grouped fields, when one complete non-partial
  key contains every predicate, group, and aggregate field. Equality and one
  contiguous range bound the scan; any covered residual expression is still
  evaluated from decoded key values. It includes transaction-overlay index
  mutations and performs no KROW point lookup. Non-covering text/decimal/float
  group shapes, joins, computed aggregate expressions, and ungrouped aggregate
  ORDER BY/HAVING stay on the scalar/index path. They are not silently dropped.
  Float values can be aggregated, but float grouping awaits a shared identity
  contract including signed zero. Decimal and temporal vectors remain separate
  future format decisions.
- Only requested column payloads are read during an ordinary scan; their
  checksums and statistics are checked before use. Metadata-only execution
  trusts the checksummed KCOL v3 header without reading the payload. Reuse and
  copy validation recompute statistics from every payload before publication.
  Corruption in an unrequested block is not a claim that the entire file has
  been verified. Sidecar verification is not part of the canonical `kitdb
  verify` command yet.
- The KCOL scan report audits successful block-header and selected-column
  payload `ReadAt` calls and bytes separately. `EXPLAIN ANALYZE` exposes these
  as `kcol_io`; `kcol_directory` separately reports blocks/rows rejected before
  header I/O. Metadata and rejected blocks therefore prove zero payload reads.
  `projection_cache` separately reports reader/directory hit, miss and bypass.
  KCOL byte counters exclude the projection container/KCOL schema open and do
  not claim physical-device reads below the operating-system page cache.
- Batch vector and selection buffers are bounded independently of table length.
  The snapshot directory is capped at 8 MiB/16384 entries, with at most 1 MiB
  of raw optional partition block-directory entries. Two experimental
  scans are admitted per Engine; further scans wait with context cancellation.
  Embedded callers still need a host governor and this is not a hard process
  RSS budget. The standalone PostgreSQL listener now adds a separate fair,
  bounded statement scheduler across databases before execution reaches the
  per-Engine projection gate.
- Group state grows with distinct groups, not all input rows. The configured
  result/group limit (default 10000, ceiling 100000) still applies before HAVING
  or LIMIT. Batch grouping also enforces a 16 MiB accounted state budget for
  keys, accumulators and projected values. This is a conservative accounting
  policy, not measured peak heap/RSS. There is no spill-to-disk implementation.
  Budget errors return no partial rows and are not retried as a KCOL failure.
  Existing-group lookup reuses a key buffer; new groups own copied keys/values.
- Search keeps the existing BM25, Vietnamese analyzer, identifier filtering,
  candidate/result budgets, snippets and cursor validation. A fresh replacement
  is packed without tombstones. Builder segments use a soft 8 MiB accounting
  threshold and at most 25000 documents, not an 8 MiB heap guarantee.
  Dictionaries and norms still use the existing search reader caches. The
  Engine-owned container cache shares one file handle and decoded manifest.
  `SearchReaderCacheBytes` may additionally retain one immutable packed reader
  per searched table, single-flighting concurrent first opens and sharing its
  sparse dictionaries and lazy norm vectors. Zero is the default. A snapshot
  whose conservative reservation does not fit bypasses residency and remains
  queryable through open/query/close. Native segment-count and input-size limits
  still apply.

### Multi-Database Residency and Mixed Workload

The standalone PostgreSQL node can name a bounded set of warm databases and
bound idle projection residency by database count, serialized snapshot
directory bytes, and total reader-capacity bytes. Reader capacity is the outer
directory plus the conservative packed-search reservation; actual deterministic
reader residency is reported separately. Neither number includes transient
query allocations, allocator overhead, file payload pages, or the operating
system page cache. Warm means an open relational owner plus eligible bounded
readers, not prefetched KCOL/search payloads. Non-warm readers are closed
oldest-first without deleting sidecars and reopen with the same exact watermark
checks. Whole-engine LRU also skips configured warm databases.
When projections are enabled, a configured warm database defaults to
`validate` admission on its first lazy open. This prevents a malformed sidecar
from becoming a protected long-lived resident. Set
`WarmProjectionOpenPolicy` (or `kitdbpg -warm-projection-open-policy`) explicitly
to choose another policy; `Relational.ProjectionOpenPolicy` remains the policy
for every database, warm or cold. `kitdbpg` exposes the per-database cache as
`-search-reader-cache-bytes` and the fleet ceiling as
`-max-idle-projection-reader-bytes`. Both are explicit resource policy, not a
correctness requirement.

The retained `BenchmarkPostgresNodeMixedWorkload` exercises the public pgwire
path over eight independent files: four stable databases alternate primary-key
reads, KCOL aggregates and BM25 search; four mutable databases alternate
primary-key reads and WAL-backed updates. Each fixture has 4,096 rows. The four
stable databases retain four independent packed readers (eight verified file
handles on Windows) under an explicit per-database reader budget. Query
admission is six listener-wide and two per database, with bounded queues. On
Windows/amd64, Go 1.26, an i7-11850H and warm operating-system cache, three
5,000-operation runs on 2026-09-05 observed:

```sh
go test ./kitdb/relational -run '^$' \
  -bench '^BenchmarkPostgresNodeMixedWorkload$' \
  -benchtime=5000x -count=3 -benchmem
```

| Measurement | Observed range |
| --- | ---: |
| Average | 124306-138124 ns/op |
| Throughput | 7238-8044 statements/s |
| Point read p95 / p99 | 3-4 ms / 4-5 ms |
| KCOL aggregate p95 / p99 | 4 ms / 4-5 ms |
| BM25 search p95 / p99 | 4 ms / 4-5 ms |
| KROW update p95 / p99 | 4 ms / 5-8 ms |
| Allocation | 90292-90380 B/op, 561-562 allocs/op |
| Admission peak / queue peak | 6 / 10 |
| Packed search readers / Windows handles | 4 / 8 |
| Conservative reader capacity | 9.962 MiB |

Setup, projection construction and connection establishment are excluded;
SQL planning, pgwire encoding/decoding, KROW/KCOL/search execution and commits
are included. Percentiles are bounded-histogram bucket upper bounds, include
admission wait, and consume constant benchmark memory. This is a repeatable
mixed-throughput baseline, not a cold-cache, 13-million-row, power-loss,
SQLite, or PostgreSQL comparison.

The deterministic node lifecycle test separately opens four independent
packed-search databases, protects one warm database, and permits only two idle
projection owners. It verifies that the warm reader and newest cold reader
survive while the two older cold readers are closed oldest-first. This proves
bounded ownership; it is not a latency benchmark.

### Projection Reader Cache Measurement

An isolated Windows/amd64 measurement on 2026-09-02 used a synthetic verified
container with about 1 MiB of serialized directory metadata. It measures only
acquiring/releasing the exact-watermark reader, not SQL planning, KCOL payload
reads or physical cold storage:

```sh
go test ./kitdb/relational -run '^$' \
  -bench '^BenchmarkProjectionReaderCache$' -benchtime=30x -count=3 -benchmem
```

| Path | Observed time/op | Bytes/op | Allocs/op |
| --- | ---: | ---: | ---: |
| Warm verified lease | 180.0-206.7 ns | 0 | 0 |
| Open + verify + decode | 12.11-13.57 ms | about 2.91 MiB | 52 |

The cold case ran against the operating-system cache and is not an SSD latency
claim. The result demonstrates removal of repeated file-open and JSON material-
ization work; it does not make the KCOL data scan itself constant-time.

A separate retained benchmark measures the packed BM25 reader through the
standalone relational API. The deterministic fixture has 4,096 rows; setup,
projection construction and the first query are excluded. Fifty queries per
sample, three samples, on Windows/amd64, Go 1.26 and the same i7-11850H observed:

```sh
go test ./kitdb/relational -run '^$' \
  -bench '^BenchmarkPackedSearchReaderCache$' -benchtime=50x -count=3 -benchmem
```

| Path | Observed time/op | Bytes/op | Allocs/op |
| --- | ---: | ---: | ---: |
| Open/query/close | 0.801-0.976 ms | about 372 KB | 981 |
| Resident reader | 0.253-0.404 ms | about 112 KB | 864-865 |

This shows that repeated reader construction was material for this fixture. It
is not a 13-million-row result, a cold-storage result, or an RSS guarantee. The
retained capacity intentionally includes all possible field-norm vectors and a
full bounded multi-field frequency cache; reported current residency can be
lower until queries populate those structures.

The opt-in 13,773,074-row shopping canary also compared both paths inside one
Engine for the documented `category = 4459` aggregate. Three small 3-iteration
samples observed 310-351 ms and 172466-172770 B/416-419 allocations with the
warm reader, versus 327-426 ms and 4354704-4365904 B/3884-3896 allocations when
every iteration forcibly closed and reopened the directory. The latency ranges
overlap because the query still reads 60,676,704 KCOL bytes and is sensitive to
the operating-system page cache. The repeatable evidence here is removal of
roughly 4.2 MiB and about 3,470 allocations of container metadata work per
query, not a claimed fixed latency speedup.

## Consistency and Failure Boundary

Refresh captures a consistent catalog/data snapshot and stamps each file with
the source database ID, transaction and checksum, catalog revision, table schema
hash and row generation/epoch. Chunk decoding/copying happens outside the source
writer gate; the retained-history proof currently invokes a kernel checkpoint.

Readers compare against their own transaction snapshot, not a racy latest
transaction number. An old transaction may correctly use an old projection.
Any newer commit, including a different table's write, invalidates the snapshot
for newer readers. No delta overlay/catch-up has been implemented for these files.

Missing, stale or corrupt columnar data causes a complete restart on canonical
KROW, after discarding partial aggregate state. A canceled query stays canceled.
Grouped and ungrouped reads share this path. Group maps, projected values and
accumulators are reset together; late-block corruption cannot double-count
already consumed groups. Consumer errors do not trigger a storage fallback.
Missing/stale search returns an explicit `RefreshProjections` error rather than
silently missing new hits, serving stale ranking or starting an unexpected full
rebuild. Search file corruption fails the read; rebuild explicitly.

Columnar append syncs new data/directory before updating the alternate root and
syncing again. Open chooses the latest valid root/directory, or the previous
valid one after a torn/corrupt publication. Exact source watermark comparison
still applies to that recovered root, so fallback must not return stale SQL
answers. Unpublished append tails are ignored, not truncated under old readers.

Search, initial columnar construction, upgrades and compaction publish a synced
same-directory temporary file by replacement. Readers are drained at publication,
not during the build. On Unix the parent directory is synced. Windows has no
portable directory fsync here: loss of a sidecar publication requires a rebuild,
not source database recovery. A failure after root writing or rename can leave
publication uncertain; reopen/verify, do not assume cancellation rolled it back.

The two files are NOT one atomic publication. A partial refresh may publish
columnar but leave older search. Independent exact watermarks make that state
safe to detect. Existing canonical backup/restore continues to back up KROW
truth, not these new files. After restore, rebuild. This experiment does not
implement portable folder backups or authorize copying an active database.

## Measurement

Measured 2026-08-31, Windows/amd64, Intel i7-11850H, Go test on the workspace
Go 1.26 toolchain, serial queries, warm cache, normal durability settings.
Deterministic synthetic fixture: 100000 rows, six fields, repeating text payload
of 288 bytes, price in 0..99, quarter-step rating, alternating boolean, and
periodic NULLs. The wide text payload is intentionally not requested by the
aggregate. This is NOT the migrated shopping dataset or a PostgreSQL comparison.

```sh
go test ./kitdb/relational -run '^$' \
  -bench '^BenchmarkProjectionAggregate$' -benchtime=1s -count=3
```

Same predicate and aggregate, with answers cross-checked against the scalar
path. The benchmark fixture and differential tests are retained in the repo.

| Path | ns/op range | B/op range | allocs/op |
| --- | ---: | ---: | ---: |
| KROW scalar | 395215633 - 448635033 | 375540461 - 375542397 | about 2532630 |
| KROW typed batch | 130845462 - 132644378 | 125039813 - 125040709 | about 502750 |
| KCOL typed batch | 3926008 - 4020755 | 66711 - 66748 | 270 |

`B/op` is cumulative Go allocation per query, NOT resident or peak RAM. The
KROW scan/cursor still allocates considerably. Batch processing is not a
zero-allocation claim. This table predates KCOL v2 block statistics. No
cold-cache, p99/concurrency, peak RSS, search-speed, 13-million-row or
production-durability result is claimed here.

### KCOL v2 Block-Statistics Measurement

Measured 2026-09-01 on the same machine and retained synthetic benchmark, with
three explicit iterations per path:

```sh
go test ./kitdb/relational -run '^$' \
  -bench '^BenchmarkProjectionAggregate$' -benchtime=3x -count=1
```

| Path | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| KROW scalar | 470527233 | 400577714 | 3142266 |
| KROW typed batch | 129564500 | 125045949 | 502763 |
| KCOL v2 typed batch | 3846333 | 88296 | 323 |

The benchmark query includes work that reads vector payloads; it is not an
all-metadata best case. Three iterations are regression evidence, not a latency
distribution.

### Migrated 13-Million-Row Observation

Measured 2026-09-01 against the retained migrated shopping artifact on the same
Windows machine. Canonical KROW was 30054427583 bytes and contained 13773074
shopping rows plus one migration row. The v1-to-v2 refresh rebuilt 1683 chunks
and produced a 2242325964-byte `.analytics` file. A second unchanged refresh
decoded zero KROW rows, reused all 13773075 source rows and 1683 chunks, and
reported zero written bytes with publication `unchanged`.

KCOL v2 execution counters, cross-checked against the existing scalar answers:

| Query shape | Scanned rows | Skipped rows | Metadata rows | Result note |
| --- | ---: | ---: | ---: | --- |
| `COUNT/MIN/MAX` over integer columns | 0 | 0 | 13773074 | all 13451 blocks answered from headers |
| `COUNT/SUM WHERE price < 0` | 0 | 13773074 | 0 | all 13451 blocks rejected by min/max |
| `official = true AND price >= 100000` aggregate | 13745426 | 27648 | 0 | 27 blocks rejected; result unchanged |

The observed warm end-to-end `go run` process times were about 0.68 seconds,
0.64 seconds and 1.01 seconds respectively. They include process startup,
database open, JSON encoding and cached Go command work; they are not engine-only
p50/p95 measurements. Legacy Schema IR v2 uses the old `integer` aggregate
result contract, so SUM/AVG on that artifact still scans. Current exact
SMALLINT/INTEGER/BIGINT schemas can use signed-128 metadata sums. No cold-cache,
concurrent-tenant, power-loss or peak-RSS claim is made by this observation.

An audited read observation on 2026-09-02 used the fresh RANGE(category)
projection at source transaction 33795 and this query:

```sql
SELECT SUM(price), AVG(stock), MAX(sold)
FROM shopping
WHERE category = 4459;
```

Both KROW and KCOL returned exactly `[206618737, 0, 2106]`. One observed KROW
batch execution scanned 13,773,074 logical rows and read 397,741 main pages
(26,648,913,678 bytes) in 58.0128091 seconds. The first instrumented KCOL
execution scanned 1,649,664 rows, skipped 12,123,410, and completed in
1.4622978 seconds. Its scan read 12,896 block headers (10,316,800 bytes) and
6,444 requested-column payloads (59,387,904 bytes), or 69,704,704 bytes total.
Three immediately repeated KCOL executions retained exactly the same I/O counts
and took 0.1314821-0.1433824 seconds.

On 2026-09-02 the new table-level block directory was added to that generation.
The one-time refresh decoded zero KROW rows, referenced 2,241,998,902 existing
KCOL bytes, wrote 925,682 bytes of generation/header/directory data, and
completed in 5.74 seconds. The canonical 30,054,427,583-byte KROW file was
unchanged. A second refresh verified all 1,683 chunks and reported publication
`unchanged` with zero bytes written.

The same query then rejected 11,285 blocks and 11,555,840 rows from the
directory before header I/O. It read 1,611 headers (1,288,800 bytes) instead of
12,896, while the 6,444 selected payload reads remained 59,387,904 bytes. Total
audited KCOL scan bytes fell from 69,704,704 to 60,676,704 and three separate
`EXPLAIN ANALYZE` processes reported 0.0945194-0.1320085 seconds. The exact
answer remained `[206618737, 0, 2106]`.

The post-directory KCOL data-path byte count is about 439 times smaller than the
observed KROW page count for this query. This is one application canary with one
KROW sample, not a cold-cache classification, latency distribution, hardware-
I/O trace, or universal speedup claim.

### Historical Chunk Copy Measurement (Before KSNAP002)

Same machine/date, a separate 32768-row synthetic fixture with the same fields
and wide payload. Each measured iteration commits a one-row price UPDATE and
then refreshes columnar. Setup and the first projection build are excluded.
The full case has no retained history and rebuilds from KROW; the chunk case
retains history and includes its proof/checkpoint overhead. Both publish a
complete new container with normal durability settings.

```sh
go test ./kitdb/relational -run '^$' \
  -bench '^BenchmarkColumnarIncrementalRefresh$' -benchtime=5x -count=3
```

| Refresh | ns/op range | B/op range | KROW rows/op | Reused rows/op |
| --- | ---: | ---: | ---: | ---: |
| Full source rebuild | 54175480 - 67650780 | 43195737 - 43195990 | 32768 | 0 |
| History + chunk reuse | 50161380 - 56736380 | 11192523 - 11193609 | 8192 | 24576 |

The chunk case copied 885312 old KCOL bytes per iteration. It decoded 75% fewer
source rows and allocated about 74% fewer cumulative Go bytes; timing ranges
overlap and the latency gain is modest at this size. These are five iterations
per run, not p95/p99 measurements. The benchmark does not establish a cold-cache,
13-million-row or disk-space/write-amplification improvement.

### Generational Append Measurement

The same synthetic 32768-row workload now uses `KSNAP002`. Eight iterations per
run capture a compaction as well as appends; setup/first build are excluded and
each measured iteration includes the source UPDATE and refresh. Retained-history
proof/checkpoint overhead is included in the chunk case. Per-operation write,
reference and copy counters are averages across the complete run, not just the
last or cheapest iteration.

```sh
go test ./kitdb/relational -run '^$' \
  -bench '^BenchmarkColumnarIncrementalRefresh$' -benchtime=8x -count=3
```

| Refresh | ns/op range | B/op range | Written bytes/op | KROW rows/op |
| --- | ---: | ---: | ---: | ---: |
| Full source rebuild | 43114875 - 58671150 | 43220017 - 43223065 | 1193801 | 32768 |
| Append + threshold compaction | 51999400 - 59575775 | 11230476 - 11231522 | 412016 | 8192 |

The chunk case compacted once per eight iterations, averaging 774648 bytes
referenced in place and 110664 old bytes copied per iteration. Application-level
file write bytes dropped about 65.5%, including compaction; this is not device
I/O accounting. Latency is not consistently better at this scale. The retained
history walk, validation reads and fsync still matter. These short warm runs do
not establish p99, cold-cache, crash-durability or 13-million-row performance.

A separate retained regression test verifies a single changed chunk writes
about 300333 bytes versus 1193801 bytes for initial construction, leaves old
payload bytes untouched, and verifies unchanged refresh writes exactly zero
bytes. It also checks repeated churn reaches compaction rather than unbounded
append under normal successful refreshes.

## Promotion Gates

Exact BIGINT SUM/AVG now shares an integer accumulator across scalar, KROW and
KCOL execution (also grouped). It uses int64 addition until widening is needed,
then arbitrary-width integer addition; results are NUMERIC text rather than
float64. AVG rounds to 16 fractional decimal digits, half away from zero.
Legacy INTEGER/FLOAT aggregate behavior is unchanged. Corrupted-sidecar fallback
must discard every partial exact accumulator before rescanning KROW, as tested
by `TestBigIntCorruptColumnarDiscardsPartialExactSums`.

The historical timings above predate this BIGINT contract. They do not measure
the new exact accumulator or a rewrite of an existing integer catalog.

Before default adoption: measure the real workload with update/delete churn,
cold cache, p95/p99 under multiple tenants and peak RSS; add per-table/delta
freshness, wider crash/orphan recovery, resource
governor, sidecar verification/backup policy, and format upgrade policy. Float
grouping, spilling and further compression need separate correctness/benchmark gates. A full-file rebuild
after every write would defeat the purpose of this snapshot experiment.

The 2026-09-04 retained shopping run measured 13,773,074 wide rows. A buffered
10,000-row CTE peaked at 4.59 MiB under the 32 MiB gate after projected retention.
The former 214.7-second scalar text grouping outlier now uses the ordered
merchant index in 2.52-2.60 seconds, reads no KROW, and allocates about 123 KiB
across a 13-segment source. The evidence still rejects spill as the next
optimizer. A Lazada equality/range covering aggregate scanned 109,430 index
entries in 90.4-120.1 ms depending on aggregate shape, with zero KROW rows or
point lookups. Dictionary text KCOL now serves explicitly analytical text fields
when no covering ordered index can answer the complete query. On the deterministic
100,000-row text-group fixture, two local three-iteration runs ranged from
0.607-1.834 s/op for scalar KROW, 137-329 ms/op for KROW batch, and
48.6-99.4 ms/op for KCOL dictionary. Reported allocations stayed near 380 MiB,
90 MiB and 8.4 MiB per operation respectively. These are warm-cache Windows
measurements with visible host variance, not a production latency promise.

The retained 13,773,074-row shopping copy now also has explicit dictionary
analytics for `merchant`. A `GROUP BY merchant` plus non-covering `SUM(price)`
returned the exact PostgreSQL aggregate values. Its warm KCOL sample was
0.633 s/op and read 191.7 MB, versus 53.3 s/op and 26.65 GB of page reads for
the KROW batch benchmark. The text projection added about 54.0 MiB to the
2.24 GB numeric/boolean sidecar. The PostgreSQL baseline ran on a different
remote host and is not a same-hardware comparison.
See
[`SHOPPING_13M_MEMORY_2026-09-04.md`](../../benchmarks/dbcompare/SHOPPING_13M_MEMORY_2026-09-04.md).
See also
[`TEXT_ANALYTICS_13M_2026-09-04.md`](../../benchmarks/dbcompare/TEXT_ANALYTICS_13M_2026-09-04.md).
The bounded pgwire fleet follow-up, including warm projection RSS and
noisy-neighbor tails, is recorded in
[`SHOPPING_13M_FLEET_2026-09-04.md`](../../benchmarks/dbcompare/SHOPPING_13M_FLEET_2026-09-04.md).

The [grouped batch follow-up](../../benchmarks/dbcompare/GROUP_BATCH_2026-08-31.md)
records the supported GROUP BY workload separately from the historical ungrouped
measurements above.
