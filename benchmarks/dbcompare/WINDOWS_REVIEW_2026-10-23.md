# Windows Handle Attribution And Fixed-Work Checkpoints

## Decision

The observed handle increase is now explained more precisely than a total
process count: the additional Thread handles resolve to Go runtime start
functions, the same growing object classes appear without opening KitDB, and
File handles stay constant through repeated database lifetimes. No retained
fixture file handle or growing KitDB ownership counter was observed.

This is bounded evidence, **not an absolute no-leak guarantee**. One baseline
File handle cannot be resolved and remains unchanged. Earlier lifecycle review
flags and unexplained WAL-denial evidence are not rewritten or waived.

Fixed-count SQL work shows that the two-worker maintenance queue dominates
checkpoint request latency. The WAL fix has a small observed cost in this
workload; it is not a performance optimization. Keep its durability behavior.
No production code, concurrency default, storage format, public API or test
budget was changed to obtain these results. This is not stable qualification.

## Scope

Windows/amd64, Go 1.26.0, GOMAXPROCS=4. Both sources start from
`4d36264cbd102a162f1b54908df29f53b422080e` plus the same prior scoped overlay
and the same two new test files. A complete inventory and post-run revalidation
cover 11,988 before-source files and 11,990 after-source files. The only source
differences are the four previously reviewed WAL publication paths. SQL,
projection, dependencies, fixtures and test implementations are identical.

The changing shared HEAD, including newer queue/HTTP work, is **not** qualified
by this frozen-source campaign. Earlier WAL compatibility and fault/crash tests
remain in the [publication report](WAL_PUBLICATION_2026-10-22.md); this new suite
does not silently replace them. The [previous cost report](WAL_COST_2026-10-22.md)
retains its shorter, paced SQL measurements and open review flags.

## Handle Attribution

`lifecycle_handles_windows_test.go` adds a test-only current-process census.
It uses the documented [Process Snapshotting API](https://learn.microsoft.com/en-us/previous-versions/windows/desktop/proc_snap/overview-of-process-snapshotting)
and copies strings before freeing each walk marker/snapshot. The x64 structure
layout is checked. Every capture is limited to 16,384 handle entries.

PSS metadata is incomplete for some object types. The helper counts bounded
live [NtQueryObject](https://learn.microsoft.com/en-us/windows/win32/api/winternl/nf-winternl-ntqueryobject)
type queries separately and resolves disk-file paths only; it never queries
pipe object names, duplicates handles or inspects another process. These live
queries are not an atomic snapshot and do not prove object identity across
handle reuse. The helper remains test-only and Windows/amd64-specific.

The always-on control first captures a baseline, deliberately opens one file,
requires its fixture path and +1 File handle, closes it, then checks four more
captures for disappearance and restoration of the baseline File count. An
unreadable baseline file is retained as a measurement gap; a new gap fails the
test instead of being treated as proof of release.

The opt-in census runs six lifetimes in each database process, alternating
balanced/noisy native SQL over 16 databases for two seconds per lifetime.
Every lifetime checks ACK/Sync counts, released kernel/node counters and exact
row/index contents after reopen: **96 verified database lifetimes per side**.
Fixture files still exist on disk during captures; test cleanup removes them
only afterward. A final idle sample is taken after one second.

A separate runtime-only control starts 24 temporary `LockOSThread` workers per
round and joins them, without calling KitDB. It demonstrates the object-class
signature, not an identical I/O workload.

| Handle class | Initial, all processes | Before DB final | After DB final | Runtime-only final |
| --- | ---: | ---: | ---: | ---: |
| File | 21 | 21 | 21 | 21 |
| Thread | 7 | 23 | 23 | 32 |
| Event | 24 | 58 | 58 | 76 |
| IRTimer | 18 | 50 | 50 | 68 |
| WaitCompletionPacket | 14 | 30 | 30 | 39 |

All other captured object-class counts remain unchanged between initial/final
samples. The +16 Thread handles in each DB process resolve to
`runtime.tstart_stdcall`; the runtime-only process adds +25 handles with the
same start function. File counts stay 21 in **every** sample, no fixture file
path remains, all handles are type-classified, and the same one baseline File
path remains unreadable. The unresolved handle is not declared to belong to
Go or to KitDB. Thread-handle counts are not a current OS-thread count.

The observed Event/Timer/WaitCompletionPacket growth is consistent with the
thread-associated resources in Go 1.26's `semacreate`, `minit` and `mdestroy`.
That source allocates events, timers, wait-completion packets and a thread
handle, releasing them when its thread owner is destroyed. This supports the
interpretation, but does not map every anonymous object to a particular thread.
See the pinned [Go Windows runtime source](https://github.com/golang/go/blob/go1.26.0/src/runtime/os_windows.go).

## Fixed-Count SQL Work

`checkpoint_fixed_work_test.go` reuses the existing mixed-work fixture,
transaction writer, visible-row checker, manager cleanup and independent
reopen oracle. Each process has 16 databases, 1,024 rows/database and 512-byte
payloads. Per database, one writer performs 64 durable single-row commits with
a checkpoint after every four, while one independent reader performs 192
point queries and 192 index queries. There is no artificial pacing. Readers
can finish before the writers; this is not an indefinitely sustained load.

Every measured sample completes exactly:

- 1,024 ACK commits, each accounted to a WAL Sync;
- 3,072 point reads and 3,072 index reads;
- 256 advancing checkpoints at their exact expected transaction boundaries;
- 16 independently verified reopen operations and complete owner drain.

Both variants keep two maintenance workers, real durable I/O, query-result
caching off, 256 KiB page cache/database and 8 MiB snapshot/transaction budgets.
Retained history is off. Setup, close/reopen and verification are outside the
workload timer. One warmup pair is retained/excluded, followed by four A/B
pairs with alternating order. Builds, census and profiling do not overlap the
unprofiled timing series. All eight measured samples complete the same counts.

| Median over four sample summaries | Before | After |
| --- | ---: | ---: |
| Workload wall time | 2,302.715 ms | 2,352.766 ms |
| Per-sample checkpoint median, QPC | 116.147 ms | 119.388 ms |
| Per-sample queue median, manager clock | 101.048 ms | 103.714 ms |
| Per-sample service median, manager clock | 14.687 ms | 15.227 ms |
| Per-sample maximum checkpoint | 319.672 ms | 382.888 ms |

These are medians of sample summaries, not pooled global quantiles. Queue and
service use the manager's existing clock/accounting and service includes handle
acquisition. They must not be added to the QPC request total as separate work.
Queue represents 86.56-86.66% of the **summed queue plus service durations** in
each window. Those sums cover concurrent requests and are not wall-clock time.

Paired after/before wall-time changes are +0.09%, +2.25%, +1.75% and +10.63%;
paired checkpoint-median changes are +1.99%, +1.67%, +0.73% and +7.39%.
The larger final sample remains in the result. The wall-time difference between
the two medians is about +2.17%. This does not isolate a post-rename Sync cost
or establish a production p95/p99. Timing variation, background activity and
uncontrolled OS/storage caches still matter on one desktop machine.

Workload cumulative allocation is approximately 2.667 GB per sample on both
sides; it is process TotalAlloc, not peak or retained RAM. No memory-efficiency
win is claimed. The earlier fixed checkpoint benchmark remains the separate
evidence for the consistent +3 allocations/checkpoint cost.

## Checkpoint Regions

One additional tagged trace run per side is **diagnostic only**, not part of
the four timing pairs. Each pairs all 3,584 storage-region intervals,
including exactly 256 checkpoint executions and 256 WAL rotations.

| Inclusive region, median in profiled run | Before | After |
| --- | ---: | ---: |
| Commit-lock acquisition | 0.00164 ms | 0.00164 ms |
| Checkpoint execution | 16.306 ms | 15.605 ms |
| Append generation | 1.136 ms | 1.133 ms |
| Open generation | 6.488 ms | 6.245 ms |
| Rotate WAL | 8.377 ms | 7.909 ms |

Nested regions overlap; these medians and inclusive sums are not additive.
In particular, Sync/header operations sit inside broader phases, and WAL
rotation includes more than rename. The new post-rename flush is not separately
instrumented. A lower value in one profiled run is not a performance win and
does not override the unprofiled paired results.

All 256 fixed-work checkpoints are incremental. There is no compaction or
snapshot-drain span in these two traces, so they do **not** measure reader-drain
latency during full compaction. The existing
`TestCheckpointContextDrainsReservationsAndQueuesReaders` is traced separately:
it holds a reservation/reader, verifies blocked admission and queued readers,
allows an existing writer to commit, releases the reader, and requires exact
full-checkpoint publication. The parser pairs one full-compaction span, one
reader-drain span, two commit-lock spans and 33 WAL rotations including fixture
setup. Its deliberately controlled drain lasts 3.698 ms in this one trace;
that is a control observation, not a latency target for real readers.

## Verification And Next Boundary

All 23 scheduled build/measurement/validation steps pass on the frozen source:

- KitDB and releasegate suite: 908 passing root tests across 14 packages;
  20 skip events include manual campaigns. This test set differs from the older
  908-test publication source; counts alone are not coverage equivalence.
- Focused kernel/node/relational race selection, three repetitions: 30 passing
  root executions, no skips.
- `go vet ./kitdb/... ./cmd/releasegate` and `go build ./...`: pass.
- The separate tagged held-reader trace/control also passes.
- Complete post-run source hash revalidation passes.

The next performance work should isolate open-generation and WAL-rotation
subphases, then calibrate the existing maintenance-worker/cadence policy under
the intended host workload. Increasing worker defaults blindly could exchange
queue delay for I/O contention. The data does not justify changing that default,
removing a durability flush, or adding a production retry.

Local evidence: `.artifacts/kitdb-review-20261023/`. Initial diagnostic decoder
failures are retained: NUL-inclusive strings, missing type metadata and an
unresolved baseline File invalidated overly strong assumptions. A trace debug
export also exceeded the decoder's 256 MiB input ceiling. A bounded streaming
filter then selected only named region lines from the same raw traces; workload
and decoder span-count contracts were not relaxed or rerun. The original
approximately 2.12 GB debug export remains local; future trace filtering streams
without writing another such full export. Native snapshots, live queries and
sampling can perturb process behavior; no universal leak-free claim follows.

No Linux qualification, physical power-loss test, 24-hour run, commit, push,
keep-awake request or power-plan change occurred.
