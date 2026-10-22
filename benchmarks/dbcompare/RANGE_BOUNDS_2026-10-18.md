# Bounded Range Reads - 2026-10-18

## Scope And Result

This follows [prefix-fenced read paths](READ_PATHS_2026-10-18.md). Normalized
inclusive Start, exclusive End and Prefix now constrain the physical page
interval in each segment for Cursor, Scan and ScanKeys, in both directions.
Only pages wholly outside the interval are skipped. Boundary pages still need
their complete checksum and row-level filtering. No format, writer geometry,
cache, worker-count or durability policy changed in this step.

The direct evidence is less page I/O and, for allocating cursors, fewer bytes
allocated. SQL latency is not a consistent win: the first normal 4/16-database
sweep regressed, while an after-first repeat did not reproduce that regression.
These are short synthetic Windows measurements, not a production qualification
or a comparison with SQLite, DuckDB or PostgreSQL.

## Implementation Boundaries

- Binary-search each segment's validated directory for the first page whose
  last key reaches Start and the first page whose first key reaches End.
- Missing legacy last-key fences remain conservative: read the candidate page.
- Filter overlay keys against both bounds before sorting them.
- Construct only the requested iterator direction. Previously reverse reads
  first constructed and discarded an eager forward merge, reading one page per
  segment before constructing the actual reverse merge.
- Preserve newest-generation deduplication and tombstones. LIMIT is applied
  after the global logical merge, never independently within each segment.
- Keep ordinary cursor ownership and callback-scoped borrowed storage unchanged.
  There is no new retained cache or process-global state.
- Validate total main record count only for a complete unbounded main scan;
  a bounded scan cannot establish that count. Verify still checks the whole DB.

## Native Range Measurements

Fresh TempDir fixtures contain eight checkpointed generations, each with 200
keys in each of three separated prefixes (a/, m/, z/), 512-byte values, and no
overlay during timing. The page cache is disabled. Each prefix occupies two
pages per generation. Benchmarks consume all 200 middle-prefix results or an
empty gap between prefixes. Three repetitions, fixed 50 iterations, CPU=4.
The same benchmark body was frozen before and after the implementation.

| Workload / API | Before pages/op | After pages/op | Before B/op (approx.) | After B/op (approx.) |
| --- | ---: | ---: | ---: | ---: |
| Middle prefix, forward Cursor | 24 | 16 | 1,682,100 | 1,026,100 |
| Middle prefix, forward Scan | 24 | 16 | 659,440 | 659,450 |
| Middle prefix, forward ScanKeys | 24 | 16 | 658,040 | 658,020 |
| Middle prefix, reverse Cursor | 24* | 16 | 2,051,300 | 1,026,100 |
| Empty gap, forward Cursor | 8 | 0 | 658,390 | 1,920 |
| Empty gap, forward Scan | 8 | 0 | 659,435 | 2,432 |
| Empty gap, forward ScanKeys | 8 | 0 | 658,007 | 2,032 |
| Empty gap, reverse Cursor | 8* | 0 | 1,027,580 | 1,912 |

All three APIs in both directions show 24 -> 16 reported pages for the middle
prefix and 8 -> 0 for the gap. Reverse Scan/ScanKeys use the ordinary reverse
reader; their middle-prefix allocation is about 2,050,780 -> 1,025,580 B/op.

*Before reverse counters exclude the discarded forward iterator's eager reads.
They are not a complete count of old reverse physical I/O. Allocation counters
include that initialization; the new path does not construct it at all.

Forward middle-prefix page bytes fall from 1,370,200 to 843,200 per operation.
Reported reverse bytes fall from 1,159,400 to 843,200, with the same old-counter
caveat. Forward borrowed Scan/ScanKeys already reuse page buffers, so skipping
reads does not proportionally reduce allocation. Avoid interpreting B/op as
peak RAM or assuming a new memory-admission policy.

Representative timing ranges across three repetitions:

- Middle forward Cursor: 0.576-0.656 ms -> 0.438-0.504 ms.
- Middle forward Scan: 0.600-0.946 ms -> 0.417-0.445 ms.
- Middle reverse Cursor: 0.826-0.927 ms -> 0.496-0.606 ms.
- Empty gaps after pruning: approximately 1.1-2.9 us across methods, versus
  approximately 0.18-0.58 ms before. These are diagnostics, not SLA estimates.

## SQL Diagnostic And Mixed Workloads

The existing 17-generation relational fixture and row size 900 are unchanged.
Point and metadata allocations are unchanged. The index query still allocates
about 774 KB / 1,069-1,071 objects: its range overlaps much of the same physical
prefix, so this change need not remove many of its candidate pages.

The first index microbenchmark sweep measured 0.614-0.636 ms before and
0.847-1.087 ms after. An additional after-first, 200-iteration sweep measured
0.555-0.761 ms after and 0.517-0.762 ms before. Preserve both observations:
there is no consistent standalone SQL-index latency improvement here.

Mixed campaigns use native relational SQL, not pgwire. Each case runs for eight
seconds plus in-flight drain in a fresh process and disposable database set:
GOMAXPROCS=4, 256 KiB page cache per DB, 8 MiB snapshot/write budgets, at most two
managed checkpoint workers and 100 ms checkpoint cadence. Normal readers are
paced at 10 ms and writers at 20 ms; noisy DB zero has one unpaced 32-row writer.
Query-result caching and the automatic checkpoint evaluator are off. Our own
builds, tests and profiling did not overlap measurements; OS/apps are not fully
controlled. Histograms report bucket upper bounds, not exact percentiles.

Primary order was before then after for each case. Rates include in-flight drain.

| Case | Revision | Point/s | Index/s | Commit/s | Point p95/p99 ms | Index p95/p99 ms |
| --- | --- | ---: | ---: | ---: | --- | --- |
| Normal 1 | Before | 94.5 | 93.4 | 47.4 | 16/50 | 16/50 |
| Normal 1 | After | 93.9 | 92.6 | 47.3 | 16/50 | 16/50 |
| Normal 4 | Before | 384.0 | 382.9 | 196.4 | 16/25 | 16/25 |
| Normal 4 | After | 326.8 | 320.7 | 169.7 | 20/100 | 25/100 |
| Noisy 4 | Before | 387.2 | 383.4 | 367.0 | 16/25 | 16/50 |
| Noisy 4 | After | 378.1 | 376.6 | 378.8 | 16/50 | 16/50 |
| Normal 16 | Before | 1460.9 | 1449.9 | 750.0 | 12/50 | 16/50 |
| Normal 16 | After | 1240.9 | 1190.4 | 664.4 | 25/100 | 50/100 |
| Noisy 16 | Before | 1383.6 | 1337.0 | 766.6 | 20/50 | 25/50 |
| Noisy 16 | After | 1513.9 | 1500.4 | 857.7 | 12/50 | 16/50 |

Additional pairs deliberately measure after first, then before, for the two
regressing normal cases; these supplement rather than replace the primary run.

| Case | Revision | Point/s | Index/s | Commit/s | Point p95/p99 ms | Index p95/p99 ms |
| --- | --- | ---: | ---: | ---: | --- | --- |
| Normal 4 | After | 397.1 | 397.7 | 199.7 | 8/16 | 10/16 |
| Normal 4 | Before | 395.4 | 395.8 | 199.3 | 10/20 | 10/20 |
| Normal 16 | After | 1557.7 | 1558.9 | 787.8 | 10/20 | 10/20 |
| Normal 16 | Before | 1493.3 | 1478.6 | 767.3 | 12/50 | 16/50 |

The primary normal-case regressions did not repeat in those pairs. This does
not identify their cause or rule out a regression under other conditions.
Do not average away the first sweep or claim a database-wide throughput win.

## Correctness And Verification

All 14 mixed reports pass independent full row/version/payload and secondary
index verification after reopen: 50,294 acknowledged transactions (34,446 in
the primary sweep and 15,848 in the additional pairs). Per-tenant commit syncs
equal ACKs. Snapshot, transaction, admission and pending-commit charges drain
to zero; managers close with zero managed DB handles, leases and maintenance
jobs. These facts do not classify all process OS handles or prove a long-term
RSS/handle leak cannot occur.

Regression coverage includes independent range oracles over old and WAL-backed
snapshots, a checkpoint with retained readers, bounds/prefix intersections,
inverted/empty intervals, exact page edges, all-FF prefixes, overlay-only keys,
reverse ordering, LIMIT after newest tombstones, legacy v1 pages without upper
fences, and the immutable historical v3 fixture (not regenerated).
Corrupted unselected pages are skipped by bounded reads, while selecting them
or calling Verify fails checksum validation. Empty gaps assert zero page reads.

Completed checks on the final implementation:

- Full kernel/node/relational suites: PASS, 35.551s / 15.000s / 114.117s.
- Focused kernel/relational race suites, count=2: PASS, 18.609s / 18.017s.
- Tagged SQL checkpoint-publication crash and storage-I/O fault matrices: PASS, 21.136s.
- KitDB/node/relational vet and scoped build: PASS.
- Whole-engine `go build -mod=readonly ./...`: PASS, with GOPROXY=off.
- Host `TestKitDBDatabaseReleaseGate`: PASS, 3.937s.

The earlier UI compile failures documented in the preceding report no longer
block this build/host gate; this task did not edit those UI files. The complete
UI/browser test suite, Linux, a clean-candidate release campaign, 24-hour canary
and hardware power-loss validation were not run. No stable promotion is made.

## Provenance And Reproduction

HEAD: e89bd9824396a2684c7669a029e94d9b526f1910, dirty worktree. No commit/push.
Both binaries include the earlier scan-buffer, immutable-overlay, checkpoint
probe and prefix-fenced writer changes. Only this range-reading step differs.
Test coverage expanded after freezing binaries; the measured benchmark body
did not change. The initial limit-eight native binary is superseded and is not
used in the table above; the baseline below consumes the full middle prefix.

| Frozen binary | SHA-256 |
| --- | --- |
| r1/kernel-full.test.exe | `41fe44f9ccac3a4f02e5e31b419f48d43193e90cea1db90b0236c0514cae4bd8` |
| r2/kernel.test.exe | `4aade2879dc490e4605e4a1ffb62bc4e1a166b8c0a3b5ef8ef01602b1d89193c` |
| r1/read.test.exe | `9703e9f24ae95abf3ca9d9c85e782149477178f163ff3fbb34c7d795527a7393` |
| r2/read.test.exe | `a68b2a1883867345b1778710b74e1c4469a4a7d43cd4c200864d333a3d103cdd` |

Raw evidence lives in `.artifacts/kitdb-range-20261018-r1/` and `r2/`:
`ranges.log`, `read-paths.log`, `reverse-index-bench.log`, the ten `mixed-*`
and four `reverse-*` JSON/log pairs. r2 also contains test/build logs and the
derived `summary-validated.json` with raw report and final source hashes.

From engine/, freeze one binary per implementation into a fresh directory:

```powershell
go test -c -o .artifacts/range-new.test.exe ./kitdb
& ./.artifacts/range-new.test.exe '-test.run=^$' '-test.bench=^BenchmarkBoundedRanges$' '-test.benchmem' '-test.benchtime=50x' '-test.count=3' '-test.cpu=4'
go test ./kitdb '-run=^TestBoundedRange' -count=1
```

Use the [mixed campaign controls](MIXED_2026-10-18.md) for the five named SQL
cases, with profiling disabled. Keep raw attempts and record ordering. The
next optimization should follow a reproducible SQL CPU/allocation profile,
not assume that fewer kernel page reads solve SQL execution and checkpoint
contention together.
