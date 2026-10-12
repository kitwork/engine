# Checkpoint Pressure Policy

## Scope

`Manager.EvaluateCheckpoint` evaluates a host-local policy on a regular node
lease and submits `MaintenanceCheckpoint` through the existing bounded queue.
It adds no worker pool, per-database goroutine, timer, or durability authority.
The database entry owns one pending ticket, one scheduling claim, and bounded
failure/cooldown state. Entry eviction discards that process-local state.

The managed Kitwork write hooks use this evaluator. Standalone Go/SQL hosts can
invoke it explicitly on their own existing cadence or write boundary; it is not
an automatic background checkpoint controller for every open KitDB file.
Idle files do not get polled. A time threshold is acted on at the next evaluation,
not exactly when a timer expires.

## Signals

Kernel Stats already expose WAL bytes, overlay mutation count, and the accounted
snapshot-overlay estimate. `OverlaySince` records when the current nonempty
overlay was first observed in this process. It survives overwrites and is reset
on successful checkpoint. WAL recovery starts its age at reopen; it is not a
persisted timestamp for the original write. `LastCheckpointAt` is a process-local
clean-state observation initialized at a clean open and updated when the overlay is reset,
not a durable historical checkpoint date.
It is zero after recovery with a nonempty overlay until a checkpoint succeeds.

The byte signal includes estimated entry metadata and a small base charge. It
is not RSS. Existing readers can retain older overlay copies after checkpoint;
checkpoint does not revoke them or erase their snapshot charges.

## Host API

```go
ticket, err := manager.EvaluateCheckpoint(ctx, path, options,
    node.CheckpointPressurePolicy{
        WALBytes:       4 << 20,
        OverlayBytes:   8 << 20,
        Mutations:      2048,
        MaxAge:         30 * time.Second,
        MinInterval:    time.Second,
        InitialBackoff: time.Second,
        MaxBackoff:     time.Minute,
    }, node.MaintenanceBackground)
```

Positive thresholds are ORed. A zero threshold is disabled; all thresholds zero
or negative fields are rejected. Zero timing fields select one-second cooldown,
one-second initial failure backoff, and one-minute maximum backoff. Timing bounds
are validated. Empty overlays do not trigger memory/age pressure. A WAL threshold
can still trigger when the transaction boundary has advanced beyond the checkpoint,
so WAL-only progress is not ignored.

The evaluator returns a shared ticket while a checkpoint is pending, or nil
when below threshold, in normal cooldown/backoff, or when another normal caller
is claiming submission. Callers need not wait on background tickets. Canceling
ticket Wait cancels only the observer, not the shared checkpoint.

Failures (including reader-blocked full compaction) use capped exponential
backoff. Queue rejection also backs off; cancellation during submission does not
poison the database with a failure delay. Urgent callers get
`ErrCheckpointBackoff` during failure delay or another submission claim.
Urgent pressure can bypass a successful cooldown, but never the failure backoff.
Successful completed checkpoints reset the failure count.

Explicit `ScheduleCheckpoint` remains the unconditional operator path and still
coalesces jobs. Do not use it in a tight retry loop to bypass pressure policy.
Policy attempts are on the ordinary maintenance metrics, not a second scheduler.

## Managed Writes

Existing WAL and mutation thresholds remain. Managed foreground writes additionally
schedule at one-quarter of `SnapshotMemoryLimitBytes`, or 30 seconds of nonempty
overlay age. At one-half of the snapshot budget (or the old hard WAL/mutation
threshold), the write hook uses urgent priority and waits for its shared ticket.
Checkpoint execution remains in the maintenance worker, not in the request;
the existing hard-pressure wait is intentionally preserved. A failure/backoff
can reject a hard-pressure write instead of growing the overlay indefinitely.

Bulk writes retain their larger WAL window but use the same memory/age protection.
Background migration/index work already owns its database maintenance gate, so
it retains its existing inline checkpoint path inside that worker; enqueueing
a nested same-path job would deadlock. That path now also considers memory/age.

This is scheduling protection, not a strict live-overlay memory cap. One large
commit can cross a threshold, direct kernel callers can omit evaluations, and
failed or snapshot-blocked checkpoints cannot reclaim memory until the cause is
resolved. Slow readers and maintenance failures must remain visible to the host.
The kernel's WAL sync/ACK contract and on-disk formats are unchanged.

## SQL Adapters

Managed `relational.PostgresNode` engines use their existing node manager
automatically. Native SQL pools and KitSQL pools borrowed from that node share
the same policy; it is not implemented separately in each transport.
An embedded host may use `relational.Attach(lease.DB(), relational.Options{
Kernel: openOptions, CheckpointManager: manager})`. The manager and open options
must be those owning that kernel lease. Plain `relational.Open`/`Attach` without
a manager retain explicit maintenance; no hidden owner or scheduler is created.
Construction verifies the manager returns the same kernel pointer and matching
open options, rejecting unavailable or incorrectly attached owners before use.

SQL uses the normal 4/8 MiB WAL and 2048/4096 mutation soft/hard windows, plus
one-quarter/one-half of the snapshot byte budget and a 30-second soft age limit.
Hard admission runs before statements, writable snapshot capture at BEGIN, and
explicit transaction commit, outside the relational write gate. It can reject before publication while waiting for
the shared ticket. Transactions retain their original snapshots: checkpoint
does not change isolation or discard their conflict dependencies.

Post-execution/commit evaluation is best-effort. It runs after releasing the
write gate and transaction snapshot, never changes the returned commit outcome,
and never replays SQL. Maintenance failures still appear in node stats and
backoff; future hard-pressure writes may be rejected. A large individual commit
can overshoot a threshold, so this is not a strict live-overlay memory cap.
Checkpoint backoff, queue-full and reader-blocked admission map to KitSQL
`KSQL_BUSY` / pgwire `53300`; a closed owner maps to unavailable. A client must
not mistake these pre-publication rejections for uncertain commit durability.

The former PostgreSQL connection message conflated acquisition deadlines and
warm capacity. These now have distinct messages (both use `53300`). The failing
warm projection residency test configured a 50 ms timeout for disk/projection
open; it now uses 5 seconds, while actual capacity/deadline tests retain short
timeouts. This removes an unrelated timing requirement from the residency test,
but does not prove the exact cause of the earlier failed gate.

`relational/checkpoint_pressure_test.go` covers native SQL and real pgwire
autocommit/explicit transactions, owned maintenance, failed post-commit
evaluation preserving row data after reopen, and hard-pressure rejection before
the next commit. `kitsql/network_load_test.go` now uses this policy under real
TCP traffic, with no periodic checkpoint loop. See [network evidence](../kitsql/NETWORK_LOAD.md).
An additional fixture fills the incremental segment window with an old reader,
releases it, and proves writable BEGIN completes full compaction before taking
its own snapshot, without changing the transaction's isolation contract.

## Validation

Managed checkpoint jobs now call `DB.CheckpointContext`. Incremental publication
does not pause readers. Full compaction temporarily queues new snapshot
reservations, waits for existing snapshots and reservations to finish, then
holds admission closed through publication. Existing reservations may still
capture their snapshot; existing transactions may still commit. The drain
does not hold the commit mutex. Snapshot file handles close before their active
registration is removed, including on Windows.

Drain and commit-lock acquisition have a two-second ceiling (or the shorter
maintenance context). Cancellation, close, and publication errors always
release the admission barrier. Waiting readers retain the existing 128-entry,
two-second FIFO and slot/byte limits. Long-lived readers can still cause a
bounded `ErrSnapshotsActive` failure; no snapshot is forcibly invalidated.
After durable publication starts, cancellation cannot interrupt its WAL
boundary. Direct `DB.Checkpoint()` keeps its immediate reader-rejection behavior.
There is no new worker, timer loop, tenant API, or durable coordination state.

Mixed native/pgwire soak and deterministic queue-full/long-reader recovery are
documented in [SQL pressure verification](../relational/PRESSURE_SOAK.md).
Reader-blocked failures retain backoff while snapshots/reservations exist.
After they disappear, the next evaluation may retry after the initial interval,
without waiting for an accumulated exponential delay. This is a point-in-time
observation, not a guarantee that new readers cannot race checkpoint. Other
failure classes keep normal backoff and the shared queue still coalesces jobs.

`checkpoint_pressure_test.go` covers threshold boundaries, invalid policy,
bounded backoff, 32 concurrent callers sharing one pending job, injected
reader-blocked failure without retry storms, successful recovery and cooldown.
The storage drill writes 80 distinct 4 KiB values with checkpoint evaluation,
retains an old reader through incremental generations until full compaction is
blocked, then closes it and resumes. It verifies storage and exact row lengths
after reopening. A second drill runs four concurrent writers (256 distinct
4 KiB values) alongside pressure-triggered checkpoint jobs, then compares every
value byte-for-byte after reopen and verifies storage. This is clean-reopen
evidence, not physical power-loss proof.

`work/db_kitdb_node_test.go` covers existing normal/bulk behavior and a 20 MiB
bulk write that triggers memory-pressure checkpoint below its 256 MiB WAL window.

```powershell
go test -race ./kitdb/node ./work `
  -run 'TestCheckpointPressure|TestKitDBWritesUseBoundedCheckpointMaintenance|TestKitDBBulkWrite|TestKitDBBackgroundMaintenanceBoundsItsOwnOverlay' `
  -count=3 -timeout=3m
```

## Local Evidence

2026-10-11, Windows/amd64, dirty working tree: pressure/node and managed-write
tests passed three repeated race runs. The final timestamp/threshold/node race
run also passed three times. Whole-engine build and KitDB/work static analysis
passed. The complete `kitdb-verify` rerun passed all 11 checks in 4m59.603s,
including recovery, operator, commerce hard-kill/backup/PITR and database drills.

The first gate failed at
`TestPostgresNodeWarmDatabaseRetainsProjectionWhileIdleBudgetTrimsOthers` with
"database capacity is busy". It passed five isolated repeats and the complete
rerun. Its cause was not established, so this is retained as a qualification
signal, not presented as a fixed bug or silently discarded failure.

- `.artifacts/kitdb-checkpoint-pressure-20261011.json`: original failed gate.
- `.artifacts/kitdb-checkpoint-pressure-recheck-20261011.json`: complete passed gate.
