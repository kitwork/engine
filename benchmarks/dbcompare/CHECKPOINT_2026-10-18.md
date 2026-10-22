# Checkpoint Phases And Ordered Existence Probes

Date: 2026-10-18. Windows/amd64, Go 1.26.0, local desktop evidence.
This follows the [mixed database campaign](MIXED_2026-10-18.md).
It does not qualify a clean release candidate or claim production stability.

## Decision

Keep the current storage architecture, durability and resource ceilings. Optimize
one measured bottleneck: incremental checkpoint's logical-record count.

Previously `countMergedRecords` visited overlay keys in map order and called
`main.get` for each key. A miss in the small shared LRU allocated and decoded a
whole page again; a successful lookup also copied a value that the count never
used. An existence query should not need an owned copy of the row payload.

The replacement sorts overlay-key references, examines segments newest first,
resolves each key only at its first put/tombstone, and reuses one private page
buffer on cache misses. It computes base count minus shadowed live records plus
live overlay records, rejecting overflow/underflow. The scratch page never
enters the shared LRU. Existing cached pages are read-only. Ordinary point reads
still populate the cache and return caller-owned copies.

Temporary memory is O(overlay keys) plus one decoded page, including legal large
single-record pages. There is no new pool, mutable global registry, increased
cache, snapshot charge exemption, worker or tenant-visible API. WAL/main format,
checksums, sync and atomic publication paths are unchanged.

## Measurement Boundaries

The synthetic SQL fixture, limits, GOMAXPROCS=4, paced closed-loop readers,
normal writers, unpaced 32-row noisy writer and exact reopen oracles are the
same as in the mixed campaign. Each case requests eight seconds, plus admitted
operation drain, on a fresh TempDir in a fresh process. Setup/reopen SQL are not
timed as workload. This is not pgwire, cold device cache or an open-loop SLA test.

Reports now split existing manager timestamps into scheduler wait and service
time. Service includes handle acquisition/release, lock admission, snapshot
drain, checkpoint computation, file operations and sync. These Windows
timestamps are coarse for short intervals; the enclosing timer remains QPC.

Separate diagnostic builds (`-tags=kitdb_profile`) add fixed-name runtime trace
regions for commit-lock wait, snapshot drain, checkpoint execution, record
counting, append/compaction, generation open, WAL rotation and sync calls.
Ordinary builds compile those hooks to no-ops. See
[phase diagnostic commands](../tracephases/README.md).

CPU, allocation and trace runs are separate from each other and from latency
runs. Profiling starts after fixture creation and ends after admitted-operation
drain, before verification/close/reopen. Allocation analysis subtracts a
pre-window cumulative profile from a post-window profile; setup allocations
must not be attributed to the workload. GC, background work and harness costs
during the window remain included. Allocation samples are statistical, unlike
the report's separate process `TotalAlloc` counter.

## Baseline Phase Evidence

The eight-second `noisy_16` trace observed 244 executions, including two no-op
checkpoints and 242 incremental publications. No full compaction/snapshot-drain
event occurred in this window; absence is not a measured zero-cost compaction.
Times below are traced wall durations, not pure CPU or device latency.

| Region | Count | Summed wall ms | p50 ms | p95 ms | p99 ms |
| --- | ---: | ---: | ---: | ---: | ---: |
| Commit-lock admission | 244 | 97.14 | 0.002 | 1.39 | 13.60 |
| Checkpoint execution | 244 | 16,268.81 | 38.22 | 221.85 | 343.14 |
| Logical-record count | 242 | 6,078.17 | 0.68 | 165.46 | 307.64 |
| Incremental append | 242 | 775.11 | 1.42 | 10.59 | 22.88 |
| Open next generation | 242 | 3,593.11 | 11.88 | 31.46 | 51.55 |
| WAL rotation | 242 | 4,543.18 | 15.28 | 38.56 | 54.00 |
| Generation sync calls within checkpoint | 484 | 449.26 | 0.47 | 3.35 | 9.75 |
| WAL-header sync calls within checkpoint | 242 | 403.49 | 0.85 | 6.38 | 12.16 |

Two workers overlap, so summed execution can exceed elapsed wall time. Child
regions overlap their parents; percentiles must never be added. Count occupied
37.4% of summed execution. Generation/WAL-header sync calls occupied about 5.2%;
the directory-sync wrapper added 1.07 ms total on this Windows platform.
Neither proves physical-device fsync latency, directory durability on every OS,
or safety against hardware that lies about sync. The combined end-to-end
checkpoint p99 bucket in the unprofiled baseline was <=2,000 ms, with scheduler
wait <=1,500 ms and service <=400 ms. Those are separate distributions, not a
decomposition of the same p99 operation.

The baseline allocation profile attributed 90.38% cumulatively to page
reading/decoding, and 12.83% to `countMergedRecords` (overlapping ownership, not
additive shares). The post-change diagnostic attributed about 17.71 MiB, 0.26%
of its sampled allocation, to record counting. Different window progress and
generation shapes prohibit treating those totals as a matched speed ratio.
Page reading/decoding still occupied 83.45% after the change, predominantly
outside this optimized checkpoint count.

Windows CPU samples contained substantial `runtime.cgocall`/native syscall
attribution. This does not establish that those samples are pure computation,
nor identify the disk/antivirus/scheduler component of each native call.

## Paired Count Microbenchmark

Both algorithms use the same 2,048-row, 512-byte-value fixture, eight immutable
segments, 1,024 overlay updates and 256 KiB page cache. The test-only reference
retains the previous point-lookup algorithm. Three samples, 20 iterations each:

| Algorithm | ns/op range | B/op range | allocs/op range |
| --- | ---: | ---: | ---: |
| Reference | 168,811,030-193,776,780 | 381,074,236-381,086,972 | 20,463-20,467 |
| Ordered scratch | 1,620,655-2,008,440 | 101,496 | 4 |

Allocation falls by about 99.97% for this deliberately cache-constrained count
fixture. This does not mean queries or the whole DB become 100x faster. The
microbenchmark used the host's default GOMAXPROCS=16, not the campaign's four,
and trace-analysis work overlapped it; its timings are diagnostic, not a clean
throughput gate. A separate allocation regression test caps count allocations
at 32 objects on a cache-disabled 2,048-row fixture, without a timing assertion.

## Full Workload Comparison

First sweep: all five baseline cases, then all five optimized cases. No profiler
or our build/test/trace-analysis process ran alongside these measurements.
The desktop was not reserved against unrelated OS/application load. p99 columns
are histogram bucket upper bounds, not exact percentiles. Allocation is total
process GiB over the workload/drain, not retained heap or a per-operation value.

| Case | Point/s before -> after | Index/s before -> after | Commit/s before -> after | Checkpoint p99 ms before -> after | Allocated GiB before -> after |
| --- | ---: | ---: | ---: | ---: | ---: |
| balanced_1 | 99.7 -> 87.8 | 99.6 -> 86.7 | 49.9 -> 44.3 | 50 -> 100 | 0.878 -> 0.742 |
| balanced_4 | 395.7 -> 344.8 | 394.6 -> 327.7 | 199.3 -> 178.1 | 100 -> 150 | 3.427 -> 2.768 |
| noisy_4 | 351.7 -> 328.5 | 345.7 -> 310.7 | 260.4 -> 206.8 | 150 -> 200 | 8.356 -> 3.972 |
| balanced_16 | 1,074.3 -> 857.7 | 894.7 -> 714.4 | 600.1 -> 502.9 | 2,000 -> 1,500 | 16.650 -> 10.010 |
| noisy_16 | 991.9 -> 855.0 | 822.9 -> 715.4 | 572.8 -> 500.6 | 2,000 -> 1,500 | 15.665 -> 9.964 |

**This sweep is not an overall throughput win.** Lower allocation totals partly
reflect fewer completed operations. Smaller-case tails regressed in this sweep;
do not omit them or blame a specific OS process without evidence.

Two additional pairs reversed order (optimized first, then baseline), each again
on fresh databases, with no profiler. The noisy 16-DB results were:

| Pair | Point/s before -> after | Index/s before -> after | Commit/s before -> after | End-to-end p99 ms | Service p99 ms | Allocated GiB before -> after |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Reverse 1 | 920.2 -> 985.0 | 789.4 -> 822.3 | 537.5 -> 560.4 | 2,000 -> 1,500 | 750 -> 250 | 13.619 -> 13.175 |
| Reverse 2 | 920.5 -> 962.5 | 781.8 -> 802.6 | 536.9 -> 565.2 | 2,000 -> 1,500 | 500 -> 250 | 13.305 -> 13.324 |

The balanced one-DB reverse pairs were near each other: point throughput
94.9 -> 95.2 and 93.9 -> 96.0/s; commit throughput 47.9 -> 47.6 and
46.6 -> 48.0/s. Checkpoint p99 was 100 -> 100 and 100 -> 50 ms. These
short runs demonstrate variability, not formal equivalence or statistical
significance. No extra reverse pairs were collected for four DBs or balanced
16 DBs. Read p99 and sustained 16-DB target throughput are not solved.

The supported conclusion is a much cheaper count primitive and a consistently
lower noisy-16 checkpoint p99 bucket in these three comparisons, with mixed
whole-workload throughput. Do not claim a general DB speedup or fully isolated
noisy neighbors. No compaction occurred in the 16-DB windows; one/four-DB cases
each observed two compactions per database, so full-compaction functional
coverage is separate from the 16-DB phase/performance result.

## Correctness And Lifecycle

All 23 captured workload reports (primary, reverse, baseline diagnostic and
profiles) passed the independent row/version/payload and every secondary-index
oracle after reopen: 69,367 acknowledged transactions in total. Each checked
one real commit sync per ACK, zero snapshot/admission/transaction/pending-commit
charges after workers drained, a closed manager, and zero managed handles,
leases and maintenance jobs. Goroutines returned 3 -> 3 in these windows.

OS process handle counts did not return to cold counts. These include runtime
and OS resources and are not classified here. Managed drain and successful
reopen must not be presented as full OS handle reclamation or long-term leak
proof. Heap/RSS sampling remains a process observation, not a hard ceiling.

Regression coverage includes cache-disabled and small-cache counts, repeated
put/delete/reinsert across 35 generations and compaction, independent old
lookup/model comparison, legacy files, checksum rejection and allocation budget.
Ordinary lookup copy/isolation behavior remains covered by the existing kernel
suite and snapshot tests. Tests and tagged fault matrices passed:

- Full `./kitdb`, `./kitdb/node`, `./kitdb/relational`: 42.222s, 23.209s, 127.539s.
- Focused concurrent checkpoint/snapshot/mixed tests under race, count=2.
- `kitdbfault` SQL checkpoint-publication crash and storage-I/O error matrices.
- Final checkpoint/count-allocation tests under race, count=2: 7.523s.
- Trace parser unit tests, focused package vet and `go build -mod=readonly ./...`.

The node package in the focused race invocation had no matching test; its full
ordinary suite ran, but this is not a full node race-suite result. Linux, the
full engine/UI/browser test suite, release qualification, 24-hour storage canary,
hardware power loss and SQL/pgwire load are not covered by this stage.

## Provenance And Reproduction

Head `e89bd9824396a2684c7669a029e94d9b526f1910`, dirty worktree containing earlier
KitDB optimizations and separate UI work. No commit, push or qualification
promotion. The measured binaries, not HEAD alone, identify the implementations:

| Binary | SHA-256 |
| --- | --- |
| `.artifacts/kitdb-checkpoint-20261018-r1/mixed.test.exe` (before) | `1941c4503c8727aafff213ff9ee6c825b57286dd2017053c84bd1bcf991ced53` |
| `.artifacts/kitdb-checkpoint-20261018-r2/mixed.test.exe` (after) | `c729bc74493344905888ec3aabb9452866951018a8e59346ea92b65467db14ba` |

Raw reports/logs/profiles are under those two artifact directories. Primary
files are `compare-<case>.json`; reverse pairs are `reverse-<pair>-<case>.json`.
`r2/summary.json` records all 23 validated report hashes and owner-drain evidence.
`r1/trace-phases-approved.json` is the completed baseline trace summary.
Incomplete/cancelled trace-parser outputs are not evidence. An initial native
PowerShell flag-parsing attempt never entered the workload; its log is retained.
Initial sandbox rename denials and a test-helper reopen cleanup bug were fixed
or rerun appropriately, not counted as storage failures. The final allocation
test was initially not launched because approval review hit its usage limit;
the later approved run completed after reset.

Reproduce the count comparison from `engine/`:

```powershell
go test ./kitdb '-run=^$' '-bench=^BenchmarkCheckpointCountProbes$' -benchmem -benchtime=20x -count=3
go test -race ./kitdb '-run=^TestCheckpointCount' -count=2
```

Use the [phase diagnostic instructions](../tracephases/README.md) for explicitly
windowed profiles. Use new artifact prefixes and no profile variables for
latency runs. Preserve every attempt instead of overwriting a previous result.

## Next Boundary

The [prefix-fenced read-path follow-up](READ_PATHS_2026-10-18.md) implements a
measured first step below without introducing a query-local cache. It also
records directory growth costs and the unrelated UI build blockers.

Target ordinary query page rereads/decoding under the existing small cache,
which still dominates allocation. Measure point/index traversal and catalog
metadata reads separately before considering a request-local reader strategy.
Any reuse must preserve immutable cached pages, exact snapshot visibility,
caller-owned results and admission charges. Generation open/WAL-rotation native
costs also deserve separate diagnosis; never remove sync or reader ownership
to hide them. Collect longer randomized/interleaved comparisons before claiming
an end-to-end speedup. Do not switch tree architecture or raise worker/cache
limits based on this bounded result.
