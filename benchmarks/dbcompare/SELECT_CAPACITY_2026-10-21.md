# SQL Source-Row Capacity: Windows, 2026-10-21

## Result

One bounded allocation change in `kitdb/relational/select.go` reduces allocated
bytes for a native SQL point lookup by approximately **5.31%** on this fixture.
It does not change row decoding, indexes, the storage format, WAL, or public APIs.

| Case | Before B/op | After B/op | Reduction | Before/after allocs/op |
| --- | ---: | ---: | ---: | ---: |
| SQL point | 43,335 | 41,032.5 | 5.31% | 144 / 144 |
| SQL index | 534,707 | 534,563 | 0.027% | 506 / 506 |

Values are medians of six samples per implementation and case. The index result
is effectively unchanged; do not attribute its small difference to this change.
Allocated bytes are cumulative allocation per operation, not retained heap or
peak process memory. Fractional medians come from averaging the middle samples.

## Change

Previously, `readMatchingRows` reserved up to 256 source-row pointers even when a
primary/unique access path could return at most one row. It now reserves at most
one for those paths. COUNT starts with zero source-row capacity because it
retains no rows; bounded Top-N starts with zero because its heap owns candidates
and it builds a separate result slice afterward. Other paths keep their existing
capacity policy. No new cache or pool was added.

`TestSelectSourceRowCapacity` checks primary hit/miss, unique lookup, COUNT,
bounded scan, Top-N, OFFSET, and LIMIT 0. It checks source-row capacity, access
path, result count, total matched rows, and selected result identities.

## Measurement

- Windows/amd64, Intel Core i7-11850H, Go 1.26.0, GOMAXPROCS 4.
- Existing `BenchmarkFixedReadWork/sql_point` and `sql_index`, unchanged body.
- 1,024 logical rows, 512-byte payloads, 16 deterministic 32-row updates,
  17 immutable generations, 256 KiB page cache, 8 MiB snapshot/write budgets.
- SQL is parsed, bound, executed, and materialized for each operation. Result
  caching is off. Independent result validation is inside both measured loops.
- Point uses a deterministic ID schedule; index reads use an equality bucket,
  `ORDER BY id LIMIT 16`. Measured operations do not write or checkpoint.
- Unprofiled binaries run sequentially in before/after/after/before order, each
  with `-test.benchtime=2s -test.count=3 -test.cpu=4`. No own build, profile, or
  test process runs concurrently with these final measurements.

| Case | Before median us/op | After median us/op | Before range | After range |
| --- | ---: | ---: | ---: | ---: |
| SQL point | 118.446 | 103.614 | 109.355-139.748 | 95.601-110.105 |
| SQL index | 550.754 | 499.695 | 502.958-664.510 | 479.542-555.005 |

These are observed sample summaries, not confidence intervals or a universal
latency improvement. Both timing ranges overlap, and the unchanged index control
also moved. The defensible local improvement is reduced point allocation.

Separate baseline profiles still identify page reads/decoding as the larger
allocation target: `readPageInto` accounts for 46.01% of sampled flat allocation
in point reads; in index reads it accounts for 70.03%, with
`decodeMutationPageRows` another 19.03%. Allocation profiles include process
setup and benchmark calibration; they are not isolated retained-memory totals.
The source-array change does not solve this larger bottleneck. No after-profile
or end-to-end website speedup is claimed.

## Verification And Retained Failure

The first lifecycle gate failed `TestLifecycleOwnerSmoke`; its race step was
not run. Both controlled Windows WAL-denial cases passed. A diagnostic-only
repeat reproduced an otherwise successful, fully drained window measured at
349.9255 ms against the required 350 ms. Nine of ten diagnostic repeats passed;
the diagnostic run as a whole failed and is retained as such.

A separate test-only correction checks the elapsed measurement clock after a
timer wakeup and waits the remaining time if necessary. It preserves the 350 ms
minimum, cancellation, and stop behavior; it does not round up reported elapsed
time or retry production WAL operations. A deterministic test simulates the
early wakeup, cancellation, and stop cases.

After that correction:

- Ten repeats of deadline/capacity/lifecycle tests passed: 30 root passes.
- Three race repeats of the same tests passed: nine root passes.
- Full relational suite passed: 395 root passes, ten expected opt-in/helper
  skips; this is not an all-package engine run or full-suite race run.
- Relational `go vet` passed.
- The dedicated lifecycle gate passed normal and race steps, each with all
  11 required Windows test entries and no skipped tests, in 23.025 seconds.

The original failed gate and timing diagnostic are not overwritten. This timing
failure is separate from the earlier unresolved Windows WAL-publication failure
described in [lifecycle diagnostics](LIFECYCLE_2026-10-20.md).

## Provenance And Limits

Evidence is local under `.artifacts/kitdb-sql-read-20261021/`: frozen before and
after source manifests, binary hashes, raw benchmark/profile/test output, the
failed gate, timing diagnostic, final test overlay, successful gate, and
`assessment.json`. The baseline is local candidate
`3ae270052df8a3a347f811599a24f677d0bf8b00` plus the previously frozen lifecycle
test/gate overlay. The after snapshot adds this SQL change and its regression
test; final verification adds two explicitly hashed test-harness overlays.
These are dirty source snapshots, not newly committed clean candidates.

Early paired timing logs were excluded because supplemental pprof analysis
overlapped them. Only the later clean paired runs supply the tables above.

No SQLite/DuckDB/PostgreSQL comparison, Linux execution, 24-hour canary,
multi-tenant load, pgwire performance, full release gate, or stability promotion
was performed in this iteration. No commit or push was made.
