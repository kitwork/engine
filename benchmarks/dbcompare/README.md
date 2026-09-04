# Standalone SQL Comparison

This is a reproducible workload runner, not a Kitwork application. KitDB is
opened through `kitdb/relational.OpenWithOptions` and queried through `Execute`.
No ORM, `struct()` DSL, tenant, VM, HTTP server or PostgreSQL listener is involved.
KitDB never delegates a query to the comparison engines.

## Profiles

| Profile | API / storage |
| --- | --- |
| `kitdb-row` | Standalone SQL, canonical KROW, scalar execution |
| `kitdb-batch` | Same database, optional typed KROW batch aggregates |
| `kitdb-analytics` | Same database, explicit experimental KCOL refresh |
| `sqlite-go` | Existing `modernc.org/sqlite` dependency, `database/sql` |
| `sqlite-native` | Python stdlib SQLite, native C SQLite |
| `duckdb` | Optional Python DuckDB wheel, native DuckDB |

No dependency is added to the engine's `go.mod`. The Python comparators are
separate child processes, run sequentially, and time SQL inside their own
processes. Startup and pipe/JSON transport are **not** included in SQL timings.
SQLite Go/native results must not be conflated: driver/language/API overhead
differs, especially for point lookups. This is an application-facing SQL
comparison, not an isolated storage-kernel benchmark.

## Run

From the engine repository:

```powershell
go test ./benchmarks/dbcompare -count=1

# KitDB's three profiles and Go SQLite, no external runtime required:
go run ./benchmarks/dbcompare -rows 10000,100000 -repetitions 7

# Optional isolated native comparator, not a KitDB runtime dependency:
python -m pip install --only-binary=:all: --no-deps --target ../.tmp/dbcompare-python duckdb==1.5.0
go run ./benchmarks/dbcompare -rows 10000,100000,1000000 -repetitions 7 `
  -python python -python-site ../.tmp/dbcompare-python
```

Use `-native sqlite-native` to run only the stdlib native comparator. A requested
but unavailable engine fails the campaign; it is not silently omitted. DuckDB
does not install/load extensions for this test. See its
[Python API](https://duckdb.org/docs/current/clients/python/overview) and
[configuration reference](https://duckdb.org/docs/current/configuration/pragmas).

The runner creates a **new** directory under `.artifacts/`, or the supplied
`-out`. It refuses to overwrite an existing run, never discovers/connects to
user databases and never deletes the output. Both database files and generated
SQL remain available for inspection. Do not pass a production database directory.
The default campaign has a 30-minute timeout. Native workers are terminated by
the parent on cancellation; partial runs are not valid successful results.

Limits: up to five scales, 128..1,000,000 rows each, 1..128 rows per INSERT,
1..100 samples, a configurable checkpoint interval and cache. The common SQL
token budget determines the maximum INSERT batch for this seven-column fixture.
Large campaigns consume disk for SQL fixtures **and** each independent database.

## Fairness And Correctness

New fixtures now resolve the existing `price BIGINT` declaration to KitDB's
exact `bigint` kind. SUM(price) is NUMERIC text and uses an exact accumulator in
all three KitDB profiles. The comparator accepts decimal text against numeric
expectations without rounding integer digits (fractional float expectations
retain the tolerance below). Older persisted fixtures retain legacy `integer`
semantics: replay does not migrate them. Do not present historical/replayed
timings as a measurement of the new BIGINT contract; record fresh schema and
engine revision for new campaigns. The shared SQL fixture has not changed.

- One deterministic SQL fixture for every engine; no random-seed drift or COPY
  versus per-row-INSERT mismatch. Generation/file writing is outside ingest timing.
- Seven columns: integer PK, 64 merchants, name, nullable integer price, nullable
  quarter-step rating, bool, and a 248-byte mostly repetitive description.
  It is synthetic, correlated and compressible, **not** the 13-million-row shop
  dataset. Compression ratios cannot be generalized to real descriptions.
- Same `INTEGER PRIMARY KEY` and `(merchant,id)` index created **before** loading.
  Engines may implement these differently. Native SQLite can use its rowid PK.
- Each multi-row SQL INSERT commits separately and durably. KitDB uses its
  ordinary synced WAL. SQLite uses WAL + `synchronous=FULL`, no mmap, no automatic
  checkpoint. DuckDB uses its default persistent durability, not a memory DB.
- All engines explicitly checkpoint every 16,384 loaded rows and at the end.
  This bounds KitDB's uncheckpointed overlay. `insert_sql`, `checkpoints` and
  `ingest_wall` are separate. They overlap: do not add `ingest_wall` to the others.
- This tests small-batch SQL ingestion, **not maximum bulk import throughput**.
  DuckDB appender/COPY and alternate transaction sizes are not measured.
- `GOMAXPROCS=1`, one connection/request at a time, DuckDB `threads=1`.
  KitDB and SQLite get a 64 MiB page-cache setting; DuckDB gets a 512 MB memory
  limit. These controls cover different memory categories, **not equal RSS caps**.
  OS cache is uncontrolled. No memory-efficiency ranking is claimed.
- Two warmups then seven individually timed queries; include parse/plan/execute
  and full result materialization. No explicit prepared statement reuse;
  Python SQLite has `cached_statements=0`. Median/min/max and all raw samples
  are retained. Seven samples do **not** establish a production p95/p99.
- Engine order is fixed and this is a desktop machine, not an isolated lab.
  Repeat campaigns/orderings before drawing small-difference conclusions.
- Windows uses QPC for Go timings: Go interrupt-time resolution otherwise
  quantizes some small operations to zero. Python uses `perf_counter_ns`.
- Every result is checked against an independent streaming Go reference, not
  just against another database. Exact row count/order/NULLs are checked, with
  numeric tolerance `1e-9`. Fixture magnitudes stay below float64's exact-integer
  limit and ratings are exactly representable quarter steps.
- SQL plans are recorded. KCOL/batch aggregate profiles assert the actual
  execution path. Plain `COUNT(*)` may use metadata; it is not a scan benchmark.
  GROUP BY now asserts grouped KROW/KCOL batch execution in those profiles;
  the original comparison predates this optimization.
- Go allocation is sampled in one extra execution outside the timing series.
  It is cumulative process `TotalAlloc` delta, **not peak RAM/RSS**, and includes
  incidental Go allocations. No native-memory comparison is derived from it.

## Columnar Cost And Freshness

The three KitDB profiles reuse one canonical ingestion. The repeated ingestion
fields in their reports describe that **shared** load, not three independent
loads. Each profile reopens the file; warmups prevent calling this a cold test.
History retention is enabled only when opening the columnar profile, after load.

The columnar profile records the initial refresh separately. After the read
suite, a single durable UPDATE changes the price of the midpoint row:

1. Query before refresh must return the new total using `krow-batch`, not stale KCOL.
2. Explicit refresh records time, reused/decoded rows and appended/copied bytes.
3. Unchanged refresh must write zero bytes (it still performs verification work).
4. The query after refresh must return the same correct result through `kcol-batch`.

SQLite/DuckDB also run the same UPDATE and post-update aggregate. These one-shot
mutation timings are diagnostics, not latency distributions. File sizes are
captured while open, after that profile's measured work, including WAL, history,
indexes and KCOL where present. Report canonical/derived/history separately;
do not compare KCOL alone to another engine's entire database. File length is
not physical filesystem allocation or peak construction disk usage.

## Outputs

```text
.artifacts/dbcompare-<timestamp>/
  environment.json        Go build dependencies, runtime, clock/method context
  results.json            combined report; errors are explicit
  100000/
    workload.json         schema, SQL queries and independent expected results
    inserts.sql           identical streaming ingestion fixture
    kitdb.json            three SQL execution profiles
    sqlite-go.json
    sqlite-native.json
    duckdb.json
    kitdb/data.kitdb...    canonical file, WAL, lock, optional projections/history
    sqlite-go/data.sqlite...
    sqlite-native/data.sqlite...
    duckdb/data.duckdb...
```

Tests cover standalone fixture execution in all three KitDB modes, the existing
SQLite driver, post-update freshness, refresh, NULL/numeric checking and refusal
to overwrite fixtures. Native report validation rejects missing/duplicate queries
and invalid samples. Set `DBCOMPARE_PYTHON` and `DBCOMPARE_PYTHON_SITE` to run
`TestNativeComparison` with both native adapters as well. Native correctness is
also checked during an opt-in campaign.

## Replay And Profile Existing KitDB Queries

To investigate a read bottleneck without ingesting another fixture:

```powershell
go run ./benchmarks/dbcompare `
  -replay .artifacts/dbcompare-20260831/1000000 `
  -updated -queries lookup,indexed_page,count_all `
  -repetitions 11 -out .artifacts/index-range-replay
```

`-replay` names the scale directory containing `workload.json` and
`kitdb/data.kitdb`. It only executes SELECT/EXPLAIN from the fixed fixture query
set, and regenerates expected results independently. `-updated` expects the
midpoint price change made at the end of a completed campaign; omit it for an
unmodified ingestion. A wrong state fails result validation. This is for the
synthetic benchmark fixture, not an arbitrary application database.

Replay defaults to `-replay-mode kitdb-row`. Select `kitdb-batch` or
`kitdb-analytics` to measure typed execution against the same source; columnar
replay uses an existing sidecar and never rebuilds/refreshes it. Supported
fixture aggregates (including GROUP BY) must report the requested batch path;
missing/stale KCOL fallback is a failed benchmark, not a columnar timing.
JSON includes execution counters when the engine supplies them.

There is no ingestion, DDL, DML or projection refresh. Normal database open/close housekeeping is still
allowed; this is read-only SQL, not a bitwise-immutable filesystem open. It
requires an existing source file and a new output directory, never overwrites
a report, and records samples, allocations and EXPLAIN in `results.json`.

Add `-profile` in a **separate run with a new output directory** to write
`cpu.pprof` and `allocs.pprof`. Use `go tool pprof -top` on these files. CPU
profiling excludes fixture-reference generation and database open; the
cumulative allocation profile includes earlier process allocations, including
reference generation. Per-query `go_allocated_bytes` remains a separate
`TotalAlloc` delta. Do not mix profiled timings into unprofiled before/after
comparisons. Replay tests check results, unchanged source transaction, failure
reporting, profile output and refusal to overwrite or run arbitrary SQL.

See [the index-range follow-up](INDEX_RANGE_2026-08-31.md) for the measured
standalone planner correction after the original comparison.
See [the grouped batch follow-up](GROUP_BATCH_2026-08-31.md) for grouped analytics.
See [the 13M dictionary text follow-up](TEXT_ANALYTICS_13M_2026-09-04.md) for
the separate opt-in real-data KitDB paths and remote read-only PostgreSQL
baseline; those measurements are not produced by this synthetic runner.

## Not Measured

PostgreSQL/Turso, FTS ranking, joins, time partitions, multi-tenant concurrency,
read/write contention, cold OS cache, disk failure, crash durability, long-term
compaction, arbitrary SQL and 13-million-row real data are **not** covered.
This runner must not be used to claim production readiness or general parity
with SQLite/DuckDB. Its purpose is to reveal the next bottleneck without hiding
fallbacks or build/refresh cost.
