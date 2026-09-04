# Standalone Grouped Batch Results: 2026-08-31

Measured on the existing 1,000,000-row synthetic fixture through standalone
`kitdb/relational`, not the Kitwork ORM/VM or a PostgreSQL listener. The real
13M shopping database was not opened, modified or migrated. No KROW, KCOL,
container, catalog or WAL format changed; no module dependency was added.

## What Changed

- Integer/boolean GROUP BY keys (including composite keys and NULL) now use
  typed KROW or KCOL batches for eligible full scans. Groups own their keys and
  projected values; a reusable binary key buffer avoids per-row JSON grouping.
- COUNT, numeric SUM/AVG, fixed-width MIN/MAX and GROUP BY without aggregates
  reuse typed accumulators. WHERE supports the existing numeric/boolean batch
  predicates. HAVING, aliases, parameters, ordering, offset and limit share
  scalar post-aggregation code; a LIMIT does not truncate the input scan.
- Grouped and ungrouped reads share one exact-snapshot KCOL/KROW scan helper.
  A stale/missing/corrupt projection discards partial state before restarting
  on the authoritative snapshot. Consumer/resource errors and cancellation
  return errors instead of silently retrying or returning partial groups.
- A correctness fix normalizes legacy integer/boolean group identities to the
  declared type in scalar grouping. A BOOLEAN encoded as `1` and one encoded
  as `true` must not produce two visually identical groups. The raw encoding
  is unchanged. New tests reproduce the old split and cover all three paths.

This is opt-in, through existing `BatchAggregates` or `ExperimentalProjections`
options. KCOL still needs explicit refresh. No scheduler, automatic refresh,
new schema DSL, full-table cache or precomputed GROUP BY result was introduced.

## Workload And Method

```sql
SELECT merchant, COUNT(*), SUM(price)
FROM products
GROUP BY merchant
ORDER BY merchant;
```

Here `merchant` is **INTEGER with 64 distinct values**, not the textual
merchant field of the real shopping dataset. The seven-column fixture contains
nullable price/rating, a boolean and a wide repetitive text description. Both
typed paths scan exactly 1,000,000 rows in 977 batches and return 64 groups;
KCOL reads the requested merchant/price columns, not the text description.

Windows amd64, Intel i7-11850H, Go 1.26.0, GOMAXPROCS=1, one query at a time,
64 MiB configured page cache. Windows QPC timing, two warmups then seven samples,
including SQL parse/plan/execute and full result materialization. No profiler
was enabled. Every answer matches the independent regenerated fixture oracle,
including the midpoint price UPDATE from the original campaign.

Replay does not ingest, update, rebuild an index or refresh KCOL. Normal
open/close housekeeping is permitted, so this is read-only SQL rather than a
bitwise-immutable open. OS cache and desktop load are not controlled. Before
measuring the final runs, test/build work had finished.

## Final-Code Measurements

All latency values are milliseconds; allocation is a separate execution after
the timed samples, using the process Go `TotalAlloc` delta.

| Mode | Median ms | Min ms | Max ms | Cumulative allocated bytes/query |
| --- | ---: | ---: | ---: | ---: |
| Scalar control | 3134.5984 | 3096.8817 | 3326.5525 | 3,886,280,424 |
| KROW typed grouping | 1031.7728 | 1024.9207 | 1061.2037 | 1,185,206,616 |
| KCOL typed grouping | 59.2016 | 57.8697 | 66.2242 | 199,616 |
| KCOL independent repeat | 47.1810 | 45.5394 | 48.5386 | 199,616 |

On this workload, KROW batches are about 3x faster than the final scalar
control; KCOL is about 53-66x faster. The KCOL allocation sample is about
0.20 decimal MB. **None of these allocation figures is resident or peak RAM.**
KROW still allocates heavily in the underlying scan/decoder. This does not
establish a general analytics speedup, zero allocation or production p95/p99.

## Before And Intermediate Runs

All raw runs are retained rather than selecting the most flattering baseline:

| Stage | Group median ms | Allocated bytes/query |
| --- | ---: | ---: |
| Before grouped execution, KCOL option fell back to scalar | 4498.1267 | 3,854,280,224 |
| Initial grouped KCOL | 48.8504 | 199,616 |
| Initial grouped KROW | 1258.1920 | 1,185,207,024 |
| Intermediate scalar control, before legacy normalization | 3815.4499 | 3,854,280,160 |

The initial before run ranged from 4168.98 to 8316.51 ms, illustrating desktop
variability. The final same-code mode comparison above is the more useful
comparison; do not advertise the entire 4.50-second-to-47-ms gap as a stable
speedup. Legacy normalization also adds work/allocations to scalar grouping;
the final scalar memory sample is not the old scalar implementation.

Final-code controls on the same file:

| Query | Median ms | Allocated bytes/query | Path |
| --- | ---: | ---: | --- |
| Indexed 100-row page | 1.0568 | 1,012,008 | scalar-or-index |
| Ungrouped full numeric aggregate | 39.3942 | 173,984 | kcol-batch |

These controls retain the prior index optimization and functioning ungrouped
batch path. Small differences from previous campaigns are not a statistically
established speedup or regression.

## Supported Boundaries

- Group keys: integer-family/system integer and boolean, including NULL.
  Float, text and decimal keys retain scalar execution. Float aggregate values
  are supported; float group identity (notably signed zero) is not promoted.
- Joins, unsupported predicates/expressions and uncommitted transaction writes
  retain their existing scalar/index paths. Selective primary/unique/secondary
  access is not displaced by a full batch scan.
- State is bounded by the existing configured group limit and an additional
  16 MiB accounted group-state budget. The latter estimates key, map/slice,
  accumulator and projected-value cost; it is not a strict process-memory cap.
  There is no disk spill. Wide/high-cardinality grouped queries may hit this
  budget before the group-count limit and receive an explicit error.
- LIMIT/HAVING do not circumvent either group budget. Two batch scans at a
  time share the existing per-engine admission channel, with cancellation.
  This is not a fleet-wide resource governor.
- Projection freshness is still exact at the database snapshot level. A
  write can send the next grouped query back to KROW until explicit refresh.
  No delta merge, group metadata shortcut, compression, SIMD or partitioning
  was added by this work.

## Reproduce

From `engine/`, use a **new output directory** for each mode/run:

```powershell
go run ./benchmarks/dbcompare `
  -replay .artifacts/dbcompare-20260831/1000000 `
  -replay-mode kitdb-analytics -updated -queries group_merchant `
  -repetitions 7 -out .artifacts/group-replay-new
```

Use `kitdb-row` or `kitdb-batch` for the other modes. Columnar replay requires
the existing fresh sidecar. The runner now asserts the actual grouped path;
a stale/missing sidecar cannot produce a falsely labeled KCOL benchmark.
Reports include `Execution.Groups`, `RowsScanned`, `Batches` and fallback reason.

Raw local evidence is under `.artifacts/group-batch-before`,
`group-batch-after-kcol`, `group-batch-after-krow`, `group-batch-scalar-control`,
`group-batch-final-scalar`, `group-batch-final-krow`, `group-batch-final-kcol`,
`group-batch-final-kcol-repeat` and `group-batch-final-controls`.

## Verification

- The initial test failed because GROUP BY did not use batches. Corrected
  grouped results match scalar rows/types over multiple batches and SQLite
  over composite keys, filtering, aggregates, aliases, HAVING and paging.
- Explicit tests cover empty input, NULL/zero/false, all-NULL aggregates,
  parameters, repeated/unprojected group fields, unsupported-shape fallback,
  legacy scalar representations, old snapshots, update/delete/insert,
  uncommitted writes, refresh and reopen.
- A later KCOL data block is deliberately corrupted after earlier groups were
  consumed; fallback returns exactly the canonical answer and clean counters.
- Group-count and wide-state byte budgets reject without partial rows, even
  with LIMIT/HAVING. Callback errors are not retried as storage failures.
  Cancellation during scan and while waiting for admission is covered.
- Concurrent grouped readers/refresh pass the race detector. A retained
  allocation test verifies zero allocations for existing-group key lookup;
  this is not a zero-allocation claim for an entire query.
- `go test ./kitdb/... ./cmd/kitdb ./cmd/kitdbpg ./benchmarks/dbcompare -count=1
  -timeout 5m` passed, including optional native SQLite/DuckDB adapter tests
  through the existing isolated Python environment.
- Focused group/projection/replay/SQLite-differential tests passed with `-race`.
  `go vet ./kitdb/relational ./benchmarks/dbcompare` passed.
- Standalone commands and benchmark build with `CGO_ENABLED=0` (the local Go
  tool emitted a non-fatal module stat-cache permission warning; exit was 0).
  Dependency inspection found no Kitwork work/core/VM/runtime, SQLite or Turso
  dependency in `kitdb/relational`; comparison drivers remain in the harness.

No fresh 1M comparison campaign against SQLite/DuckDB, real 13M test, FTS test,
server restart, production migration or production-readiness claim is made.
The next broadening requires text-key vectors/dictionaries and their own
correctness/resource gates, not labeling every GROUP BY as columnar now.
