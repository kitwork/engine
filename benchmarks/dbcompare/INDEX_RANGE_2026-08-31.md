# Standalone Index Range Follow-Up: 2026-08-31

This is a measured follow-up to the [original comparison](RESULTS_2026-08-31.md),
not a replacement for its historical results. It changes the independent
`kitdb/relational` planner/executor, not Kitwork's ORM, VM or host runtime.
No KROW, WAL, index codec, module dependency or fixture data change was needed.
The existing real 13M-row shopping database was not opened or modified.

## Cause And Correction

The selected `(merchant,id)` index previously consumed `merchant = 7` as an
equality prefix, but did not push `id >= 500000` into the kernel cursor's byte
range. It hydrated thousands of earlier rows, only to reject them in SQL.
The before-profile put most query CPU/allocation under indexed-row hydration
(`Snapshot.Get`, `mainImage.readPage`, `decodeMutationPage`), not sorting.

The planner now intersects strict/inclusive bounds on the first non-equality
index field and uses the existing generation-aware cursor. SQL predicates
remain residual checks against the captured snapshot. Upper-only ranges exclude
NULL, whose index marker sorts before non-NULL values. Decimal byte order and
missing leading index components deliberately remain outside this optimization.

A second correction stops ordinary bounded pages as soon as the final matching
row is appended, rather than hydrating another row before stopping. Offset,
DISTINCT, aggregates, uncovered sorting and no-LIMIT overflow semantics remain
separate. LIMIT 0 does not need row decoding but still validates the SQL.

EXPLAIN now identifies the chosen access more precisely:

```text
index scan
table=products index=products_merchant_id estimated_rows=1000000 filters=2
equality_prefix=1 range=id direction=forward order=index
```

`estimated_rows` is still the table statistic, not an estimate of rows visited
after the range seek. Iterator prefetch/page reads are not promised to equal
the exact SQL result count.

## Paired Replay At 1M

```sql
SELECT id, name, price, merchant
FROM products
WHERE merchant = 7 AND id >= 500000
ORDER BY id
LIMIT 100;
```

Same previously generated synthetic database and midpoint UPDATE state. No
ingest, DDL, DML, refresh or index rebuild occurred during these replay runs.
Windows amd64, i7-11850H, Go 1.26.0, QPC, GOMAXPROCS=1, configured 64 MiB page
cache. Two warmups and eleven timing samples per query, parse/plan/execute and
materialization included. Every result is checked against the regenerated
independent fixture reference. Timing runs below did not enable the profiler.

| Run | Median ms | Min ms | Max ms | Cumulative Go allocated bytes/query |
| --- | ---: | ---: | ---: | ---: |
| Before correction | 155.6062 | 144.9882 | 187.0546 | 252,635,008 |
| After correction | 1.2260 | 0.8944 | 2.3788 | 1,012,008 |
| After, independent repeat | 1.2782 | 1.0467 | 2.2656 | 1,012,008 |

The paired median improvement is about 127x (122x using the repeat). Allocations
fall about 250x. These are **cumulative Go allocations, not resident or peak
memory**. The improvement removes wasted work for this indexed page; it does
not establish a universal query speedup or production p95/p99.

Controls from the first paired run:

| Query | Before median ms | After median ms | Allocated bytes before/after |
| --- | ---: | ---: | ---: |
| Primary-key lookup | 0.0603 | 0.0696 | 21,432 / 21,432 |
| Metadata COUNT(*) | 0.0539 | 0.0424 | 12,880 / 12,880 |

These tiny timing differences on a desktop are not evidence of improvement
or regression. Their access paths and allocation counts are unchanged.

## Smaller Replays

The same query shape uses `id >= rows/2`. These are after-only checks, not
additional paired speedup claims. At 10k only 78 matching rows exist.

| Table rows | Returned rows | Median ms | Cumulative allocated bytes |
| --- | ---: | ---: | ---: |
| 10,000 | 78 | 0.6733 | 326,592 |
| 100,000 | 100 | 0.6353 | 503,160 |
| 1,000,000, repeat | 100 | 1.2782 | 1,012,008 |

Different file generations, cache state and desktop load still affect reads.
Do not infer perfectly constant-time I/O from a LIMIT or extrapolate to 13M.
Uncached row hydration, low-selectivity residual filters, large OFFSETs and
index cost selection remain optimization opportunities.

## Reproduction And Evidence

From `engine/`, with a fresh output path:

```powershell
go run ./benchmarks/dbcompare `
  -replay .artifacts/dbcompare-20260831/1000000 `
  -updated -queries lookup,indexed_page,count_all `
  -repetitions 11 -out .artifacts/index-range-new-run
```

Raw reports retained locally under `.artifacts/`:

- `index-range-before/results.json`: same-fixture unprofiled baseline.
- `index-range-before-profile/`: separate diagnostic CPU/allocation profiles.
- `index-range-after/results.json`: first unprofiled corrected run.
- `index-range-after-repeat/results.json`: unprofiled repeat.
- `index-range-after-100k/results.json` and `index-range-after-10k/results.json`.

See [replay methodology](README.md#replay-and-profile-existing-kitdb-queries).
The allocation profile includes reference-generation allocations; per-query
allocation deltas and CPU stacks, not aggregate process bytes alone, identify
the execution bottleneck. Windows OS calls can appear as `runtime.cgocall` in
CPU profiles even in a CGO-disabled build; KitDB does not delegate to SQLite.

## Verification

- The initial range-seek and stop-at-LIMIT tests failed on the old executor and
  passed with the correction. A deliberately invalid row after LIMIT proves
  the extra SQL row is not decoded; requesting that row still fails.
- Indexed results match reference scans for inclusive/exclusive/repeated and
  contradictory bounds, NULL, residual predicates, OR/NOT, offsets, reverse
  order, escaped text, finite floats and integer/boolean boundaries.
- Committed overlay changes, uncommitted changes, old snapshots, checkpointed
  data, active row/index generations and UPDATE/DELETE ranges are covered.
- `go test ./kitdb/... ./cmd/kitdb ./cmd/kitdbpg -count=1 -timeout 5m` passed.
- Focused standalone range/LIMIT/generation tests passed under `-race`.
- Numeric/text range fuzzing passed bounded 8-second campaigns with two workers:
  126,393 numeric and 13,683 text executions; this is not exhaustive proof.
- Benchmark tests passed, including both optional native adapters. Replay tests
  verify unchanged source transaction, independent answers, rejected arbitrary
  SQL/overwrite, wrong-state failure reporting and emitted profiling files.
- `go test -race ./benchmarks/dbcompare` and `go vet` for the two changed packages
  passed. The standalone commands and benchmark build with `CGO_ENABLED=0`.
- Kitwork/standalone format compatibility and existing ORM planner tests passed.
  The ORM test first hit a sandbox temporary-path denial; rerunning with
  `TMP`/`TEMP` inside the writable workspace passed without code changes.
- Standalone dependency inspection contains no `work`, `core`, `vm`, Kitwork
  `runtime`, SQLite or Turso package. SQLite remains a benchmark-only comparator.

## Remaining Work

GROUP BY is still the measured scalar fallback and was not optimized here.
KROW scan allocations, ingestion/checkpoint cost and stale-projection fallback
also retain the limits described in the original report. No new SQLite/DuckDB
performance campaign, server restart, production migration or multi-tenant
load test is claimed by this follow-up.
