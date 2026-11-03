# Snapshot lock-holder attribution on Windows

## Outcome

Added opt-in, DB-owned, bounded lock instrumentation under `kitdb_profile`.
The normal build retains empty recorder/span types and passes a zero-allocation
lock-wrapper test. No lock order, snapshot ownership, storage format, Sync
policy, cache budget or worker limit changes. Thin lock wrappers do change
normal code shape; their latency is **not qualified as non-regressing**.

The QPC measurements identify snapshot capture as an observed holder overlapping
checkpoint writer acquisition intervals. Within snapshot capture, the open-file
phase dominates measured hold time. This narrows the next optimization target,
but does not establish pure disk latency, eliminate scheduler/GC effects or
justify moving `os.Open` outside synchronization without a generation-lifetime
design. No additional storage optimization is shipped by this investigation.

See [checkpoint-tail traces](CHECKPOINT_TAIL_2026-10-24.md) for the prior waiter
attribution and [the instrumentation contract](../../kitdb/LOCK_PROFILE.md) for
field meanings, resource limits and exact coverage.

## Reproducibility and source

Local evidence: `.artifacts/kitdb-lock-holder-20261025/` in the engine repository.
The previous retained point-reader candidate is the reference, not the original
pre-point-reader implementation and not the whole current dirty worktree.
The campaign freezes 12,006 files from the verified R3 candidate plus 14 scoped
instrumentation/test file overlays. Base hashes and overlay hashes are recorded.

An initial recorder used Go monotonic interrupt time. Short Windows phases were
quantized, sometimes to zero; those profiles and the first assessment remain
available but are superseded for holder-timing conclusions. `r2/source.json`
and `r2/overlay.json` capture exactly one correction to that frozen source:
`kitdb/lock_profile_enabled.go` now uses the existing `internal/benchclock`
(QPC on Windows). Go's build overlay applies that captured file without mutating
the original frozen tree. The correction is profile-tag-only; the ordinary
binary and its original gate evidence remain unchanged. Final audit verifies
all frozen files, the one virtual correction and the shared scoped source.

Go 1.26.0, Windows/amd64, GOMAXPROCS=4, GOGC=100, maximum Go memory limit.
Each process performs fixed mixed work: 16 databases, 1,024 acknowledged commits,
3,072 point reads, 3,072 index reads, 256 checkpoints and 16 verified reopens.
Cache is 256 KiB/database, 4 MiB total; maintenance stays at two workers.

The initial campaign has 18 workloads: five normal pairs and four profile
on/off pairs, with the first pair of each excluded as warmup. The corrected QPC
campaign adds four profile on/off pairs, again excluding the first. Runtime
tracing is disabled throughout. Build, tests, parsing and inventory checks do
not overlap timed workloads. Alternating order and every raw result are kept.
These are native relational/kernel/node tests, not pgwire or a web application.

## Corrected QPC results

All four QPC-on reports, including warmup, match 1,024 checkpoint boundaries and
3,072 writer-lock intervals. Each report contains 15,360 lock intervals, 960 per
database, with no drops. The recorder caps each DB at 4,096 samples, preallocated
before workload timing (320 KiB/DB on amd64). Report copying is outside timing.

For each writer's wall acquisition interval, the analyzer intersects observed
snapshot read-hold intervals from the **same DB** and takes their union. It does
not add overlapping reader durations. Every writer site must match the workload
transaction, with exactly one install/detach/WAL-install sample per checkpoint.

| Measured QPC pair | Writer acquisition wall time, summed ms | Union of snapshot hold overlap, ms | Overlap |
| --- | ---: | ---: | ---: |
| 1 | 409.9302 | 408.0440 | 99.54% |
| 2 | 262.0429 | 260.1520 | 99.28% |
| 3 | 356.8187 | 313.3832 | 87.83% |

These sums span 256 checkpoints and multiple workers; they are **not** workload
wall time or proof that every overlapped nanosecond was blocked on that reader.
The residual includes uninstrumented holders, scheduling and boundary timing.

The 7,168 admitted snapshot captures in each measured process contain:

| Pair | Snapshot hold, summed ms | Waiting for snapshotMu, ms | Open-file phase, ms | Overlay clone, ms |
| --- | ---: | ---: | ---: | ---: |
| 1 | 4,841.00 | 6.13 | 4,539.64 | 87.19 |
| 2 | 3,029.57 | 9.63 | 2,799.10 | 82.53 |
| 3 | 3,315.91 | 29.52 | 3,116.73 | 62.33 |

The open-file phase accounts for approximately 92-94% of these capture hold
totals. Phase durations include scheduling and GC; they are not syscall-only
measurements. Phase totals describe each holder, not exact intersections of
each phase with a writer's wait. Other code and instrumentation account for
the remaining hold time.

Concrete example: QPC pair 1, database 12 / transaction 18, main installation
waits 53.9431 ms until write-lock acquisition. A snapshot capture's certain
read-hold interval overlaps 53.9347 ms of it. That snapshot holds for 54.7454 ms,
including 54.7325 ms in the open-file phase and 0.0082 ms cloning the overlay.
The writer's own measured critical section is 0.0017 ms. Thus the long observed
interval is not explained by that writer doing expensive state publication.
This example is profiled behavior, not an unprofiled p99 guarantee.

## Observer and default-build controls

The corrected profile on/off comparison uses the **same binary**. Median paired
changes across three measured pairs are elapsed +9.99%, allocation +0.185% and
coarse checkpoint service p99 +25.94%. Allocation is workload total allocation,
not RSS. These short controls are noisy, not a universal observer-cost bound;
they clearly prevent treating instrumented timings as an optimization result.
The clock helper and the retained sample buffers can affect allocation, GC and
scheduling even though collection does not run under `db.mu`.

For the ordinary build versus its pre-instrumentation candidate reference,
four measured pairs give median paired elapsed -2.77%, allocation -0.024%,
coarse service p99 +7.88% and maximum +2.21%. P99 worsens in three pairs and
improves in one. No zero-overhead or tail non-regression claim is made. Absolute
latencies must not be pooled with previous campaign batches as if the machine
state were identical. Normal structs have no diagnostic sample buffer, and
`TestLockProfileDisabled` separately verifies zero allocation in the wrappers.

## Verification

- Ordinary KitDB suite: 912 root pass executions, 19 root skips, 13 packages.
- Corrected QPC controls: 52 root pass executions, two root skips, two packages.
- Corrected QPC race selection: 60 root pass executions over two repeats, two
  packages. This covers recorder concurrency, snapshot and attribution paths;
  it is not a race run of every package.
- Normal and profile vet/build checks pass. Earlier interrupt-time controls
  also passed but are not substituted for the corrected QPC controls.
- Offline interval-union/boundary tests pass two roots, covering overlap,
  duplicate writer identities, dropped evidence and invalid phase coverage.
- All 26 workload reports pass exact oracle/work/reopen checks. Managers close;
  open/active/idle/warm handles, leases, waits, cache reservations and maintenance
  queues drain. Open/maintenance/cancellation/rejection/close errors are absent.

The initial shared smoke command encountered sandbox-denied main-file rename
in sandbox Temp; the same command passed outside the sandbox. Frozen gates use
that normal Windows environment. The first offline summary hit PowerShell's
`Compare` alias; its function was renamed, and the same saved data was analyzed
again. These limitations are recorded in `analysis-tooling-note.json`; workloads
were not rerun to hide either issue.

No Linux qualification, crash campaign, 24-hour soak, power-plan change,
production deployment, commit or push. Stable and tail qualification stay open.

## Next boundary

Prototype a generation-bound snapshot reader handoff: eliminate repeated path
opens while retaining an independently valid immutable generation for each
snapshot. Compare explicit handle duplication against shared-reader pinning,
including close-error ownership, compaction exclusion and Windows sharing.
Do not simply hoist the path open outside `db.mu`: publication could replace
the generation between selecting metadata and opening the file. Require
controlled snapshot/checkpoint/close interleavings, fault/race checks and
unprofiled paired measurements before choosing or retaining a production change.
