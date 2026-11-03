# Shared SELECT preparation, 2026-10-25

## Decision

Retain the narrowly scoped native autocommit primary-read optimization after
removing the speculative fallback tax found in
[the first experiment](PRIMARY_SELECT_2026-10-25.md).

Both batches preserved the isolated primary benefit. Non-primary equality
fallback now uses 429 allocations per operation instead of the original
snapshot executor's 430, rather than the rejected prototype's 485. Mixed
latencies remain variable; neither batch is discarded or used to claim a
general SQL speedup, statistical significance, a p99 SLA, or stable readiness.

## Implementation Boundary

- `captureTransactionLocked` factors the existing transaction capture loop.
  `BeginTransaction` retains its original admission and lifetime-lock rules.
  An autocommit caller can transfer its already-owned reservation and catalog
  into the same bounded capture loop rather than reserve/read both twice.
- `rowSelectBinding` contains only query-local schema, coerced conditions and
  residual predicate. `executeSelectWithBinding` reuses the ordinary executor;
  it is not a second row/index query implementation or a shared plan cache.
- The narrow autocommit entry prepares once. A single primary-field match may
  read row-generation metadata and caller-owned row bytes through `DB.Get`.
  A final monotonic transaction fence must equal the prepared catalog's
  transaction. Failure discards the direct result and takes the snapshot path.
- A non-primary equality skips direct I/O and transfers its reservation,
  catalog and binding straight to snapshot execution. Only an unchanged
  catalog revision permits binding reuse. A changed revision closes the
  mismatched capture and retries/rebinds through the existing bounded loop.
- The physical row generation is never reused from an earlier attempt: the
  fallback reads it from its own fixed snapshot, including after layout-only
  changes that do not change the logical schema.
- The caller's engine lifetime read lock is either released locally or
  transferred once to the returned transaction. Every error/return releases
  reservation ownership. No normal callback is invoked while a DB lock is held.
- Errors in speculative preparation/read use the snapshot executor for normal
  diagnostics. Cancellation and the existing count/FIFO/memory admission still
  apply, including when the direct read would not allocate a snapshot.

Eligibility remains one table, one non-null equality on a single primary key,
and ordinary columns. Joins, aggregates, expressions, aliases of the source
table, qualifiers, ordering/pagination, DISTINCT, CTEs, derived tables, search,
compound plans and EXPLAIN keep their existing path. Non-primary equalities
which match the simple syntactic shape use the prepared snapshot fallback.
Aliases of projected columns are supported.

Explicit transactions, role-authorized reads and streaming were not moved to
the direct path. Authorization still reads grants/schema/data in the same
existing transaction. These native `Execute` / `ExecutePlan` measurements do
not establish faster pgwire, KitSQL or role-policy execution. `DB.Get` still
holds its read lock over main-file I/O; the optimization is not lock-free.

No kernel API, format, WAL/Sync behavior, cache budget, worker count, handle
pool, generation refcount, global mutable state or shared schema cache changed.

## Evidence And Method

Evidence directory: `.artifacts/kitdb-prepared-select-20261025/`.

Before is the restored ordinary snapshot executor from the previously frozen
`kitdb-lock-holder-20261025/source` corpus, whose 12,006 files were hash-checked
before building. Both sides include the retained portable snapshot-reader
tests, existing QPC profile-clock correction and a common microbenchmark.
After adds the capture/binding changes, autocommit helper/hook and regression
tests. Eight source files are captured with hashes; both normal binaries are
hashed before timing. This is a scoped candidate, not shared HEAD.

Windows/amd64, Go 1.26.0, GOMAXPROCS=4, GOGC=100, no Go memory limit. Timed
workloads use no lock recording or runtime trace. Each side starts in a fresh
process with disposable databases; source/build/setup/validation/report output
are outside timings. Side order alternates, round zero is a warmup excluded
from comparisons, and no measured timing pair is deleted or rerun.

The first batch contains 34 fixed-work process reports: eight measured mixed
pairs, three read pairs and three write pairs, plus one warmup pair per mode.
Mixed work has 16 databases, 1,024 ACKs, 3,072 primary reads, 3,072 index reads
and 256 checkpoints per process. Cache remains 256 KiB per DB / 4 MiB total,
with two maintenance workers. Read-only and write-only retain their respective
fixed work. All reports verify 16 reopens and manager drain without remaining
leases, queue/running maintenance, open/warm/idle handles, cache reservations,
or recorded open/maintenance/close failure.

Its separate microbenchmark uses 128 checkpointed rows, 1,000 calls per case,
five measured alternating pairs and one warmup pair. Parsing and result
materialization are timed; setup is not. Primary repeatedly visits eight keys;
secondary fallback returns 16 rows; ordered-secondary adds `ORDER BY id LIMIT
16` and bypasses fast-path eligibility. There is no concurrent writer in these
microbenchmarks.

## Initial Batch

All changes are medians of paired percentages, **not** ratios of side medians.
Negative means less time or allocation.

| Metric | Before side median | After side median | Paired change |
|---|---:|---:|---:|
| Primary ns/op | 138,613 | 37,100 | -67.87% |
| Primary allocs/op | 130 | 116 | -10.77% |
| Secondary fallback ns/op | 207,916 | 251,002 | +0.58% |
| Secondary fallback allocs/op | 430 | 429 | -0.23% |
| Ordered-secondary allocs/op | 435 | 435 | 0.00% |
| Mixed wall | 1,798.062 ms | 1,861.466 ms | -0.63% |
| Mixed allocated | 922.209 MiB | 912.727 MiB | -1.00% |
| Mixed checkpoint service p99 | 104.431 ms | 99.084 ms | +4.55% |
| Mixed checkpoint request p99 | 400.993 ms | 426.547 ms | +16.19% |
| Read-mode wall | 513.542 ms | 480.004 ms | -3.72% |

There was substantial paired variation, including the write-only control.
Four of eight mixed wall pairs improved. The positive paired checkpoint tail
change is retained, not hidden by the lower after-side service median.
Service/queue intervals use the existing node clock; request/workload elapsed
intervals use QPC. The read mode includes index work as well as point reads.

## Longer Confirmation

The initial allocation result showed the fallback tax was removed, but timing
was too noisy for a default decision. A separately recorded confirmation used
unchanged production source and resource limits. Its only extra source was a
common test helper; both confirmation binaries and that helper were hashed
before timing. The confirmation does not supersede the initial batch.

Each mixed process increased fixed work within existing harness limits to
2,048 ACKs, 8,192 primary reads, 8,192 index reads and 512 checkpoints across the
same 16 databases. Eight measured alternating pairs and one warmup pair make
18 reports. The kernel workload context and test timeout were not relaxed.
Reopen/drain, Sync/cache/worker and no-observer checks remain unchanged.

The same three microbenchmarks then ran 10,000 calls per case, five measured
pairs and one warmup pair. All 12 process logs are retained.

The batches are separated in wall time, not pooled: initial reports were
written at 15:40-15:42 local time and confirmation reports at 20:53-20:55.
`timing-audit.json` records the file timestamps and within-batch report gaps.
It is not an external-load or OS sleep monitor, and absolute timings should
not be compared across these two workstation conditions.

| Metric | Before side median | After side median | Paired change |
|---|---:|---:|---:|
| Primary ns/op | 74,225 | 22,481 | -68.91% |
| Primary B/op | 17,646 | 15,791 | -10.51% |
| Primary allocs/op | 130 | 116 | -10.77% |
| Secondary fallback ns/op | 137,299 | 138,097 | -1.00% |
| Secondary fallback B/op | 72,471 | 72,406 | -0.09% |
| Secondary fallback allocs/op | 430 | 429 | -0.23% |
| Ordered-secondary allocs/op | 435 | 435 | 0.00% |
| Mixed wall | 3,018.227 ms | 3,067.322 ms | -8.01% |
| Mixed allocated | 2,423.235 MiB | 2,396.043 MiB | -1.16% |
| Mixed checkpoint service p99 | 72.377 ms | 63.058 ms | -2.02% |
| Mixed checkpoint request p99 | 342.312 ms | 270.871 ms | -18.72% |

The checkpoint-tail direction from the initial batch was not reproduced in
the longer workload. Both sets still vary materially, so they do not prove a
universal no-regression or tail-latency improvement. Retention is based on the
bounded primary benefit reproduced in both microbenchmark batches, removal of
the structural fallback allocation tax, and correctness/resource gates. Do
not advertise the confirmation's -8.01% as a general mixed-work speedup.
Paired-percent medians can even differ in direction from a ratio of the two
side medians; use the retained raw pairs rather than recomputing a different
statistic from the table's middle columns.

## Correctness And Qualification

Six new portable root tests cover snapshot-equivalent values/columns/errors
across primary and non-primary queries, misses, NULL and invalid parameters,
checkpointed/overlay values, both direct-read commit interleavings, schema
drop/recreate with a changed projected column type, cancellation after
preparation, saturated admission, concurrent updates/checkpoints and cleanup.
The profile-tag test additionally requires exactly one reservation for each
primary/non-primary statement, with zero/one snapshot capture respectively.

Frozen-candidate gates passed 939 root tests with 19 skips across 14 packages
(`./kitdb/... ./cmd/releasegate`), 201 root race executions over three repeats
across two packages, ten profile-control roots, normal/profile vet and build.
The one early smoke failure was a test expectation using `integer` instead of
the existing result metadata kind `int32`; it was corrected before freezing
and all timed work. No production failure was masked.

`integration.json` records a separate direct-checkout pass without an overlay:
939 root tests with 19 skips across 14 packages, 30 focused race executions
over three repeats, seven profile-tag roots, vet and build. The focused
current-checkout race selection is narrower than the 201-execution frozen
candidate selection; these counts are not added together as unique tests.
`final-index.json` hashes the captured source,
all four binaries, reports and logs. An existing out-of-kernel release-planner
difference (`./storage` in the shared focused-race command versus
`./utilities/sse` in the frozen planner) is recorded rather than treated as
whole-checkout equivalence; neither planner command ran this campaign.

This is local Windows evidence. Large catalogs, cold OS caches, other devices,
Linux, long soaks, physical power-loss behavior, wire-level latency and broad
production distributions are not qualified by these batches. No commit,
push, release promotion, power-plan change or new long-running service occurs.
