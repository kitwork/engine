# Managed SQL Authorization Canary

This is the managed-root companion to [SQL_CANARY.md](SQL_CANARY.md), not a
replacement for its original smoke/pressure modes. It reuses the independent
ACK/data/index oracles, typed conflict/admission classifier and per-adapter
progress monitor. Native SQL, real loopback pgwire and real loopback HTTP
KitSQL share one authenticated PostgresNode and its existing relational owner.
No adapter opens the storage file independently.

## Run

From the engine directory in PowerShell:

```powershell
go test ./kitdb/kitsql -run '^(TestSQLAuthCanaryOracles|TestExecutionErrorClassification)$' -count=1

$env:KITDB_SQL_CANARY_AUTH = '1'
go test -race ./kitdb/kitsql -run '^TestSQLCanaryAuthorization$' -count=1 -v -timeout=8m
```

Defaults are one round of three six-second workload phases. For a finite
three-minute phased soak:

```powershell
$env:KITDB_SQL_AUTH_PHASE_SECONDS = '20'
$env:KITDB_SQL_AUTH_ROUNDS = '3'
$env:KITDB_SQL_AUTH_REPORT = 'D:/qualification/sql-auth.json'
go test ./kitdb/kitsql -run '^TestSQLCanaryAuthorization$' -count=1 -v -timeout=12m
Remove-Item Env:KITDB_SQL_CANARY_AUTH, Env:KITDB_SQL_AUTH_PHASE_SECONDS, Env:KITDB_SQL_AUTH_ROUNDS, Env:KITDB_SQL_AUTH_REPORT
```

Report paths are optional, absolute and must have an existing parent directory.
Retain stdout as well as JSON; `-count` invocations overwrite the same report
path, and simultaneous tests must not share that path. Schema/format v1 includes
OS, architecture, Go version, UTC start time, elapsed duration, per-phase ACK/
denial/conflict/admission counts, samples, and both closed-owner receipts.

Seconds must be 2..30 and rounds 1..4; malformed or out-of-range values fail.
The maximum is 360 seconds of actual workload, plus setup/control/recovery.
The outer context is requested workload plus five minutes, never an unlimited
background process. Phase duration excludes completion of already-started
operations (`DrainMS`) and the between-phase control/verification work.

The existing release runner explicitly enables the default short profile in
verify modes, and its race variant in release modes. `kitdb-auth` now has eight
steps including both variants. Required JSON PASS events prevent an opt-in
skip from being called a pass. Gate budgets are fixed at six seconds and one
round, independent of inherited shell settings; their report-path override is
cleared so they do not overwrite a caller's manual report.

## Fixture and Phases

The fixture contains three writers and a read-only auditor, plus a control admin.
All non-admin root accounts have only CONNECT and explicit local-role mappings.
Each writer owns a different four-row table, with a version index and a fixed
9 KiB payload. A fourth four-row table is hidden from all non-admin accounts.
Each adapter owns its own keys and independent model; versions advance only
after confirmed successful commit responses. Unknown outcomes fail; the harness
does not replay writes. Typed conflicts and known admission failures are counted
without advancing the model.

Every round checks:

1. **Allowed:** all writers update/read their own keys. A native read-only
   transaction pins a version before workers start, waits for a native write ACK,
   retains its old value after a 150 ms hold and rolls back.
2. **Revoke:** with workers paused at a barrier, a pgwire transaction stages one
   increment before UPDATE is revoked from the pgwire and HTTP roles. Its pinned
   commit must succeed and enter the model. The next workload phase requires
   pgwire/HTTP writes to be denied and their reads to remain allowed, while the
   native writer continues. A single unauthorized write ACK fails the test.
3. **Restore/reconnect:** UPDATE is restored. A native root role mapping is
   removed: the already-captured native logical pool must still write, but fresh
   native/pgwire/HTTP clients for that account must fail reads and zero-row writes.
   The mapping is restored, all three writer pools are retired/reopened, and all
   adapters must make write/read progress again.

Workers stop **between** operations and finish in-flight work before a barrier.
Administrative mutations do not race unsynchronized client expectations. This
is deterministic permission-phase coverage under workload, not arbitrary
revocation-timing chaos. Revoke is not a commit barrier and native pool mapping
capture is intentional; see [REVOCATION.md](../managed/REVOCATION.md).

At barriers every writer and the auditor compare full rows/payloads and indexed
version groups with the independent model. Hidden-table and cross-table probes,
including zero-row writes, must receive typed permission errors. After all rounds
an HTTP-role revoke is left durable. The original owner closes, an exclusive
offline kernel open runs VerifyOnOpen/Verify, and a fresh authenticated node
rechecks data, index groups, role IDs, mappings and the persisted denial through
the adapters. The reopened owner must close cleanly as well.

## Checkpoints and Resource Evidence

There is no explicit checkpoint worker or forced recovery checkpoint in this
profile. It uses the node's ordinary SQL checkpoint policy. At least one manager
checkpoint completion must be sampled during an active phase with write ACK
progress; missing coverage fails, rather than silently extending duration.
A completion may have been queued earlier; the receipt does not identify its
causal request or prove full compaction/publication while a snapshot is held.

Limits are 16 authenticated sessions, two open databases, 2 MiB total managed
page-cache reservations, 1 MiB snapshots and 8 MiB transaction memory per user
database. HTTP transport and handler concurrency are bounded too. Cache
reservations are not process RAM; samples report heap without claiming a heap
ceiling or proving a long-term plateau. Samples are retained at most once per
second (at most 372 with supported settings), and fixed rows are reused.

The monitor checks each adapter separately, using denial progress for revoked
writers, with a 30-second stall window and a three-second monitoring-gap limit.
Short phases cannot demonstrate thirty seconds of liveness; every phase still
requires positive expected progress and reads from every adapter.

Before owner close, all results/transactions and client pools are retired,
HTTP handlers drain, then pgwire stops. Both closed-node receipts require zero
registered sessions, managed/opening engines, projection leases/handles, open
database handles, leases, waiters, cache reservations and maintenance work.
The private kernel snapshot counters are not exposed by PostgresNode, so this
does not pretend to sample them on the live owner. Pinned transaction cleanup,
closed ownership and exclusive offline verification provide the bounded evidence.

## Permission Error Contract

Statement-level SQLSTATE 42501 now maps to HTTP 403 / `KSQL_FORBIDDEN`, matching
database-level denial. It is not retryable. Syntax errors and message-only
lookalikes remain 400 / `KSQL_EXECUTION`; uncertain durability keeps its existing
500 / `KSQL_DURABILITY` precedence. Negative unit tests fence these distinctions.
Authentication failures are not accepted as successful permission probes.

This is a production error-code mapping change: results for a previous source
variant must not be relabeled as qualification of this patch. No SQL privilege,
transaction or storage semantics are relaxed to make the workload pass.

## Limits

Temporary roots and ephemeral loopback ports only. Existing demo databases and
listeners are not selected. Windows tests need atomic rename permissions.
Graceful reopen here is not a process crash; the standalone crash campaign is
separate. This is not a new 24-hour canary, clean-release qualification, Linux,
distributed sync, physical power-loss or general stability claim.
