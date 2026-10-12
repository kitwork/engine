# KitDB Transaction Concurrency

## Current Contract

KitDB uses fixed snapshots with optimistic physical key/range validation for
audited standalone SQL paths, and a database-wide fence for unsupported or
oversized dependency sets. This is not row-version-chain MVCC.

A standalone relational transaction captures catalog and data together, then
reads that immutable snapshot plus its private bounded mutation overlay.
Uncommitted changes are visible only to their transaction. Later committed
changes do not refresh an existing snapshot. Rollback publishes nothing.

A mutation-bearing commit validates the catalog revision and submits its
complete write-set through `CommitWithDependencies(base, dependencies)`. The
kernel commit writer rejects intersecting mutations, catalog changes, or missing
journal coverage, including earlier accepted requests in the same batch. Coarse
transactions reject any intervening data/catalog mutation. Kernel-owned sequence
reservations are the existing narrow exception, not a general bypass.

The standalone relational no-mutation commit returns its original boundary
without validating later changes; it is completion of an older read snapshot,
not proof that the database stayed unchanged. Do not assume all frontends have
identical read-only catalog validation policies.

DDL on the same relational facade waits for active transaction read leases.
A second facade can publish DDL over the same kernel; the old writer must then
fail its catalog fence. Facades share storage, not a second file owner.

## Snapshot Admission

Admission also checks the per-DB byte budget shared by active snapshots and
reservations. Frontend mutation writes share a separate aggregate budget with
kernel prepared transactions. See [memory budgets](MEMORY_BUDGET.md) for the
accounting model, configurable defaults, validation, and excluded memory.

The kernel shares one 32-slot budget across active snapshots and outstanding
reservations, including independent relational facades attached to the same DB.
`ReserveSnapshot(ctx)` waits FIFO with a 128-caller queue and a two-second wait
per attempt; cancellation removes the waiter and releases any concurrently
granted reservation. An unused reservation must be closed. Waiting owns no
snapshot file or overlay and holds neither the database state lock nor the
relational commit gate. Capture consumes the reservation under that gate, so
catalog and data still describe a consistent boundary. Closing the DB wakes
queued callers and revokes unused reservations.

`Snapshot()` remains nonblocking and returns `ErrTooManySnapshots` when the
shared budget is full. `SnapshotContext(ctx)` is the waiting convenience API.
Standalone relational/native SQL/KitSQL transaction entry uses reservations;
other direct snapshot consumers retain their existing nonblocking behavior.
Admission does not alter commit validation, WAL durability, or the on-disk format.

Tests in `snapshot_admission_test.go`,
`relational/snapshot_admission_test.go`, and
`kitsql/admission_network_test.go` cover FIFO, queue bounds, cancellation/grant
races, timeout, shutdown, commit progress under saturation, and rejected writes
over HTTP. Stats expose active snapshots, outstanding reservations, and waiters
separately; all must drain when work completes.

## Executable Baseline

`relational/concurrency_contract_test.go` covers both commit orders for:

- Same-row lost updates and absent primary-key claims.
- Disjoint row updates: both commit. Inserts/deletes may still intersect on
  shared table-count metadata; physical independence is stricter than different IDs.
- Write skew through an aggregate predicate and through primary-key reads.
- Empty indexed ranges and existing records moved into those ranges.
- Unique-value claims under different primary keys.
- Parent deletion raced against insertion of a referencing child.

Each case checks exact visible data, releases every snapshot, closes storage,
and verifies the winner's data again after reopen. These are deterministic
overlapping transactions; their commit calls intentionally run in fixed order.
A separate real goroutine race starts two commits together and verifies exactly
one winner, one conflict, and no partial publication of rows or audit records.

Additional tests cover dirty-read exclusion, repeatable snapshot reads,
read-your-writes, rollback, and external catalog invalidation. Existing tests
continue to cover savepoints, triggers, referential actions and sequence fences.

```sh
go test -race ./kitdb/relational -run '^TestTransactionConcurrency' -count=10 -timeout=3m
go test ./kitdb/... -count=1 -timeout=10m
```

## Bounded Dependency Tracking

Writable standalone relational transactions now record physical point access
and range reads under their own mutex. Tracking includes `Get`, `getWithStats`,
`scan`, `scanKeys`, and successful staged Put/Delete operations. A point miss is
still a read dependency. Empty or early-stopped scans retain the full requested
Start/End/Prefix interval; LIMIT and direction never narrow it. Repeated ranges
and keys are deduplicated, with read/write access bits merged for each key.

Each transaction retains at most 4096 distinct point keys, 256 distinct ranges,
and one MiB of copied key/bound bytes. Map/slice overhead is additionally bounded
by those entry caps; one MiB is not a total-heap-size promise. Caller buffers do
not alias retained evidence. Explicit read-only transactions skip tracking,
including eligible native/KitSQL snapshot streams.

Exhaustion discards the dependency containers and sets a sticky coarse-fence
flag. Later reads/writes do not restart tracking or reject otherwise supported
queries. Savepoint and failed-statement rollback conservatively retain previous
dependency evidence. Commit, conflict, cancellation and rollback release it.

The collector now supplies the kernel validator for standalone relational
transactions, including native SQL, pgwire and KitSQL's per-request execution.
KitSQL v1 still has no multi-request transactions. The Kitwork ORM's separate
record transaction implementation is not migrated by this change.

`relational/dependencies_test.go` checks misses, overlay reads, blind writes,
stats paths, empty ranges, early stop, caller-buffer ownership, rollback,
concurrent reads, all exact limits, sticky fallback, and state release.

The repeated-access allocation test requires zero allocations once the key or
range is already tracked. A local Go 1.26.0 Windows/amd64 microbenchmark measured
about 9.7 ns for a repeated key, 10.4 ns for a repeated range (both zero B/op),
and 22.7 us / 27792 B/op for a new set of 256 point keys. These isolate the
collector, not total SQL latency or resident RAM. Full KitDB tests, host database
integration tests, package vet and engine build passed after integration;
dependency/concurrency tests also passed ten repetitions under the race detector.

## Kernel Commit Validator

The Go kernel now supports `Tx.CommitWithDependencies(base, dependencies)`.
`CommitDependencies.Keys` must include every physical point read, including
misses; `Ranges` must include all predicate ranges. Tx automatically adds every
staged write key. `Coarse: true`, oversized input, and incomplete retained
mutation coverage preserve the existing conservative conflict fence. This is a
trusted adapter API: omitting reads can permit anomalies, and the kernel cannot
infer SQL/application decisions from caller-supplied dependencies.

Validation runs in the existing commit writer before WAL append. It checks
published mutations and each earlier accepted request in the same batch. Any
catalog mutation conflicts conservatively; tombstones and delete/reinsert/ABA
histories remain mutations even when values return to their original bytes.
Sequence-only reservations retain their existing conflict exemption.

The database owns one lazily activated, process-local journal, guarded by
`commitMu`. It records only mutation keys/catalog flags after successful WAL
sync, with caps of 512 frames, 8192 key entries and one MiB of key bytes.
Eviction advances a coverage watermark. A transaction older than coverage
cannot be declared independent. A single oversized mutation drops coverage
without retaining its keys. No row values, WAL bodies, global key-version map
or persisted sidecar is introduced. Ordinary commits do not activate the
journal; once activated, all ordinary data writers participate in its history.
Standalone SQL mutation commits now opt in and therefore activate this journal.

Normalization copies dependency input and caps it at 4096 point keys, 256
ranges and one MiB of key/bound bytes, including automatic write keys.
Dependency payload plus a fixed metadata allowance is charged to the existing
aggregate transaction admission budget. Reservation failure leaves the Tx
available for rollback/retry; submitted commits release that charge on success
or failure. Entry/payload caps bound storage independently of this allowance;
it is not an exact Go heap accounting claim.

Close releases the journal. Reopen starts with no journal, so an old boundary
with intervening untracked data changes conflicts. Recovery still derives
durability solely from main storage and WAL. An uncertain WAL sync never
publishes journal entries and fences the handle as before.

`conflict_validation_test.go` checks disjoint/dependent concurrent writers,
same-batch ordering, point misses, blind writes, range endpoints/intersections,
catalog changes, tombstones, mutation retention, caller-buffer ownership,
admission cleanup, future boundaries, sync failure and fresh-owner recovery.

## SQL Integration Boundary

Primary/unique lookups, secondary-index scans, row scans, index hydration,
row-layout metadata and constraint probes use the tracked transaction reader.
Unique claims include missing physical keys. Foreign-key parent lookups and
reverse-reference scans use that same reader. All staged physical writes,
including index entries and table-count metadata, participate in validation.
Savepoint and failed-statement rollback retain their read evidence.

The following paths explicitly select sticky coarse validation:

- Base-table aggregate SELECT and direct KROW/KCOL batch scanning (including skipped chunks).
- JOIN SELECT, pending a narrower independent coverage contract.
- Writes with catalog triggers, pending an audited trigger execution contract.
- EXPLAIN, PRAGMA and sequence SELECT, whose observations include non-row state.
- Collector exhaustion and missing/expired kernel mutation history.

SEARCH remains autocommit-only; it cannot authorize a mutation inside a writable
transaction. Result caching belongs to engine autocommit, not transactional reads.
DDL, ANALYZE, reindexing and projection maintenance own separate engine paths;
they do not bypass the SQL transaction reader. Catalog invalidation remains
conservative regardless of the selected data-validation path.

`relational/dependency_validation_test.go` proves point/unique misses, empty and
LIMIT ranges, independent reads, sticky fallback, failed-statement/savepoint
evidence, mixed raw-kernel writers, same-byte writes, delete/reinsert ABA,
journal exhaustion, and simultaneous independent writers through two facades.
Native `database/sql` tests also distinguish independent updates from a writer
whose earlier SELECT depends on the other transaction's modified row.
The earlier constraint/lost-update/write-skew safety oracles remain intact.

Next work should measure write-heavy/large-scan overhead and selectively audit
coarse paths. Shared metadata must not be skipped merely to improve concurrency;
commutative count/statistics operations require a separate correctness design.

## Status

The baseline, bounded collector, kernel validator and conservative SQL integration
are implemented. No tenant JS API, wire protocol, WAL or file version changes.
This is a scoped concurrency improvement, not a claim that every SQL plan is
key-granular or that the database has completed production qualification.

Local verification on 2026-10-10, Windows/amd64: the new contract suite passed
ten repetitions under the race detector; the full KitDB suite and
`go vet ./kitdb/relational` also passed. This is local checkout
evidence, not cross-platform or long-duration concurrency qualification.

Kernel validator verification on the same platform passed ten race-detector
repetitions, plus ten repetitions of exact normalization-limit checks. The
full KitDB suite, package vet and full engine build passed. `kitdb-verify`
passed all eleven steps in 143416 ms, with local evidence at
`.artifacts/kitdb-dependency-validator-20261010.json`. That gate includes
durability/recovery, standalone commerce, backup/PITR and host database journeys.

SQL integration verification on 2026-10-10 passed ten race-detector repetitions
of the dependency/concurrency suites, plus ten repetitions through the native
`database/sql` adapter. Full engine build and KitDB vet passed. `kitdb-verify`
passed all eleven steps in 156722 ms; its kernel/node step includes the full
KitDB/KitSQL suite. Local evidence is
`.artifacts/kitdb-sql-dependency-validation-20261010.json`.

Three one-second single-row UPDATE microbenchmark samples measured 434-521 us/op,
about 44255 B/op and 261 allocations with fine validation; forced coarse
validation measured 515-537 us/op, about 43139 B/op and 244-245 allocations.
The measured allocation increment is about 1.1 KiB and 16-17 allocations per
transaction in this fixture. Local filesystem sync and scheduling dominate the
timings: these samples do not establish a throughput speedup. They do not replace
write-heavy/large-scan campaigns, Linux evidence or long-duration qualification.
