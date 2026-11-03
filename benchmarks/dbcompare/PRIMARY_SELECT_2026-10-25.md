# Primary SELECT fast-path experiment, 2026-10-25

## Decision

**Not adopted in the default runtime.** A bounded optimistic primary-key read
is promising in isolation, but this prototype charges unrelated equality
queries for an unsuccessful attempt before taking their ordinary snapshot.
The helper and its tests remain replayable under `.artifacts`, not in the
default executor. The three-line dispatch hook has been removed; the previous
projection and point-reader work is unchanged.

This is Windows development evidence, not a stable release, a general SQL
speedup, or a new snapshot/transaction contract.

## What was tried

The autocommit `Engine.Execute` / `ExecutePlan` SELECT branch tried one direct
read for a deliberately narrow shape: one table, one non-null equality on its
single primary field, and ordinary column projections. It rejected aliases,
qualified references, expressions, aggregates, joins, DISTINCT, ordering,
limits/offsets, derived tables, CTEs, search and compound queries. EXPLAIN and
explicit transactions continued through their normal snapshot executor.

The attempt held the ordinary engine lifetime read lock and obtained a normal
`ReserveSnapshot` reservation, preserving bounded FIFO/count/memory admission.
It did not consume the reservation into a snapshot or open another main-file
handle. It read the catalog, physical row generation and caller-owned row
bytes through the existing kernel methods. A final `LastTransaction` equal to
the captured catalog transaction proved that no commit had crossed those
reads. A mismatch discarded the result and fell back once to a snapshot;
there was no retry loop. Binding/decoder errors also fell back to preserve
the snapshot executor's diagnostics. Reservation ownership ended before the
fallback began.

The existing binders, projected KROW decoder, residual predicate and projector
were reused. No storage format, WAL/Sync rule, cache policy, worker count,
kernel public API, handle pool or generation refcount changed. `DB.Get` holds
`db.mu.RLock` over its main-file read; avoiding file-open time is therefore a
trade for a different I/O lock interval, not a lock-free read.

Role-authorized reads still capture roles, grants, schema and data in their
existing transaction. Streaming and wire-adapter paths were not extended by
this prototype. These measurements cannot be claimed as a pgwire/KitSQL or
permission-path optimization.

## Source and method

Evidence: `.artifacts/kitdb-primary-select-20261025/`.

- Base: the previously frozen `kitdb-lock-holder-20261025/source` corpus,
  all 12,006 files hash-checked before building and again at finalization.
- Five captured overlays: candidate `relational/engine.go`, prototype helper,
  its tests, the retained portable snapshot-reader tests, and the existing
  QPC profile-clock correction.
- Before and after share helper/tests and instrumentation. Their only runtime
  source difference is the SELECT dispatch hook; the before executor does
  not call the helper. Neither includes the rejected Windows ReOpenFile work.
- Both normal test binaries were hashed before timing and rechecked afterward.
  Full-suite child Go builds inherited the candidate overlay.
- Windows/amd64, Go 1.26.0, GOMAXPROCS=4, GOGC=100, no Go memory limit.
- Alternating side order, one warmup pair excluded per mode; eight measured
  mixed pairs, three read pairs and three write pairs. All 34 process reports,
  including warmups, were retained. No timing pair was discarded or rerun.
- Each mixed process used 16 disposable DBs, 1,024 acknowledged commits,
  3,072 primary reads, 3,072 index reads and 256 checkpoints. Read-only mode
  retained the reads; write-only retained commits/checkpoints.
- Cache: 256 KiB per DB / 4 MiB total; two maintenance workers. Setup,
  validation, close/reopen and report output stayed outside workload timing.
  Profiling and tracing were off for all timed workloads.
- Every report verified all 16 reopens and a closed manager with no remaining
  leases, queue/running maintenance, open/warm/idle handles, reserved cache,
  or recorded open/maintenance/close failure.

## Fixed-work results

Changes below are medians of paired percentages, **not** percentages calculated
from the two side medians. Negative means less time/allocation.

| Metric | Before side median | Prototype side median | Paired change |
|---|---:|---:|---:|
| Mixed wall | 1,384.692 ms | 1,334.386 ms | -2.31% |
| Mixed allocated | 922.501 MiB | 913.507 MiB | -0.95% |
| Mixed checkpoint service p95 | 39.791 ms | 34.082 ms | -10.97% |
| Mixed checkpoint service p99 | 61.654 ms | 65.073 ms | +0.36% |
| Mixed checkpoint queue p99 | 236.986 ms | 214.605 ms | -6.11% |
| Mixed checkpoint request p99 | 281.566 ms | 247.097 ms | -9.99% |
| Read-mode wall | 491.801 ms | 439.180 ms | -5.30% |
| Read-mode allocated | 779.042 MiB | 774.517 MiB | -0.58% |
| Write-mode wall control | 795.193 ms | 876.466 ms | +5.79% |

Mixed wall improved in five of eight pairs; service p99 improved in four of
eight. The unchanged write-only execution path also varied, so small mixed
wall changes are not strong adoption evidence. Service/queue intervals use
the existing node clock, while total request/workload intervals use QPC.
These are short local batches, not a significance or tail-latency guarantee.
The read mode includes index work, not only isolated primary reads.

## Isolated primary and fallback cost

A separate common benchmark overlay ran only after the main campaign.
Each side executed 1,000 calls per benchmark against 128 checkpointed rows,
with one warmup pair and five measured alternating pairs. Setup was excluded;
parsing and result materialization remained inside each call. No concurrent
writer or checkpoint was present. The primary query repeatedly visited eight
keys; the fallback query returned 16 matching rows through a secondary index.

| Metric | Before side median | Prototype side median | Paired change |
|---|---:|---:|---:|
| Primary ns/op | 66,052 | 22,792 | -66.53% |
| Primary B/op | 17,718 | 16,146 | -8.87% |
| Primary allocs/op | 130 | 117 | -10.00% |
| Secondary fallback ns/op | 120,522 | 136,002 | +8.24% |
| Secondary fallback B/op | 72,559 | 79,177 | +9.12% |
| Secondary fallback allocs/op | 430 | 485 | +12.79% |

Primary duration improved in all five pairs. Fallback duration worsened in
three; its extra 55 allocations appeared in every pair. Outliers remain in
the raw reports. The failed attempt repeats catalog/schema/condition work
and obtains/releases admission before the unchanged snapshot fallback.
That allocation tax is sufficient reason not to enable this design globally,
even though the isolated primary result is useful evidence for further work.

## Correctness and gates

Six new prototype root tests covered snapshot-equivalent projections, aliases
of projected columns, misses, checkpointed/overlay values, fallback/error
shapes, missing parameters, commits after either physical read, drop/recreate
between reads, concurrent row updates/checkpoints, saturated admission,
cancellation and reservation release.

Candidate verification passed 939 root tests with 19 skips across 14 packages
(`./kitdb/... ./cmd/releasegate`), 54 root race executions over three repeats
across two packages, nine profile-control roots, vet and build. Before-side
controls also passed the six new test roots; helper tests call it directly
even when the production dispatch hook is absent.

After rejecting the prototype, default re-verification passed 933 root tests
with 19 skips across 14 packages, 36 root race executions over three repeats
across two packages, vet and build. `default-execution.json` and
`final-index.json` retain the results. Final auditing verified both binaries,
the 12,006 base files, five captures and 121 evidence files. Shared KitDB Go
sources matched the restored candidate after CRLF/LF normalization.

The frozen source is a scoped candidate, not a qualification of the entire
dirty shared checkout. One existing out-of-kernel difference was explicitly
recorded: the shared release planner's focused-race command names `./storage`,
where the frozen planner names `./utilities/sse`. Neither release planner was
invoked as a campaign here; the scoped commands above were executed directly.
No Linux run, long canary, physical power-loss test, commit, push or stability
promotion was performed.

An initial smoke test exposed that simple parsed equality carries both
`Conditions` and `Predicate`. The prototype was corrected to accept only a
bounded field/literal equality predicate and still bind/evaluate it. This
correction preceded freezing and all timed measurements.

## Next bounded step

Determine primary eligibility where catalog/schema binding can be reused by
the fallback, instead of doing speculative setup twice. First prove a shared
capture/binding boundary that preserves catalog/data consistency, reservation
ownership, cancellation and errors. Keep the same direct-read transaction
fence and the existing role/explicit-transaction paths. Then repeat both the
fixed-work campaign and the non-primary fallback control; do not promote on
the primary microbenchmark alone or add an unbounded schema/plan cache.
