# Windows Checkpoint Attribution, 2026-10-24

## Decision

Investigate **read-page allocation and primary-row hydration** next, with bounded
query-owned scratch or ordered/batched probes. Do not reduce durability, enlarge
the cache budget, disable GC, or add maintenance workers to hide this workload's
pressure. This campaign adds diagnostic coverage, not another production storage
optimization or a new release qualification.

The earlier WAL writer handoff remains in the measured candidate. Its mixed-work
service tail had not improved reliably. This follow-up explains which components
deserve another controlled experiment; it does not retroactively attribute every
slow checkpoint in the earlier campaign.

## Method

Local Windows, Go 1.26.0, GOMAXPROCS=4, offline dependencies, disposable databases.
Sixteen databases retain 256 KiB of page-cache budget each, two maintenance
workers, the existing checkpoint cadence and Sync policy. GOGC stayed at 100 and
the Go memory limit at MaxInt64. One warm-up process per mode is excluded; three
fresh processes per mode are summarized. Mode order rotates between rounds.

| Mode | ACKed commits | Point queries | Index queries | Checkpoints |
| --- | ---: | ---: | ---: | ---: |
| Write/checkpoint only | 1,024 | 0 | 0 | 256 |
| Read only | 0 | 3,072 | 3,072 | 0 |
| Mixed | 1,024 | 3,072 | 3,072 | 256 |

The read statements are point lookups and `WHERE bucket=$1 ORDER BY id LIMIT 16`
with payload projection. Each mode uses the same seeded fixture and independent
result/reopen oracles. The modes remove work, rather than normalize throughput;
their elapsed times must not be subtracted as a causal decomposition. Mixed
execution changes cache contents, overlap and scheduling.

Setup, the pre-workload GC, profile startup, profile flush, final verification and
close/reopen stay outside the workload timer. Trace, allocation and CPU profiles
each use a separate process, not a normal timing sample. Allocation sampling uses
the default sampling rate and before/after heap-profile subtraction. Final GC
for the allocation profile is outside workload timing. No GC tuning was applied.

## Unprofiled Results

Medians of three processes. Checkpoint percentiles are nearest-rank within each
process, then their median is reported; they are not pooled percentiles.

| Mode | Wall ms | Total allocated MiB | GC cycles | GC pause ms | Service p95 ms | Service p99 ms | Request p99 ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Write/checkpoint only | 811.63 | 132.07 | 16 | 2.12 | 10.28 | 15.15 | 64.62 |
| Read only | 523.47 | 2,397.90 | 193 | 24.04 | n/a | n/a | n/a |
| Mixed | 1,284.82 | 2,546.82 | 214 | 29.35 | 33.20 | 55.08 | 232.21 |

Three-round wall ranges: write 706.65-813.72 ms, read 519.18-575.77 ms, mixed
1,238.19-1,421.23 ms. Mixed service p99 ranges from 49.45 to 59.45 ms; write from
12.16 to 21.64 ms. These short runs do not establish production tail percentiles.

`TotalAlloc` is cumulative allocation, **not resident memory or a leak**. End
heap samples across these normal rounds are roughly 11.45-16.66 MiB and are not
post-GC retained-heap measurements. GC pause time is not all GC work; concurrent
marking and allocation assists are separate. None of these counters alone proves
that GC caused a particular slow checkpoint.

## Allocation Attribution

Separate sampled `alloc_space` profiles identify `mainImage.readPageInto` as:

| Profile | Flat allocation share | Cumulative share |
| --- | ---: | ---: |
| Write/checkpoint only | 14.97% | 23.04% |
| Read only | 85.43% | 91.16% |
| Mixed | 81.64% | 86.79% |

The read path alone reproduces most allocation volume. In the read profile,
`Transaction.readMatchingRows` accounts for 95.89% cumulatively; mutation-page
decoding contributes another 5.71% flat. Profile percentages are sampled and come
from different processes than the normal TotalAlloc counters.

Source inspection explains a plausible mechanism: normal main-page misses call
`readPage`, which creates a fresh `rowPage`; `readPageInto` allocates a buffer when
that page has insufficient capacity. The latter already supports reuse when a
private page is supplied. Scattered index-to-primary lookups under a small cache
can repeatedly miss. **Exact per-query miss/reload attribution remains to be
measured**; the profile does not prove every allocation is an avoidable miss.

Do not recycle evicted shared-cache pages without ownership protection. Readers
may still hold them after the cache lock is released. Any proposed scratch must
be query/snapshot-owned, bounded, preserve returned-value ownership, validate
checksums normally, and never be inserted into the shared cache while mutable.

## Checkpoint Trace

The `kitdb_profile` build logs a diagnostic identity `(fixture path, transaction)`
inside each checkpoint execution. The reducer streams Go trace headers with
size/event ceilings instead of retaining the enormous full text/stack dump.
The parser matches **256 write and 256 mixed checkpoint identities** to their
reports, uses the transition's target GoID rather than its emitting goroutine,
and checks exact state coverage of every complete execution region.

Summed execution-state intervals across both workers, **not wall time**:

| State | Write ms | Mixed ms |
| --- | ---: | ---: |
| Syscall | 1,283.92 | 1,215.30 |
| Runnable | 42.63 | 864.11 |
| Running | 58.42 | 64.22 |
| Waiting:sync | 0.20 | 422.37 |
| Waiting:GC mark assist wait for work | 0 | 1.73 |
| Waiting:preempted | 0.06 | 0.05 |

In this trace, the mixed service tail is not simply a longer I/O syscall. For
example, database 10 / transaction 18 has 58.11 ms coarse service and 58.73 ms
trace execution: 33.71 ms Runnable, 20.97 ms Waiting:sync, 3.72 ms Syscall and
0.32 ms Running. Its 15.96 ms `storage.sync` region contains only 0.81 ms in
Syscall and 15.14 ms Runnable. A region named Sync measures elapsed time around
the call, not device latency.

That checkpoint overlaps 2.55 ms of stop-the-world GC and 25.74 ms of a worker
allocation-assist range. These overlaps are **not additive** to state totals;
an assist range includes waits and scheduling time, not just GC CPU time.
Concurrent-GC overlap is also not proof of causation. Trace timestamps and coarse
manager timing have different boundaries/clocks and may differ by sub-millisecond
amounts; do not force their difference into an invented latency component.

Trace-derived synchronization stacks focused on `CheckpointContext` identify
approximately 305.67 ms at `sync.RWMutex.Lock` and 112.79 ms at
`runtime.gcMarkDone` across the whole run. The lock sites include generation
installation and WAL handoff (`db.mu`); stacks identify the waiting sites, not
the identities or work of all lock holders. Initial `commitMu` acquisition is
only about 0.002 ms for the three slowest mixed samples. The broad sync profile
also includes idle commit-worker receives and maintenance-ticket waits; its
grand total is not checkpoint lock contention.

One read-only GC range crosses trace shutdown. The parser initially rejected
this valid partial range; it now clips to the observed trace interval and labels
the range partial. The original failed analysis and its log remain. Synthetic
tests cover this boundary, target-GoID state partition, discontinuous states and
invalid checkpoint identities. The read-only trace has zero checkpoint regions.

## Verification And Scope

- All 21 reports (warm-up, normal and profiles) pass work-count, result and
  close/reopen checks. All 16 databases reopen correctly per report.
- Manager after-close checks: Closed=true, no open/opening/closing databases,
  leases, waiters, queued/running maintenance or reserved page-cache bytes;
  no maintenance failure, cancellation, rejection or open/close error.
- Full `./kitdb/... ./cmd/releasegate` suite: 920 passing root test executions,
  19 root skips, 14 passing packages. Opt-in campaigns remain skipped here.
- Targeted race controls: 27 passing root executions across two packages.
- Profile-tag controls: nine passing root executions across two packages.
- Offline parser tests, `go vet ./kitdb/... ./cmd/releasegate` and
  `go build ./...` pass. The default binary has no `CheckpointResult` symbol;
  the profile build does. Its default implementation is an empty inlineable hook.

The frozen source has 11,996 inventory-hashed files: the preceding WAL-rotation
candidate plus exactly five diagnostic/harness files. It is not the entire
current shared HEAD. Shared overlay hashes match the measured copy. Existing
unrelated changes are not qualified by this campaign.

Local evidence is under `.artifacts/kitdb-tail-attribution-20261024/`:
`source.json`, `sources.json`, `execution.json`, `analysis-execution-r2.json`,
`assessment-final.json`, normal reports, raw traces/profiles and derived stacks.
`final-index.json` hashes the retained evidence. `assessment.json` is superseded
because PowerShell member enumeration encoded the file count as an array;
`assessment-final.json` contains the scalar count and unchanged measurements.

This is Windows-local diagnostic evidence, not Linux verification, an end-to-end
pgwire comparison, a new crash/power-loss qualification, a 24-hour soak, or a
stable-release claim. No commit or push was made.

## Next Controlled Change

Prototype bounded private read scratch or grouped primary hydration, with
separate point/index-query allocation counters. Keep budgets, fixture, worker
count, Sync and SQL results identical. Require snapshot/ownership, checksum,
cancellation and race tests before comparing fresh normal A/B pairs. Accept only
measured reductions without correctness or memory-bound regressions; remeasure
checkpoint tails rather than assuming lower allocation guarantees them.
