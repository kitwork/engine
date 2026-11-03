# Bounded Query-local Read Buffers, 2026-10-24

## Decision

Retain the bounded point-reader implementation for local development as an
**allocation improvement**, not as a completed checkpoint-tail optimization.
Windows fixed-work read allocation drops 67.51% and mixed allocation 63.80%
(medians of paired changes). Mixed checkpoint service p99 increases 5.71% by
that same paired method, with five of six pairs slower. This tradeoff remains
open. No production/stable qualification, commit or push is made.

The [preceding attribution](CHECKPOINT_ATTRIBUTION_2026-10-24.md) found page-read
allocation pressure. Lower allocation did not guarantee better service tails;
the evidence below keeps those outcomes separate.

## Implementation

- A snapshot can create a `PointReader`, owning one private page buffer of at
  most 128 KiB and 128 decoded row positions. It opens no new file and does not
  pin another generation; the caller continues to own the snapshot lifetime.
- Relational secondary-index walks create one reader per walk and close it on
  every return path. Primary/unique point paths retain their ordinary lookup
  behavior. Transaction overlay/dependency handling and index visit order stay
  unchanged; this is not a new batching or sorting algorithm.
- Shared-cache hits still borrow immutable cached pages. The first cacheable
  miss retains ordinary cache admission. Later small misses reuse the private
  buffer without publishing it into the shared cache. Repeated working sets can
  therefore warm the cache rather than remain permanently cold.
- Oversized pages retain the ordinary read path. The 128 KiB limit bounds the
  private retained buffer, **not all query/result or oversized-page memory**.
  Page-cache budget remains 256 KiB per database; private scratch is not charged
  as shared-cache residency. There is no process-global pool or cross-query reuse.
- Page reads still validate checksums and directories. Returned values are
  copied, so later reads, caller mutation and Close cannot change earlier
  results. Closing a reader drops its buffers; closing its snapshot invalidates
  reads. Reader access/Close and snapshot lifetime are synchronized.

The storage format, Sync policy, maintenance worker count, transaction semantics
and existing structural limits are unchanged. No GC policy was tuned.

## Experiments Kept

1. The 64 KiB prototype excluded most normal pages: writers check their 64 KiB
   target before appending the next record, so a completed page may be larger.
   Allocation did not improve materially. The run was deliberately interrupted;
   its failed execution status is not an independently observed DB failure.
2. The first 128 KiB implementation reused private buffers on every cache miss.
   It reduced mixed allocation, but `large/hot_index` allocation rose 108.89%
   and its short-window paired timing rose 21.65%. It passed correctness tests
   but was rejected because it prevented cache warming.
3. The retained implementation adds first-cacheable-miss admission and kernel/
   SQL tests requiring warm-cache convergence. It is measured again against the
   original diagnostic baseline, not against the rejected prototype.

The first SQL lifetime test also initially self-blocked by retaining a facade
transaction and requesting its exclusive facade checkpoint. The test now calls
the snapshot-safe kernel checkpoint for that deliberately controlled condition.
No production lock was weakened to pass the test. Development notes retain this
test-only correction and the early Begin/Commit signature errors.

## Fixed-work Windows A/B

Go 1.26.0 / Windows amd64, Intel i7-11850H, GOMAXPROCS=4, GOGC=100, Go memory
limit MaxInt64. Sixteen disposable databases, 256 KiB cache each, two maintenance
workers. Modes have the same seeded fixture, statements, Sync/cadence and exact
result/reopen oracles as the preceding diagnostic campaign:

| Mode | Commits | Point queries | Index queries | Checkpoints |
| --- | ---: | ---: | ---: | ---: |
| Write | 1,024 | 0 | 0 | 256 |
| Read | 0 | 3,072 | 3,072 | 0 |
| Mixed | 1,024 | 3,072 | 3,072 | 256 |

Six fresh-process pairs per mode, alternating A/B order and rotating mode order;
one warm-up pair per mode is excluded. Setup, validation, close/reopen and the
pre-workload GC remain outside workload timing. No concurrent build/test/profile
or inventory job is launched during timed measurements. OS scheduling, frequency,
background activity and cold filesystem cache are not controlled laboratory axes.

| Mode | Median wall before/after ms | Median allocated before/after MiB | Paired wall change | Paired allocation change |
| --- | ---: | ---: | ---: | ---: |
| Write | 682.14 / 660.47 | 132.07 / 130.97 | -2.23% | -0.83% |
| Read | 534.60 / 394.70 | 2,397.92 / 779.05 | -26.19% | -67.51% |
| Mixed | 1,244.69 / 1,137.19 | 2,546.27 / 921.84 | -5.18% | -63.80% |

Allocated MiB means cumulative TotalAlloc, **not resident/retained RAM**. Private
buffers can change live-memory/cache behavior; no RSS reduction is claimed.
Median GC counts: write 17 -> 20, read 192.5 -> 81, mixed 214 -> 100. The write
mode does not reduce every memory/runtime counter; it is not the target win.

Percent changes are medians of `after/before - 1` within each pair, not ratios
of the two side medians. Checkpoint percentiles are nearest-rank per process,
not pooled fleet percentiles. In particular, lower side medians must not hide
the paired service-p99 regression:

| Mixed pair | Service p99 before ms | After ms | Change |
| --- | ---: | ---: | ---: |
| 1 | 40.78 | 45.86 | +12.45% |
| 2 | 49.01 | 52.32 | +6.75% |
| 3 | 57.59 | 41.32 | -28.24% |
| 4 | 49.61 | 50.16 | +1.11% |
| 5 | 61.37 | 65.23 | +6.30% |
| 6 | 56.27 | 59.15 | +5.11% |

Mixed median paired service p95 improves 9.92%, service p99 worsens 5.71%, and
request p99 improves 2.91%. Do not describe the tail as cleared or infer a
particular GC/I/O cause without new matched traces of the retained candidate.

## Query Controls

The identical benchmark harness is compiled into both binaries. Three process
pairs use 2,048 fixed iterations per case after one excluded warm-up pair.

| Case | Median bytes/query before | After | Allocations/query before/after | Median paired time change |
| --- | ---: | ---: | ---: | ---: |
| Small hot index | 40,881 | 40,974 | 265 / 266 | +12.55% |
| Large hot index | 71,634 | 71,725 | 433 / 434 | -0.69% |
| Historical-generation SQL index | 258,056 | 258,168 | 525 / 526 | -1.54% |

The permanent cold-cache allocation regression is gone. Hot index walks pay one
small reader allocation; ordinary point lookup allocation counts remain unchanged.
Do not silently discard the small-hot timing increase in this first control.

An explicit follow-up, planned after this result, kept the **same binaries/code**, increased the focused
small query controls to 8,192 iterations and six pairs, and added two A/A pairs
using the before binary twice. Small-hot index median paired time then changed
-4.10% (individual changes -10.18% to +4.69%); point control changed -4.55%.
The A/A small-hot changes were +21.31% and -0.44%; point A/A +10.33% and -2.62%.
This fails to establish a reproducible small-hot slowdown in this follow-up, but
does **not** prove timing equivalence or universal speedup. All initial and
follow-up samples remain in evidence. Byte/allocator counts are the stronger
signal here; the extra reader allocation remains visible.

## Correctness And Provenance

- All 42 fixed-work reports pass exact operation counts, acknowledged result
  checks, 16 database reopen checks, and manager drain checks: Closed=true;
  no open/opening/closing handles, active leases, waiters, reserved cache bytes,
  queued/running maintenance, or recorded maintenance/open/close failure.
- Full `./kitdb/... ./cmd/releasegate`: 930 passing root test executions,
  19 root skips, 14 passing packages.
- Targeted `-race`, three repetitions: 201 passing root executions in two
  packages. Profile-tag controls: 12 root executions in two packages.
- Baseline common SQL controls pass too. Tests cover old snapshots, tombstones,
  read-your-writes/conflicts, cancellation, caller-owned values, checksum retry,
  oversized-page fallback, page-target overshoot, cache warming, private buffer
  bounds, concurrent Get/Close and allocation reuse.
- `go vet ./kitdb/... ./cmd/releasegate`, `go build ./...`, and scoped whitespace
  checks pass. These are not the whole shared HEAD's tests or a new crash/
  physical-power-loss qualification.

Frozen inventories contain 11,997 before files and 11,999 after files. Exactly
five paths differ: four production files (`point_reader.go`, `rowstore.go`,
`relational/index.go`, `relational/transaction.go`) and the new kernel test.
The SQL controls and benchmark harness are identical. The base is the preceding
diagnostic source (commit 4d36264c plus its documented overlays), not the current
shared HEAD; unrelated changes are not included or qualified. Shared scoped
source hashes match the measured after copy.

Local evidence roots:

- `.artifacts/kitdb-point-reader-20261024/`: interrupted 64 KiB prototype.
- `.artifacts/kitdb-point-reader-20261024-r2/`: rejected no-warming prototype,
  full test results and explicit rejection decision.
- `.artifacts/kitdb-point-reader-20261024-r3/`: retained source pair, 56-step
  campaign, 18 focused-control processes, inventories, assessments and final
  evidence index. Run scripts preserve exact commands and refuse existing
  report paths where appropriate; use a fresh output directory to reproduce.

This is Windows-local evidence only. No Linux, pgwire end-to-end, desktop/mobile,
24-hour soak or production traffic was tested. No background server, keep-awake
request, new dependency, commit or push was added. The next latency investigation
must account for the paired p99 tradeoff rather than assume allocation reduction
has solved it.
