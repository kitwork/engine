# SQL Canary Harness

The first gate is a finite, opt-in smoke test in `sql_canary_test.go`. It uses
native SQL, PostgreSQL wire over real loopback TCP, and KitSQL HTTP over real
loopback TCP. All three share one relational engine attached to one managed
kernel lease. No adapter independently opens the storage file.

## Run

The independent model and error-classification unit tests require no sockets:

```powershell
go test ./kitdb/kitsql -run '^TestSQLCanary(Oracles|ErrorClassification)$' -count=1
```

Enable the short integration test explicitly:

```powershell
$env:KITDB_SQL_CANARY_SMOKE = '1'
go test -race ./kitdb/kitsql -run '^TestSQLCanary' -count=3 -timeout 120s -v
```

Each plain/controlled smoke run has a 45-second context deadline and a fixed finite workload.
An optional short pressure phase is described below. It creates a temporary database and ephemeral
loopback ports. There is no access to application databases or production ports.
Windows sandbox environments may prohibit the required atomic file rename;
record that failure separately and use an approved ordinary Windows process.
Do not remove atomic publication to make the sandbox test pass.

Optional JSON output:

```powershell
$env:KITDB_SQL_CANARY_REPORT = 'D:/qualification/sql-smoke.json'
```

The parent directory must already exist and the path must be absolute. Keep the
test output as well as the report: JSON describes the latest invocation, whereas
`-count=3` produces three independent smoke results in the test log. Do not reuse
this report path for concurrent runs. Failure reports are not success receipts.

## What Is Checked

- One shared manager/relational owner with matching kernel memory options.
- A fixed 32-row fixture, deterministic payload, and a secondary version index.
- Three concurrent workers, one per adapter, with 12 bounded update/read attempts
  each. Native writes use an explicit transaction; network writes use autocommit.
- One writer per key. Expected versions advance only after confirmed commit
  success, independently of database reads. Each adapter must ACK at least one
  write and every successful read must match its owned key's model and payload.
- Conflict and admission rejection counts use typed native errors, PostgreSQL
  SQLSTATE, or KitSQL code/status pairs. An unknown outcome or uncertain durability
  fails the run; there is no automatic replay.
- Full primary data and expected secondary-index group membership are compared
  through every adapter. No write traffic is active during these comparisons.
- Early closure/cancellation of a row result through every adapter, followed by
  ownership drain. The counter means a stream was closed early, not proof that a
  server cancellation response was observed.
- Zero active requests, in-use native connections, snapshots, reservations,
  waiters, transaction memory, transactions, pending commits, and maintenance work
  before Verify/checkpoint/Verify.
- Network servers and client pools close before their shared owner. The manager
  must be closed with no open database, active lease, waiting acquisition, or
  maintenance work. Only then may a fresh relational owner reopen the file.
- Reopen with VerifyOnOpen and the same independent data/index model.

Negative unit tests reject missing/duplicate/out-of-range rows, wrong versions,
wrong payloads, missing/duplicate/wrong index members, mismatched HTTP statuses,
message-only fake rejection codes, and uncertain/transport-failed writes.

## Controlled Pressure Gate

Enable this explicitly in addition to smoke:

```powershell
$env:KITDB_SQL_CANARY_SMOKE = '1'
$env:KITDB_SQL_CANARY_PRESSURE = '1'
$env:KITDB_SQL_CANARY_PRESSURE_SECONDS = '6'
go test -race ./kitdb/kitsql -run '^TestSQLCanary' -count=3 -timeout 120s -v
```

Duration defaults to six seconds and is restricted to 6..30 seconds. Three
throttled adapter workers update separate reusable keys and verify their own
ACK model. A read-only native transaction holds its snapshot for 150 ms and
checks that the same row version remains visible despite concurrent writes.

A bounded checkpoint worker submits explicit managed jobs every 40 ms. Full
compaction coverage requires observing a generation advance with segment-count
reduction before the workload stops. Missing compaction fails coverage rather
than silently extending the run. This accelerates the real segment boundary
without requiring a large fixture; it does NOT qualify the automatic SQL
checkpoint-pressure policy. Publication correctly waits for existing snapshots
to drain; "under load" means workers remain running, not that atomic replacement
occurs while a snapshot still owns the old main file.

The monitor observes each adapter independently every 100 ms. A 30-second ACK
stall, backward counter, or monitor gap over two seconds fails the gate. Samples
are retained once per second and bounded by the maximum phase duration. They
include ACK counts, heap, snapshots, waiters, generation, and segments. Unit
tests exercise the exact stall boundary and ensure another adapter cannot mask
the stalled one. A six-second run does not demonstrate thirty seconds of liveness.

Stop happens between operations, not by deliberately cancelling submitted
writes. After workers finish successfully, each adapter must confirm a recovery
write through ordinary admission within one shared ten-second deadline. No
forced recovery checkpoint or transparent replay is allowed. Recovery writes
are counted separately and join the independent model used by the final data,
index, drain, Verify, and reopen gates. No recovery write is attempted after an
unknown write outcome or failed phase.

## Automatic Policy Gate

Use a separate mode, not both pressure modes at once:

```powershell
$env:KITDB_SQL_CANARY_SMOKE = '1'
$env:KITDB_SQL_CANARY_PRESSURE = '0'
$env:KITDB_SQL_CANARY_POLICY = '1'
go test -race ./kitdb/kitsql -run '^TestSQLCanary' -count=1 -timeout 120s -v
```

This profile defaults to 30 seconds, still capped at 30 seconds, with a 70-second
outer deadline allowing setup, in-flight completion, recovery, and verification.
`KITDB_SQL_CANARY_PRESSURE_SECONDS` may reduce its duration to 6..30 seconds;
missing compaction coverage fails rather than extending the run automatically.

It reuses the same 32 rows with an 8 KiB deterministic payload, 512 KiB snapshot
budget, and 8 MiB transaction budget. Writers rotate over disjoint reusable key
sets, retaining independent ACK ownership. SQL's existing checkpoint policy is
unchanged. There is NO scheduled checkpoint worker in this mode. Generation
advances and segment-count reductions are sampled only during the workload,
before recovery or the final explicit Verify/checkpoint/Verify sequence.

Initial concurrent reads/transaction admission may receive typed capacity
rejections in this intentionally small-budget profile. Those are counted rather
than replaying submitted writes. Unknown codes, timeout, transport errors,
uncertain durability, or a mismatch still fail. PostgreSQL `XX000` never becomes
an accepted rejection merely because its message mentions memory pressure.

Calibration against e70f50e2 exposed pgwire returning `XX000` for snapshot-budget
rejections. The follow-up production patch maps known admission errors to `53300`
and explicitly keeps uncertain durability at `XX000`. These are different source
variants: a pass with the patch is NOT a pass for the unmodified frozen candidate.
Record the production patch hash separately; preserve the pre-patch failure logs.

## What This Does Not Prove

This is not a performance benchmark or long-running qualification. The plain
smoke does not require compaction; controlled pressure does not establish
automatic-policy compaction coverage. The automatic mode covers one bounded
small-budget profile, not every policy threshold or deployment size. None of
these runs holds a reader for minutes, validates a heap-growth trend, or proves multi-tenant
SQL isolation, backup/PITR, hard-crash recovery, Linux behavior, or power-loss safety.
Primary checks and expected index groups are logical SQL checks, not a forensic
enumeration of every physical index entry.

The separate packaged 24-hour kernel key/value canary remains separate evidence.
Do not silently extend this smoke to 24 hours or run another heavy campaign beside
it. Next gates are longer progress/resource sampling after short-policy gates pass,
followed by a 30-minute run and an explicitly approved
long run. Preserve engine candidate identity separately from harness source hashes;
adding this test does not create a new qualified production candidate.
