# Kitwork Search experiment

`search` is an experimental, embedded, pure-Go full-text search package. It is
inside the engine repository while the storage and query model are measured,
but it does not import any Kitwork runtime, VM, database, or tenant package.
That one-way boundary keeps later extraction into a standalone module mechanical.

## Current milestone

Segment V2 provides:

- an immutable packed segment file;
- a schema fingerprint tied to field order, analyzer identity, and boost;
- standard and Vietnamese diacritic-folding analyzers;
- a sparse, prefix-compressed term dictionary;
- independent posting blocks of at most 128 documents with exact `maxTF` and
  minimum field-norm bounds;
- independently protected posting headers and payloads plus section CRC32C;
- lazy `ReadAt` access to dictionary blocks, postings, and stored identifiers;
- a per-posting-iterator 4 KiB read-ahead window that coalesces adjacent block
  headers/payloads without creating a shared unbounded cache, with context
  checks immediately before and after physical reads;
- two query-local 4 KiB read-ahead windows for monotonically accessed stored
  identifier offsets and bytes, reused across immutable segments;
- exact implicit-AND BM25 search seeded from the rarest term;
- exact disjunctive OR search through `MatchQuery.Operator == QueryAny` across
  the selected field or fields, using bounded WAND pruning once Top-K is full;
- exact prefix search through `MatchQuery.Prefix`, expanded into matching
  analyzed terms on the selected field or fields;
- exact phrase search through `MatchQuery.Phrase` on one field, using stored
  posting positions for adjacency checks;
- exact cross-field AND through `MatchQuery.Fields`: each term may occur in a
  different selected field while BM25 length normalization and boost remain
  field-specific;
- conjunctive Block-Max pruning with local and index-wide Top-K thresholds;
- segment-level MaxScore pruning, so a populated global heap can reject a
  complete later segment before loading its norms or posting iterators;
- bounded multi-field union-frequency caching plus safe MaxScore early exit;
- bounded Top-K collection without sorting every match; exact score and cursor
  thresholds reject losing candidates before an optional identifier-prefix
  read because that filter cannot improve rank;
- presentation-neutral highlighting that preserves source spelling and returns
  bounded byte ranges instead of injecting HTML;
- context cancellation, full verification, fuzz seeds, and concurrent-reader tests.

The index layer now adds:

- an `IndexWriter` that flushes one bounded builder into multiple immutable
  segments instead of retaining a complete tenant index in memory;
- immutable, checksummed manifest generations published only after every new
  segment is synced and visible;
- ordered crash-safe publication: immutable artifacts are synced before their
  manifest, and manifest directory-sync failures cannot trigger cleanup of an
  already-visible generation;
- one OS-level writer lock per index directory, released by the operating
  system when a process exits or crashes while readers remain concurrent;
- snapshot isolation: an open `Index` never changes when a newer generation is
  committed;
- exact index-wide document frequency and average field length for BM25, so
  ranking remains comparable across differently sized segments;
- bounded sequential fan-out and one global Top-K heap across at most 256
  segments;
- reopen, subprocess-crash, corruption, orphan-file, scoring-equivalence, fuzz,
  and concurrent search coverage;
- Manifest V2 with backwards-compatible Manifest V1 reads;
- a sorted, fixed-width `.ki` identifier sidecar for collision-verified
  `O(log n)` update/delete lookup without affecting the query path;
- immutable `.kd` tombstones with exact live document counts, token totals,
  document frequencies, and BM25 scores;
- atomic `Update` and `Delete` publication while old readers retain their
  previous snapshot;
- transactional `ReplaceAll` publication for disposable projections and full
  reindexing: a failed or canceled build removes its uncommitted artifacts,
  while an empty replacement deliberately publishes an empty generation;
- a streaming `BeginReplacement` transaction whose `Add`, `Flush`, `Commit`,
  and idempotent `Abort` methods keep only one bounded segment builder in RAM;
- exact cross-segment duplicate-ID validation through a buffered k-way merge of
  sorted `.ki` sidecars, using memory proportional to segment count rather than
  document count;
- bounded, size-tiered compaction that streams immutable posting blocks,
  remaps live documents, and does not require original source text or analyzer
  replay;
- generation-aware garbage collection whose default mode retains every
  committed generation and only removes unreachable artifacts.

The package uses only the Go standard library.

## Multi-tenant manager

`Manager` is the process-local owner for bounded multi-tenant operation. Tenant
keys are SHA-256-addressed below one root directory, so caller-controlled keys
cannot become filesystem paths. Each open tenant owns exactly one writer actor,
one bounded mutation queue, and immutable leased reader generations.

Search admission is bounded separately from execution. The default global
execution limit is `max(4, GOMAXPROCS)`, while each tenant defaults to at most
eight active searches. Waiting calls occupy fixed process and per-tenant queues;
when either queue is full, `Search` returns `ErrSearchOverloaded` immediately
instead of retaining another goroutine until capacity becomes available.

```go
manager, err := search.NewManager(".data/search", search.ManagerOptions{
    MaxOpenIndexes: 64,
    MaxConcurrentSearches: 16, // Tune with the concurrent-load harness.
    MaxConcurrentSearchesPerIndex: 8,
    MaxQueuedSearches: 4096,
    MaxQueuedSearchesPerIndex: 256,
    MaxConcurrentMutationBatches: 8,
    MaxConcurrentReplacements: 1,
    MaxConcurrentMaintenance: 2,
    MutationQueueSize: 1024,
    MutationBatchSize: 128,
    MutationBatchDelay: 5 * time.Millisecond,
    MaxPendingMutationBytes: 256 << 20,
    MaxPendingMutationBytesPerIndex: 64 << 20,
    CommitTimeout: 30 * time.Second,
    ReplacementTimeout: 2 * time.Hour,
    MaintenanceTimeout: 10 * time.Minute,
})
if err != nil {
    return err
}
defer manager.Close()

if _, err := manager.Add(ctx, tenantID, schema, document); err != nil {
    return err
}
hits, err := manager.Search(ctx, tenantID, schema, query, options)
```

Large database projections use the manager-owned streaming transaction rather
than constructing a complete `[]Document` in memory:

```go
replacement, err := manager.BeginReplacement(ctx, tenantID, schema)
if err != nil {
    return err
}
defer replacement.Abort()
for rows.Next() {
    document, err := documentFromRow(rows)
    if err != nil {
        return err
    }
    if err := replacement.Add(ctx, document); err != nil {
        return err
    }
}
if err := rows.Err(); err != nil {
    return err
}
if _, err := replacement.Commit(ctx); err != nil {
    return err
}
```

The `BeginReplacement` context owns the complete session, not only admission.
Cancellation, timeout, `CloseIndex`, or `Manager.Close` aborts uncommitted
artifacts. Searches continue on the old immutable snapshot until commit. New
`Add`, `Update`, `Delete`, and `Maintain` calls for that tenant return
`ErrReplacementActive` while the stream owns its writer.

Mutation context controls admission to the bounded queue. Once the actor has
accepted a mutation, the call waits for its batch manifest to commit even if
the caller context is canceled later. This avoids an ambiguous "canceled but
possibly durable" result. The configured commit timeout spans the complete
actor batch, including analysis, update/delete lookup, flush, and publication.
`Manager.Close` stops admission, cancels searches and maintenance, drains every
accepted ordinary mutation, and aborts any uncommitted streaming replacement
before closing writers.

`CloseIndex` is serialized with manager operations for the same key. It first
stops new leases, waits for already-admitted `Search`, `Add`, `Update`,
`Delete`, and `Maintain` calls to finish, and then closes the writer. A caller
that reaches the same key during this drain waits for it to finish and reopens
the durable generation instead of borrowing the canceled owner. This makes a
proven-idle eviction safe even when the tenant becomes active again at the
lifecycle boundary.

If a manifest is already visible but its final directory sync fails, low-level
`Commit`/`Compact` return the adopted `IndexInfo` together with
`ErrDurabilityUncertain`. Do not blindly retry that mutation. `Manager` wraps
the condition with `ErrIndexUnavailable`, reports the published generation,
and quarantines that tenant writer until it is deliberately reopened and
verified.

The manager clones each accepted document before enqueueing it, so the actor
never retains a caller-owned field map. Count limits and conservative payload
byte budgets bound pending queue plus in-flight batch memory both per tenant
and process-wide. Search takes a tenant slot before a global slot, preventing a
noisy tenant from reserving global capacity while waiting on its own limit.
Mutation batches and compact/GC passes have separate process-wide concurrency
limits to prevent CPU or disk storms across otherwise-isolated tenants.
Streaming replacements have another process-wide limit, defaulting to one,
because a full rebuild is sustained CPU and disk work. Their admitted document
payloads share the mutation byte budgets, while each active builder remains
bounded separately by `Writer.Segment` thresholds. `Stats` separates mutation,
replacement, and total pending write bytes.

`Stats` and `IndexStats` split searches into waiting and active counts, retain
maximum concurrency, and report started, completed, succeeded, failed,
canceled, and overloaded totals. Total, queue, and execution latency use fixed
14-bucket histograms with no query, tenant, route, or identifier labels. These
signals are bounded and safe for a long-lived process; bucket values are upper
bounds rather than exact percentile samples.

The default manager automatically asks the tiered compactor to enforce its
configured policy after committed batches. Automatic garbage collection stays
off by default. Enable `AutoGarbageCollect` only when this Manager owns every
reader of its root, including readers in other processes. Snapshot reference
counts then provide the exact oldest retained generation to GC.

`CloseIndex` explicitly drains and evicts an idle tenant. The first manager
version does not guess idleness or run an LRU eviction policy. Runtime stats
also expose commit, compaction, generation, tombstone, pending-byte,
retired-reader, and maintenance-failure state without opening files.

Kitwork hosts may call `Engine.SetSearchManagerOptions` during boot to replace
the defaults before schedulers, prewarm, or request traffic load any owner.
`Engine.SearchStats` exposes the same bounded, label-free process counters.
Reconfiguration is rejected after the first app or site loads.
Actual site eviction and removal feed the generation-inherited index keys to
`CloseIndex`, releasing writer goroutines and file handles for other tenants.
Hot reload only transfers those keys and never closes the shared index.

## Kitwork database adapter

`db.<table>.search()` now uses this package rather than the legacy SQLite blob
index. `core.Engine` owns one process-wide `Manager`, injects it into every app
and site facade, and closes it only after requests, tenants, and app runtimes
drain. Hot reload therefore cannot multiply managers or reset process-wide CPU,
queue, and replacement limits. Standalone `work.NewTenant` tests lazily own one
local fallback manager and close it with that tenant.

The SQLite table remains the source of truth. The adapter installs three
small database triggers per searchable table and increments a durable revision
inside the same insert/update/delete transaction. Freshness checks are `O(1)`;
they do not scan `count(*)` or total text length on every query. A stale
projection streams `database/sql` rows directly into `Manager.BeginReplacement`
and publishes one immutable generation, so ten million source rows are never
materialized as one Go slice. A source revision that changes during the stream
is deliberately not marked fresh and is rebuilt by a later request.

Only a compact revision signature is stored beside the tenant data. Search
segments live below the host manager root under SHA-256-derived index paths.
Result identifiers are hydrated in one bounded `IN` query, reordered by rank,
and highlighted through `Highlight`; the Kitwork adapter HTML-escapes source
text before adding `<b>` presentation markup.

KitDB uses the same public `.searchable()` and `.search()` vocabulary, but reads
the kernel directly rather than routing a bulk rebuild through SQL-light. The
standalone relational engine owns a durable source watermark for each searchable
struct. A matching watermark reopens the existing immutable projection after
restart. When retained history covers later source transactions, the owner
replays complete row mutations and advances the watermark only after the search
commit succeeds. A missing or incompatible projection is rebuilt by streaming
one fixed row snapshot; source rows are never retained as one tenant-sized Go
slice. The projection is derived data and the KitDB rowstore remains the source
of truth.

The Kitwork fluent adapter still has its own correctness-first freshness path:
it binds a replacement to the database transaction and catalog revision, then
hydrates ranked identifiers through one KitDB snapshot. Search is refused
inside an explicit record transaction on both paths because source/projection
snapshot semantics have not been defined for that boundary.

KitDB SQL-light and the local PostgreSQL protocol share one ranked-search plan:

```sql
SELECT id, name, _score, _snippet
FROM products
WHERE (name, brand, description) SEARCH $1
  AND stock > 0
ORDER BY _score DESC
LIMIT 20;
```

`field SEARCH text` selects one searchable field, `(field, ...) SEARCH text`
selects an explicit set, and `* SEARCH text` uses every `.searchable()` field.
The shorter `SEARCH text` remains a compatibility alias for the all-field form.
`LIKE` keeps its ordinary row-pattern semantics. SQL V1 accepts one SEARCH
predicate connected to residual row filters only by `AND`; ranking remains
`_score DESC`. `_score`, `_snippet`, and `_cursor` are virtual result columns.
`LIMIT n AFTER cursor` resumes strictly after the prior score/ordinal boundary;
the checksummed cursor is rejected after a source transaction, projection
generation, schema, query, or residual-predicate change.

Residual filters consume ranked hits in bounded pages until the requested page
is complete or the configured candidate budget is exhausted. The standalone
defaults are 10,000 returned search rows and 50,000 inspected candidates, with
hard ceilings of 100,000 and 1,000,000 respectively. Exhaustion fails explicitly
instead of silently returning an incomplete page. JOIN, GROUP BY, DISTINCT,
aggregate projections, arbitrary ordering, and SEARCH inside an explicit
transaction are not yet part of this search profile.

## Kitwork collection canary

`collection.search()` can shadow the segment engine without changing its
public API or serving result. The standard Kitwork host enables it explicitly
in the executable manifest:

```javascript
import { app } from "kitwork";

app
  .search({ collectionCanary: true })
  .web({ port: 8080 });
```

Custom Go hosts can call `Engine.SetCollectionSearchCanary(true)` before
schedulers, prewarm, or request traffic load any app or site.

The existing SQLite blob index remains authoritative for every response. After
that search completes, the generation-owned collection manager attempts a
non-blocking enqueue into one 32-entry queue. One worker per active generation
checks that the collection source signature is still current, streams Markdown
documents one at a time through `Manager.BeginReplacement`, and compares only
the ordered Top-200 identifiers. Collection query text is already limited to
4 KiB, so the shadow queue cannot retain tenant-sized inputs. Shared host
admission caps accepted active plus queued shadow tasks at 256 across all
generations. A full local queue or host budget drops the comparison instead of
delaying the request.

Every projection borrows the host-owned `search.Manager`; it never opens a
site-local writer. Immutable segments survive process restart below the host
search root, while a small generation-qualified signature below the site marks
which source snapshot they represent. Hot reload inherits the hashed index key
and reuses the durable projection. Generation retirement cancels and drains its
worker; proven site idle/removal closes the shared-manager index.

`Engine.CollectionSearchCanaryStats()` returns host-wide, fixed-cardinality
`Rebuilds`, `Searches`, `Matches`, `Mismatches`, `Failures`, `Dropped`,
`Canceled`, and `Stale` counters. They contain no query, path, tenant, or
document labels and survive generation replacement. `PendingTasks` and
`PeakPendingTasks` expose current and high-water host admission pressure. Pair them with
`Engine.SearchStats()` to observe manager admission, latency histograms,
commits, pending bytes, and open-index pressure. Promotion to a serving backend
must be a separate boot-only decision after representative canaries show stable
ranking, failure, overload, rebuild, and resource behavior.

## Example

```go
schema, err := search.NewSchema(
    search.Text("title", search.VietnameseAnalyzer(), search.Boost(3)),
    search.Text("body", search.VietnameseAnalyzer()),
)
if err != nil {
    return err
}

writer, err := search.NewIndexWriter("products.search", schema, search.WriterOptions{
    Segment: search.BuildOptions{FlushThresholdBytes: 64 << 20},
})
if err != nil {
    return err
}
defer writer.Close()

if err := writer.Add(ctx, search.Document{
    ID: "product-1",
    Fields: map[string]string{
        "title": "Ao thun cotton nam",
        "body":  "Cotton mem cho mua he",
    },
}); err != nil {
    return err
}
if _, err := writer.Commit(ctx); err != nil {
    return err
}

// Rebuild a disposable projection without exposing a partial generation.
if _, err := writer.ReplaceAll(ctx, []search.Document{
    {
        ID: "product-1",
        Fields: map[string]string{
            "title": "Ao thun cotton nam",
            "body":  "Cotton mem cho mua he",
        },
    },
    {
        ID: "product-2",
        Fields: map[string]string{
            "title": "Ao khoac chong nang",
            "body":  "Bo suu tap moi",
        },
    },
}); err != nil {
    return err
}

// For a large source, stream rows instead of materializing []search.Document.
replacement, err := writer.BeginReplacement()
if err != nil {
    return err
}
defer replacement.Abort()
for rows.Next() {
    document, err := documentFromRow(rows)
    if err != nil {
        return err
    }
    if err := replacement.Add(ctx, document); err != nil {
        return err
    }
}
if _, err := replacement.Commit(ctx); err != nil {
    return err
}

if err := writer.Update(ctx, search.Document{
    ID: "product-1",
    Fields: map[string]string{
        "title": "Ao thun cotton premium",
        "body":  "Cotton mem, form moi",
    },
}); err != nil {
    return err
}
if _, err := writer.Commit(ctx); err != nil {
    return err
}

// One bounded merge. Zero options use an 8-segment/1M-document policy.
if _, _, err := writer.Compact(ctx, search.CompactOptions{}); err != nil {
    return err
}

// Advance this only after the runtime has drained older readers.
if _, err := writer.GarbageCollect(ctx, search.GarbageCollectOptions{
    OldestRetainedGeneration: drainedGeneration,
}); err != nil {
    return err
}

index, err := search.OpenIndex("products.search", schema)
if err != nil {
    return err
}
defer index.Close()

hits, err := index.Search(ctx, search.MatchQuery{
    Fields: []string{"title", "body"},
    Text:   "ao cotton",
}, search.SearchOptions{Limit: 20})

fragment, err := search.Highlight(
    ctx,
    search.VietnameseAnalyzer(),
    sourceText,
    "ao cotton",
    search.FragmentOptions{MaxTokens: 18, ContextTokens: 4},
)
```

## Deliberate limits

The standalone package and file format remain experimental. Kitwork's schema
database search uses it through the host-owned adapter above, but it is not yet
the production `collection.search()` backend. That capability still serves its
legacy SQLite projection and uses the segment engine only through the opt-in,
bounded asynchronous canary described above.

- `FlushThresholdBytes` is conservative accounting that tells a caller when to
  flush; it is not a hard Go heap limit.
- `Add` is the high-throughput bulk/append API and rejects duplicate IDs across
  its complete uncommitted batch. It can still append an ID that already exists
  in a committed generation. `Update` resolves and tombstones every live
  committed occurrence of an ID. Applications that require insert-or-replace
  semantics must choose that policy above this low-level API.
- `ReplaceAll` remains the document-slice convenience API. Large sources should
  use `BeginReplacement`, feed rows one at a time, and `Commit`; a deferred
  `Abort` is safe after either commit or failure.
- Replacement commit performs one sequential buffered merge over every pending
  identifier sidecar to detect exact duplicate IDs across segments. This adds
  linear read I/O at publication time but avoids a document-count-sized ID map.
  At most 256 sidecar cursors exist because the manifest segment cap is 256.
- A manager-owned replacement accepts at most `MutationQueueSize` document
  producers and applies the same per-tenant and process byte budgets as normal
  writes. The segment builder itself is additional bounded memory controlled by
  `Writer.Segment`; process-wide builder pressure is capped by
  `MaxConcurrentReplacements`.
- `IndexWriter` owns `.kitwork-search.writer.lock` for its lifetime. A second
  writer for that directory receives `ErrWriterLocked`; readers remain
  concurrent and immutable. The lock file is intentionally persistent and must
  not be deleted to unlock a live writer because the OS handle, not file
  existence, owns the lock.
- `Manager` enforces one writer actor per tenant in-process, while each tenant's
  `IndexWriter` also excludes writers in other processes. On network or unusual
  filesystems, this guarantee is only as strong as that filesystem's OS locking,
  atomic rename, and sync semantics.
- `Manager` has a bounded open-index count and deliberately has no implicit LRU.
  A host serving more simultaneously warm indexes than `MaxOpenIndexes` must
  call `CloseIndex` from a proven idle lifecycle or configure a measured higher
  bound; it must not make the writer/file-handle set unbounded. Same-key
  arrivals during `CloseIndex` wait and reopen after the old owner drains.
- An `Analyzer` is trusted Go code running in-process. It receives the operation
  context and must check it during bounded work; Go cannot safely preempt an
  arbitrary custom callback that ignores cancellation.
- Garbage collection never guesses reader lifetime. With a zero drain boundary
  it keeps all valid manifests. A non-zero `OldestRetainedGeneration` is safe
  only after the owner has drained every older reader, including readers in
  other processes.
- The collection canary deliberately does not garbage-collect old segment
  generations because a replacement Kitwork generation may still be draining.
  Its small source-signature files are pruned, but production promotion requires
  moving ownership to a site/app-lifetime manager that can prove reader drain
  before reclaiming index artifacts.
- A snapshot is capped at 256 segments because the current reader owns one file
  handle per segment. Writers must merge before reaching that bound.
- One compaction defaults to at most eight segments and one million physical
  documents. Raising `MaximumInputDocuments` raises remap and identifier-index
  build memory proportionally; the default is deliberately conservative.
- Single-field exact live document frequency currently intersects a term
  posting list with a segment's tombstones on each query. Multi-field union
  frequency is exact and cached per immutable snapshot in a fixed 4,096-entry
  cache; a cold term still pays one union scan. Compact deletion-heavy segments
  to keep uncached work bounded.
- `Field` selects the optimized one-field Block-Max path. `Fields` requires
  every query term across the union of selected fields and keeps per-field
  norms and boosts. Phrase is available through `MatchQuery.Phrase`; fuzzy,
  range, and facets remain future milestones.
- Posting read-ahead is transient query memory, not reader residency. One
  iterator owns at most one 4 KiB window; the hard 32-field and 32-term limits
  therefore cap these windows at 4 MiB per active segment query before posting
  payload buffers. Cancellation cannot preempt an operating-system `ReadAt`
  already in progress, but is returned before decoding or publishing that
  completed payload.
- Identifier-prefix filtering owns two additional query-local 4 KiB windows.
  They are invalidated and reused between segments, never retained by the
  immutable reader, and obey the same before/after-I/O cancellation boundary.
- Search admission bounds goroutines owned by the manager, but the surrounding
  HTTP server must still set request deadlines, connection limits, and rate
  limits. `ErrSearchOverloaded` is a retry/backpressure signal, not permission
  to retry immediately in a tight loop.
- Norms use exact 32-bit token counts and are loaded per field on first use.
  A later format can use compact norms or platform-specific mapped access.
- Only external document identifiers are stored. General stored fields and fast
  columns are not part of Segment V2.

The database schema path now exercises the engine in production-style
ownership. Existing collection search remains on its prior backend while the
opt-in canary accumulates parity and failure data under real workloads.

## Verify

From the engine module:

```text
go test ./search
go test -race ./search
go vet ./search
go test ./search -run '^$' -bench BenchmarkSegmentSearch -benchmem
go test ./search -run '^$' -bench BenchmarkIndexMultiFieldSearch -benchmem
go test ./search -run '^$' -bench BenchmarkBlockMaxConjunctive -benchmem
go test ./search -run '^$' -fuzz FuzzParseSegmentHeader -fuzztime=10s
go test ./search -run '^$' -fuzz FuzzDecodeDictionaryBlock -fuzztime=10s
go test ./search -run '^$' -fuzz FuzzParseManifest -fuzztime=10s
go test -tags scale ./search -run '^TestSegmentScale$' -v -timeout 30m -args -kitwork-segment-sizes=1000000
go test -tags scale ./search -run '^TestIndexScale$' -v -timeout 30m -args -kitwork-segment-sizes=1000000 -kitwork-index-segment-documents=100000
go test -tags scale ./search -run '^TestManagedReplacementScale$' -v -timeout 30m -args -kitwork-segment-sizes=1000000 -kitwork-index-segment-documents=10000
go test -tags scale ./search -run '^TestManagerConcurrentLoad$' -v -timeout 15m -args -kitwork-manager-load-tenants=8 -kitwork-manager-load-documents-per-tenant=12500 '-kitwork-manager-load-concurrency=100,1000' -kitwork-manager-load-search-slots=0 -kitwork-manager-load-duration=3s -kitwork-manager-load-writes-per-second=20
```

The on-disk layout is documented in [FORMAT.md](FORMAT.md). Reproducible scale
results and their exact commands are recorded in [BENCHMARKS.md](BENCHMARKS.md).
