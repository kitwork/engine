# SQL Pressure and Recovery

## Scope

This is verification of the existing SQL engine and node ownership contract,
not a new scheduler, tenant API, database format, or automatic SQL replay.
Native SQL and real loopback pgwire share one `PostgresNode` kernel owner.
No project database or configured application credentials are used.

## Deterministic Recovery

`TestCheckpointPressureRecoversAfterQueueFullAndCanceledWait` fills a real
bounded maintenance queue behind one deliberately paused worker. Pressure
admission must return queue-full; canceling one ticket waiter must not cancel
the shared job. Once the worker is released and real backoff expires, the same
database must checkpoint successfully and return all active leases/queue slots.
The fixture does not edit internal backoff timestamps to force recovery.

`TestSQLCheckpointRecoversAfterLongReader` keeps one read-only SQL transaction
alive through incremental generations until full compaction is reader-blocked.
A large acknowledged write makes the real pressure job fail. New writers must
back off without committing or retaining snapshots. The old reader must still
see its original version/payload. After it rolls back and real backoff expires,
writable BEGIN must perform full compaction before capturing its own snapshot,
then another write must succeed. Reopen must preserve acknowledged row data and
the secondary-index lookup. Setup alone uses explicit kernel checkpoints to
reach the segment boundary deterministically.

```powershell
go test -race ./kitdb/node ./kitdb/relational `
  -run 'TestCheckpointPressureRecovers|TestSQLCheckpointRecovers' `
  -count=5 -timeout=2m
```

## Mixed Soak

`TestSQLPressureSoak` is opt-in and defaults to 180 seconds in three equal
rounds. `KITDB_SQL_SOAK_SECONDS` accepts 9..1800 seconds, divisible by three.
It owns 256 rows with 2 KiB payloads, a version index, and 8 MiB snapshot and
transaction budgets each. Four native explicit-transaction writers and four
pgwire autocommit writers each own one row. Four readers, one slow read-only
transaction worker and one canceled-stream worker run alongside them. Both SQL
pools are capped at 12 connections; pgwire at 32. Operation metrics and resource
samples use fixed counters/one round accumulator, not per-request retention.

Only confirmed successful commits increment the independent acknowledgement
oracle. Writes are not intentionally canceled at round boundaries; each has a
30-second deadline and any ambiguous transport/durability error fails the run.
Busy/conflict rejections are separate from completed writes. Each later counter
increment is a new test operation, not generic transparent SQL replay. Read
cancellation is intentional, separately counted and must actually be observed.

Every round drains workers and maintenance, requires zero snapshot/reservation/
waiter/transaction/commit ownership and SQL connections in use, compares every
row byte-for-byte, and checks all secondary-index value groups. Snapshot and
transaction budget assertions apply during traffic. After the third round,
storage Verify, close/reopen and complete row/index comparisons must pass.
After load readers stop, each round also requires a new acknowledged native
write within ten seconds through ordinary SQL admission, without a forced
checkpoint or changed policy timestamps. Recovery wait/rejections/writes are
reported separately; the total acknowledgement oracle includes these three
quiescent writes.

Report v2 also gates sustained write progress: any 30-second progress window
with no acknowledged writes, or a sampled no-write interval above 30 seconds,
fails the test. `WriteProgressVerified` and `Success` are false in the saved
report on that failure. Sampling is once per second, not a per-commit latency
oracle. Historical v1 reports below used safety/cleanup/reopen gates only.

```powershell
$env:KITDB_SQL_SOAK = "1"
$env:KITDB_SQL_SOAK_SECONDS = "180"
$env:KITDB_SQL_SOAK_REPORT = Join-Path (Get-Location) ".artifacts/kitdb-sql-soak.json"
go test -race ./kitdb/relational -run '^TestSQLPressureSoak$' -count=1 -v -timeout=6m

# Longer local qualification, without changing ordinary test duration:
$env:KITDB_SQL_SOAK_SECONDS = "1800"
go test -race ./kitdb/relational -run '^TestSQLPressureSoak$' -count=1 -v -timeout=35m
Remove-Item Env:KITDB_SQL_SOAK
```

## Limits of Evidence

The JSON report includes per-round acknowledged writes, read activity, observed
cancellations, checkpoint count, maximum operation latency, sampled resource
peaks and post-GC heap/goroutine observations after drain. Heap includes the Go
test clients and server; it is not process RSS or an engine-only memory cap.
Sampling can miss peaks, and three post-GC observations cannot prove absence of
long-term heap growth. The exact kernel byte/ownership assertions are distinct
from these observations. The fixed hot-row working set is not an unbounded
insert/churn test or fleet-wide RAM qualification.

Long runs log bounded progress every 30 seconds (writes and delta, reads,
busy rejections, checkpoints, sampled heap and main-segment count). The JSON
rounds include `MaxNoWriteSeconds` from approximately one-second sampling and
`ZeroWriteWindows` for logged intervals without a new acknowledged write.
These are observations, not new fail thresholds; sampling can miss brief
stalls or underestimate their duration. No per-operation history is retained.

Loopback traffic omits TLS, WAN latency, external machines, and mobile/desktop
platform qualification. Clean reopen is not process-crash or power-loss proof.
Use the existing release/recovery gates and the 24-hour storage canary in
`../RELEASE_1_0.md` before declaring a stable release. Concurrent UI failures in
the broad engine gate must still be resolved independently.

## Initial Finding

The initial 180-second race soak exposed a liveness gap rather than observed
row corruption: round 3 completed zero writes, produced 109,634 busy rejections
and zero checkpoints, while reads continued. The overlay/WAL reached hard
pressure and the 32-segment window required full compaction. Repeated live
readers blocked it, allowing exponential failure delay to outlive individual
reader ownership. All rounds drained and matched the row/index oracle, but the
required write-progress assertion failed before reopen, so this is not a passed
restart campaign. No success JSON was emitted for that failed run.

The node evaluator now remembers only whether the last completed failure was
`ErrSnapshotsActive`. With no active snapshots or reservations, it allows a new
attempt after the initial backoff interval rather than the accumulated delay.
The original interval still bounds attempts if a new reader races the observed
quiescent point. Disk/queue errors keep their ordinary exponential backoff;
no reader is invalidated and there is no new polling worker. Continuous readers
can still block compaction, so the campaign deliberately removes read pressure
between rounds and proves ordinary write admission recovers.

## Local Evidence (2026-10-11)

Windows/amd64, Go 1.26.0, dirty working tree, local filesystem, clients and server
in the same Go test process. The final 180-second race campaign passed in
182.11 seconds of test time:

- 72,062 acknowledged writes: 72,059 under load and 3 quiescent recovery writes.
- 64,974 point reads, 10,266 observed stream cancellations and 848 slow readers.
- 42 pressure checkpoints across the rounds (17, 15, 10). Main generation 33
  had one segment, proving the campaign crossed full compaction, not only
  incremental checkpoints.
- No unexpected errors or reported races. Snapshot/reservation bytes, waiters,
  transaction bytes, active transactions, pending commits, maintenance jobs and
  SQL connections in use drained to zero at every round boundary.
- Complete row/payload/index equality passed after every round and reopen.
- Post-GC heap after drain was 2.82, 2.12 and 2.71 MiB; sampled peak was 9.60 MiB.
  Goroutines after each drain were 14. Heap before/after close was 0.75/1.07 MiB.
  These observations are not RSS ceilings or long-term leak qualification.

The final short race/report-schema run passed with 2,131 acknowledged writes
and one actual checkpoint. Deterministic recovery tests passed five repeated
race runs, final node pressure tests passed five repeats, and final full node,
relational and KitSQL suites passed. Managed normal/bulk work pressure tests
passed three race repeats. KitDB vet passed. Whole-engine build passed on retry;
the first attempt had compiler errors opening standard-library imports in
`jit/fonts`. Its cause was not established and no font code/cache was altered.
The broad release gate was not rerun in this change; previous UI failures are
not treated as fixed by these database tests.

Ignored local reports:

- `.artifacts/kitdb-sql-soak-final-180s-20261011.json`
- `.artifacts/kitdb-sql-soak-final-smoke-20261011.json`

This three-minute campaign did not include a Linux run or 24-hour canary. A
later 30-minute run is recorded below. The separate
[SQL process-crash matrix](SQL_CRASH.md) tests hard-kill recovery; that evidence
is not implied by this green three-minute campaign.

## 30-Minute Race Campaign (2026-10-11)

Windows/amd64, Go 1.26.0, local temporary database, dirty working tree. The
compiled test binary stayed unchanged through all three 600-second rounds.
Test time was 1800.96 seconds (package time 1803.568s). The final report is
`.artifacts/kitdb-sql-soak-30m-race-20261011.json`.

The correctness/resource test returned PASS, but this campaign **does not
qualify sustained write liveness**:

- 167,322 acknowledged writes: 167,319 under the phase workload plus three
  quiescent recovery probes. Per-round workload writes: 55,795 / 55,761 / 55,763.
- 1,409,755 point reads, 122,626 observed stream cancellations and 8,802 slow
  readers. No unexpected operation errors or reported races; no conflicts.
- 3,660,230 busy rejections. All three rounds reached 32 main segments, then
  had long intervals without a new acknowledged write while reads continued.
- Maximum sampled no-write intervals were 513.75 / 521.79 / 522.97 seconds.
  Each round logged 16 zero-write progress windows (48 total).
- After stopping load readers, each native recovery probe succeeded without
  rejection, in 142.44 / 137.54 / 1.11 milliseconds. Full compaction completed:
  drained generations 33 / 65 / 97 each had one segment. There were 96 successful
  pressure checkpoints across the rounds, including those recovery checkpoints.
- Every round returned snapshot/reservation/waiter/transaction/commit ownership
  and SQL connections in use to zero. Complete primary-row/payload and secondary
  index comparisons passed after each drain and final close/reopen; `Verify`
  passed before and after reopen.
- Post-GC heap after drain: 2.12 / 2.13 / 2.14 MiB, with 14 goroutines each.
  Sampled heap peak was 9.36 MiB; heap before/after close was 0.75/1.03 MiB.
  These are client-plus-server Go heap observations, not RSS or leak guarantees.
- Sampled snapshot/reservation peak was below the configured 8 MiB budget;
  transaction-byte peak was 26,037 bytes. No snapshot waiters were observed.

The evidence establishes recovery after quiescence, not good progress while
read traffic remains continuous. The full-compaction path refuses active
snapshots, and its pressure retry policy only accelerates after observing no
snapshots/reservations. The repeated stalls at the segment limit are consistent
with reader-blocked compaction starving writers. This is a release-readiness
issue even though the test's existing safety/cleanup assertions pass.

The next hardening target should be bounded reader-admission coordination for
managed full compaction: let existing snapshots finish, briefly queue new ones,
respect context deadlines, and release the admission barrier on every outcome.
Preserve old reader validity, the existing slot/byte/queue bounds and the shared
node worker. This is a proposed follow-up, not implemented in this campaign.
Increasing limits or only changing retry cadence would not prove fairness.

A separate scheduled/manual `storage-soak` CI job now runs this campaign on
Windows and Linux, uploads logs and JSON labeled with the checkout SHA, and
warns when zero-write windows are observed. It deliberately preserves the
existing test pass conditions. JSON `Success` means the safety/cleanup/reopen
oracle passed; it must not be read as liveness or production qualification.
No remote CI result, Linux run, immutable candidate commit or 24-hour canary
was obtained here. Relational vet passed; changes remain uncommitted.

## Bounded Compaction Admission Follow-Up

Managed maintenance now uses `DB.CheckpointContext` with a two-second admission
budget and the shorter caller deadline. Only full compaction queues new readers;
incremental checkpoints do not. It drains existing snapshots AND reservations
without holding the commit mutex. Already admitted reservations can capture
their snapshot, and an existing SQL writer can finish its commit. Snapshot
handles close before unregistering so a zero-reader observation is safe for
Windows file replacement. The admission barrier releases on success, error,
cancellation and database close. Publication itself finishes its durable
boundary even if cancellation arrives after it starts.

No new worker, SQL retry/replay, increased slot/byte limits, durable state, or
tenant API was added. Direct `DB.Checkpoint()` still rejects active readers
immediately. An indefinitely held reader can still cause a bounded failure;
the change prevents a succession of short readers from starving full compaction,
not arbitrary long-lived transactions from blocking it.

Kernel tests cover queued readers, preexisting reservations, old-reader validity,
concurrent checkpoint ownership, context/commit-lock deadlines, the default
drain timeout and close. `TestSQLTransactionCommitsWhileFullCheckpointDrains`
checks an actual staged SQL writer while the shared maintenance job is waiting
for its snapshot, then verifies the full checkpoint contains that commit.

Initial local post-fix race soak: Windows/amd64, Go 1.26.0, 180 seconds (181.14s
test time), 83,132 acknowledged writes, 74,374 reads, zero busy/conflict
rejections and zero zero-write windows. There were 49 pressure checkpoints;
generation 35 had three segments during the second round, demonstrating full
compaction under continuing load. Exact data/index/drain/reopen checks passed.
Ignored report: `.artifacts/kitdb-sql-quiescence-180s-race.json` (v1 observational
schema, before enabling the v2 liveness failure gate).

The subsequent 300-second race campaign passed the v2 write-progress gate in
301.88 seconds of test time (304.547s package time):

- 102,249 acknowledged writes, 91,926 point reads, zero busy/conflict rejections
  and zero zero-write windows. No unexpected errors or reported races.
- 60 pressure checkpoints (28 / 16 / 16). Second-round progress moved from
  32 main segments to six while readers and writers continued; its drained
  generation was 45 with 13 segments. Peak sampled queued readers was 12.
- Every round drained snapshot/reservation/waiter/transaction/commit ownership
  and SQL connections in use to zero. Complete data/index comparison, storage
  Verify and reopen checks passed.
- Post-GC heap after drain: 2.74 / 2.86 / 2.77 MiB, 14 goroutines each. Sampled
  peak heap was 8.76 MiB; these are not RSS ceilings or leak qualification.
- `.artifacts/kitdb-sql-quiescence-300s-race.json` records
  `WriteProgressVerified: true` and `Success: true` with format v2.

Local verification also passed the complete `kitdb/...` suite normally and
under the race detector, five race repetitions of the staged SQL-writer
regression, three race repetitions of the tagged 17-case SQL I/O-fault,
10-case checkpoint-publication crash and three-case kernel WAL-fault matrices,
normal/tagged KitDB vet, and the full engine build. These runs use a dirty local
Windows checkout, not a released immutable candidate or remote CI evidence.

The historical 30-minute starvation evidence remains above. The post-fix
30-minute campaign is recorded below; Linux and a 24-hour candidate canary
remain qualification work. No physical power-loss guarantee is implied.

## Post-Fix 30-Minute Race Campaign (2026-10-12)

Windows/amd64, Go 1.26.0, local temporary database, dirty working tree based on
`4e142d7e53c606a9e8c0d1b397203de66e486368`. This is not evidence for that clean
commit: the binary includes the recorded uncommitted KitDB changes. One
separately compiled race-enabled test binary ran all three 600-second rounds.
Test time was 1801.25 seconds. Binary SHA-256:
`CCF9436D3C9BE235272CDF4CBA97CC898E113CDF399A7A68ECCDD4B4639DEF38`.
Its hash matched after the run; all recorded KitDB Go source hashes were
unchanged. No additional build or heavy benchmark was launched during the run.

- 547,157 acknowledged writes: 547,154 under load (207,671 / 176,142 / 163,341)
  plus three ordinary quiescent recovery writes. There were 492,968 point reads,
  89,874 observed stream cancellations and 8,419 slow readers.
- Zero busy/conflict rejections, zero zero-write progress windows, no unexpected
  operation errors or reported races. The v2 write-progress gate passed. Every
  one-second sample observed write advancement; this is sampled progress,
  not a claim of zero latency or absence of shorter pauses.
- 323 pressure checkpoints (122 / 105 / 96). Progress logs repeatedly show
  the segment count reaching 32 then returning to a small count while both
  reads and writes advance. Drained generations were 123 / 228 / 324 with
  27 / 4 / 4 segments. Full compaction did not require stopping load readers.
- All three rounds passed complete primary-row/payload and secondary-index
  comparisons and returned snapshot/reservation/waiter/transaction/commit
  ownership and SQL connections in use to zero. Storage Verify and complete
  close/reopen comparisons passed. Recovery probes needed no rejection/retry
  and completed in 4.98 / 3.01 / 3.28 milliseconds.
- Peak sampled queued readers: 12, below the 128-entry ceiling. Snapshot plus
  reservation bytes peaked at 3,924,074, below the configured 8 MiB budget;
  transaction bytes peaked at 29,287.
- Post-GC heap after drain: 2.88 / 2.42 / 2.56 MiB, with 14 goroutines each.
  Sampled peak heap was 8.73 MiB; heap before/after close was 0.75/1.22 MiB.
  These include test clients and server, are not RSS ceilings, and do not prove
  long-term absence of leaks. Throughput varied across the run; this is not a
  controlled throughput benchmark or a universal performance guarantee.

Ignored local evidence, all sharing the stem
`.artifacts/kitdb-sql-quiescence-30m-race-20261012`:

- `.json`: v2 report, with exact data, reopen, write progress and Success true.
- `.log`: complete test progress and PASS output.
- `.provenance.json`: base commit, dirty KitDB paths, source and binary hashes,
  start/completion timestamps and exit code zero.
- `.test.exe`: the exact race-enabled binary used by this campaign.

This run addresses the earlier 30-minute continuous-reader starvation signal
for this local workload. It does not qualify arbitrary long-held transactions,
Linux, physical power loss, or a stable release. Same-candidate Windows/Linux
evidence and a 24-hour storage canary still precede production qualification.
