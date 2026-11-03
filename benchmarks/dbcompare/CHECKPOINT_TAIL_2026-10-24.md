# Windows checkpoint-tail follow-up

Campaign: 2026-10-24; report finalized 2026-10-25 (Asia/Saigon).

## Decision

Keep the previously retained query-local point-reader candidate for local
evaluation. This investigation makes **no additional production-code or runtime
instrumentation change**. Allocation savings repeat, but checkpoint-tail latency
is not qualified. Neither a consistent p99 regression nor a reliable p99 fix is
established. Do not call this candidate stable or production-ready.

The earlier [point-reader report](PRIVATE_READ_BUFFER_2026-10-24.md) remains valid
historical evidence: mixed service p99 increased by a median paired 5.71%, with
five of six pairs slower. The new measurements below do not replace that batch.

## Source and work

Evidence is under `.artifacts/kitdb-checkpoint-tail-20261024/` in the engine repo.
`source.json`, `sources-before.json`, `sources-after.json`, `assessment.json`,
`decision.json` and `final-index.json` describe provenance, checks and limits.

The campaign reuses the R3 frozen before/after trees, based on commit
`4d36264cbd102a162f1b54908df29f53b422080e` with the overlays documented in
`.artifacts/kitdb-point-reader-20261024-r3/source.json`. This is not a measurement
of the entire current shared worktree or HEAD. Full inventory counts are 11,997
and 11,999 files; exactly five paths differ, including one new kernel test.
The shared SQL control/benchmark file is identical. Inventories before and after
analysis match byte-for-byte. Normal binaries match the prior recorded hashes;
profile-tag binaries are built separately from the same frozen source trees.

Windows/amd64, Go 1.26.0, GOMAXPROCS=4, GOGC=100, maximum Go memory limit.
Every process uses the same disposable mixed-work fixture: 16 databases, 1,024
acknowledged commits, 3,072 point reads, 3,072 index reads and 256 checkpoints.
Page-cache budget stays 256 KiB per database / 4 MiB total; maintenance stays at
two workers. Sync and publication policy are unchanged.

Normal and trace processes are separate. Build, inventory and parsing do not
overlap timed workloads. Alternating before/after execution order is recorded.
There are nine normal pairs (the first excluded as warmup), three trace pairs,
and four later A/A pairs using the same before-normal binary twice per pair.

This exercises native relational SQL plus kernel/node maintenance. It does not
exercise pgwire, HTTP, a VM, real application data or another database engine.

## Normal measurements

Each side column is the median of eight processes. Change is the median of
eight paired percentage changes, not the ratio of those side medians. Allocated
MiB is total allocation during the workload, not resident memory.

| Metric | Before median | Candidate median | Median paired change |
| --- | ---: | ---: | ---: |
| Workload elapsed, ms | 1,092.51 | 972.66 | -11.38% |
| Allocated, MiB | 2,547.07 | 922.38 | -63.78% |
| GC cycles | 215.5 | 100.5 | -53.49% |
| GC pause, ms | 26.00 | 17.37 | -32.05% |
| Checkpoint service p95, ms | 28.81 | 25.62 | -12.09% |
| Checkpoint service p99, ms | 46.37 | 42.12 | -9.65% |
| Checkpoint service maximum, ms | 62.20 | 60.22 | -3.45% |
| Maintenance queue p99, ms | 164.16 | 135.37 | -6.64% |
| Checkpoint request p99, ms | 193.22 | 162.73 | -8.20% |

Service p99 improved in five pairs and worsened in three:

| Pair | Before, ms | Candidate, ms | Change |
| --- | ---: | ---: | ---: |
| 1 | 41.5714 | 64.5954 | +55.38% |
| 2 | 38.1169 | 41.2808 | +8.30% |
| 3 | 41.8596 | 45.8918 | +9.63% |
| 4 | 50.1982 | 46.4631 | -7.44% |
| 5 | 48.7295 | 42.9540 | -11.85% |
| 6 | 56.3116 | 41.1569 | -26.91% |
| 7 | 47.3157 | 40.9095 | -13.54% |
| 8 | 45.4189 | 39.1307 | -13.84% |

The A/A service-p99 changes were +1.09%, +0.20%, -17.86% and +27.46%
(median +0.64%). This demonstrates variability on the same binary and fixture.
Four A/A pairs do not establish statistical equivalence, explain every outlier,
or prove that the implementation has no tail-latency effect. No batches were
discarded or pooled into a favorable release claim.

## Trace attribution

All 1,536 checkpoint identities match their database/transaction workload
records. All execution intervals have exact state coverage. There are 768
matched before/after logical boundaries across three trace pairs, **not 768
unprofiled latency-effect measurements**.

The reducer streams `go tool trace -d=parsed` and retains selected
`TransitionStack` records for the goroutine entering a synchronous wait during
an active checkpoint. It does not attribute the generic emitter stack to the
waiter. Limits: two minutes, 8 GiB input text, 1 MiB per line, one million event
headers, 10,000 selected waits, 64 lines/stack, 1,024 characters/line and 16 MiB
selected stack text. All six traces have zero missing or truncated wait stacks.

The wait parser closes waits using the same target goroutine's next transition,
clips them to the checkpoint and asserts that their sum equals the correlated
`Waiting:sync` time exactly. GC mark completion is classified before a deeper
mutex frame. GC waits in a `storagetrace`/`runtime.trace` hook are separate from
ordinary GC waits. Small unclassified waits remain explicitly reported.

The following are sums across checkpoint executions and two workers, in ms.
They are not elapsed wall time. Running, preemption and GC-assist-work states
are retained in the machine-readable assessment but omitted from this table.

| Trace | Runnable | Syscall | Sync wait | Main-install write lock | WAL-rotation write lock | GC mark done | GC in trace hook |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 before | 601.19 | 1,128.84 | 535.64 | 280.20 | 123.93 | 72.15 | 44.62 |
| 1 candidate | 576.61 | 1,139.60 | 270.01 | 180.05 | 69.55 | 6.64 | 13.20 |
| 2 before | 616.22 | 1,099.62 | 440.66 | 245.31 | 74.62 | 40.18 | 74.83 |
| 2 candidate | 552.07 | 1,091.47 | 203.70 | 141.37 | 56.49 | 5.61 | 0.00 |
| 3 before | 655.92 | 1,072.80 | 389.66 | 212.14 | 73.83 | 39.14 | 60.16 |
| 3 candidate | 531.35 | 1,108.36 | 240.86 | 168.11 | 43.21 | 0.00 | 29.47 |

The lock and GC columns are subsets of sync wait, not additional time. Page
cache and unclassified sync waits account for the remainder. Nested phases,
STW overlaps and worker GC-assist overlaps are non-additive. Partial GC ranges
at trace boundaries are labeled and clipped, not treated as complete events.

Among each candidate trace's 13 slowest service checkpoints, summed Runnable
time is 286.79-307.14 ms, sync wait 96.75-170.63 ms, and syscall time
44.54-47.50 ms. This is a description of the selected profiled tails, not a
claim about the distribution of unprofiled execution or physical CPU time.

For example, trace 1 candidate, database 9 / transaction 22 has 80.1181 ms
coarse service and an 80.191225 ms trace execution interval. The latter contains
62.987107 ms Runnable, 3.742105 ms Syscall, 0.263916 ms Running and 13.198097 ms
sync wait. That wait's stack is `runtime.gcMarkDone` through
`runtime/trace.StartRegion` and `storagetrace.Start` in `rotateWALLocked`.
The profiler itself can therefore contribute to a measured tail. It would be
incorrect to call this entire sample disk latency or optimize production to
remove a debug-only trace effect. The two clocks need not produce identical
service and region durations.

## What this establishes

- Allocation savings repeat at roughly 64% for this mixed workload.
- Candidate aggregate checkpoint write-lock wait is lower in all three traces;
  this does not identify the cause of each unprofiled p99 outlier.
- Wait sites include `installAppendedMain` and WAL rotation. Waiter stacks do
  **not** identify the lock holder or its critical-section work.
- Aggregate syscall-state time is broadly similar across the trace pairs. It
  is not a pure disk/Sync measurement and does not justify weakening durability.
- Runnable delay and GC, including observer overhead, matter in these traces.
  None establishes a universal scheduler or lock bottleneck from this fixture.

The next evidence-driven investigation is bounded **lock-holder duration and
source attribution**, particularly around snapshot capture and publication.
Snapshot capture is a source-based hypothesis, not a proven holder attribution.
Do not move I/O, weaken locking, increase workers/cache, or change Sync merely
because a writer's wait stack contains one of these functions.

## Verification and limits

All 32 workload reports pass exact work/oracle/reopen checks: 256 unique
checkpoints and 16 verified reopens per process. Managers close; leases, open/
opening/closing databases, waiting acquisitions, cache reservations and
maintenance queues drain; no open, maintenance, cancellation, rejection or
close errors are reported. Policy checks pass in every report.

Fresh profile controls pass on both frozen sides: nine root test executions
and two packages each, 18 roots total. Offline correlation/wait-site tests pass
three root tests, including five synthetic wait classifications. The previous
930 full-suite root passes and 201 targeted race root passes belong to the
unchanged candidate's earlier campaign; they were **not rerun here**.

The 24 workload steps, eight A/A steps and corrected 65-step analysis pipeline
pass. An initial approval timeout wrote nothing. The first analysis attempt
failed because PowerShell dynamically shadowed a trace basename with a step
name. Its failed log is retained; the runner was corrected and offline analysis
rerun, without rerunning or changing workload evidence. This is a tooling
failure, not a database failure. A console-only OrderedDictionary projection
printed null fields after assessment; saved assessment data was verified and
is intact.

No Linux run, new crash campaign, 24-hour soak, pgwire qualification, production
rollout, keep-awake request, commit or push occurred. Existing unrelated working
tree changes are untouched. Tail qualification remains open.
