# KitDB Snapshot and Transaction Memory

## Ownership and Limits

One kernel DB owns the accounting. Independent relational facades, native SQL,
pgwire, and KitSQL adapters attached to it share the same budgets; opening a
second file owner is not a way to get another budget.

`OpenWithOptions` accepts two positive byte budgets. Zero selects the default,
64 MiB for each; negative values are rejected before files are opened.

```go
db, err := kitdb.OpenWithOptions("app.kitdb", kitdb.OpenOptions{
    PageCacheBytes:         1 << 20,
    SnapshotMemoryBytes:    8 << 20,
    TransactionMemoryBytes: 16 << 20,
})
```

These are trusted Go owner options, not a new tenant-facing JS API. Existing
`app.database` memory/page-cache configuration is not silently reinterpreted.
Budgets are process-local; no storage-format migration is needed.

## Snapshot Admission

A snapshot charge is 256 base bytes plus, for each current WAL-overlay entry,
128 overhead bytes and the key/value byte lengths. Deleted entries charge a key
and metadata but no value. Keys are charged even though immutable string bytes
may be shared. This is conservative accounting for the copied overlay, not an
allocator-exact prediction. The main file is not copied into each snapshot.

Recovery calculates the current overlay cost once. Commit updates it from each
mutation, including repeated overwrites/deletes in a single transaction.
Checkpoint resets the current estimate and wakes eligible waiters. Request
admission therefore checks bytes in O(1), without scanning the overlay.

Active copies and outstanding reservations together must fit both the 32-slot
limit and `SnapshotMemoryBytes`. A reservation charges the current estimated
cost without allocating an overlay or opening a snapshot file. Capture rechecks
the current cost, because writes may grow the overlay after reservation. Failure
releases the reservation before returning `ErrSnapshotMemory`.

FIFO admission remains bounded to 128 waiters and two seconds per attempt, or
the caller's shorter context. If even one current overlay cannot fit, admission
returns `ErrSnapshotMemory` immediately; queued callers are also rejected when
that condition is observed. Raw `Snapshot()` remains nonblocking.

Checkpoint can shrink the cost of new snapshots but cannot reclaim the old
copies that active readers still own. Closing those readers returns their exact
accounted charge once; reservation cancellation and shutdown do likewise.

## Transaction Writes

The existing kernel prepared-payload counter and frontend write-set counter
share `TransactionMemoryBytes`. Relational transactions charge an operation
before cloning its storage: 128 overhead bytes, twice the key length (operation
and overlay key), and its value length. Repeated writes are charged separately
because the operation log retains them. Per-transaction operation and payload
limits remain in force, independently of the aggregate budget.

Rollback to a savepoint zeroes discarded operation references, rebuilds the
remaining overlay, and returns the discarded charges. Rollback, commit success,
commit failure, and kernel shutdown release remaining charges with the owning
snapshot. Kernel preparation also needs space while the frontend writes are
still alive; a transaction can therefore fit while staging but fail admission
when preparing its commit. That failure publishes no partial write.

KitSQL maps snapshot and transaction memory exhaustion to `KSQL_BUSY` / 503.
Retry requires bounded backoff and enough reclaimed capacity; the driver never
automatically repeats a write. A deterministic oversized workload should be
reduced, checkpointed where appropriate, or given an explicitly larger owner
budget, not retried indefinitely.

## What This Does Not Bound

The budgets are not a whole-process RSS cap. They exclude the owning live WAL
overlay, page cache (separately bounded), catalog graphs, query materialization,
temporary scan/commit copies, dependency tracking, client/server buffers, and Go
allocator/GC overhead. The kernel payload counter is a serialized-byte charge,
not a measurement of all kernel transaction allocations. Multi-database hosts
also need fleet-level policy; these limits are per DB, not a global RAM quota.

The budgets themselves do not introduce an automatic checkpoint timer. Writes can grow the
live overlay beyond a snapshot budget; maintenance/checkpoint policy must keep
that live state appropriate for the deployment. Increasing the budget is not a
substitute for checking the machine's actual memory and request lifetime.
Managed writes now evaluate [checkpoint pressure](node/CHECKPOINT_PRESSURE.md)
through the existing maintenance manager, including overlay bytes and age.
Standalone callers must invoke that evaluator explicitly; idle files are not
automatically polled.

## Evidence and Reproduction

`memory_budget_test.go` covers snapshot byte saturation below the slot limit,
checkpoint wakeups with older snapshots retained, capture-time growth, recovery,
repeated-key accounting, invalid configuration, and shared kernel/frontend
transaction pressure. `relational/memory_budget_test.go` checks cross-facade
pressure, savepoint cleanup, and a rejected commit followed by a successful
small write without partial publication. Existing admission tests cover FIFO,
cancellation/grant races and shutdown; all run under the race detector.
`kitsql/memory_network_test.go` pauses a multi-row response with a 2 MiB value,
checks that its snapshot remains charged, cancels another reader waiting for
bytes, checkpoints while the old snapshot is alive, then checks restored
admission and complete drain. A single-row producer may finish before a slow
socket releases its buffers; those buffers are not snapshot-owned memory.

```powershell
go test -race ./kitdb ./kitdb/relational ./kitdb/kitsql `
  -run 'TestSnapshotMemory|TestTransactionMemory|TestRelationalMemory|TestSnapshotAdmission|TestExecutionError|TestKitSQLSnapshotAdmission' `
  -count=5 -timeout=3m

go test ./kitdb -run '^$' -bench '^BenchmarkSnapshotOverlayMemory$' `
  -benchmem -benchtime=200ms
```

2026-10-11 Windows/amd64, Go 1.26.0, same-process local measurements:

| Overlay payload | Accounting per snapshot | Allocated bytes/op | Allocations/op |
| --- | ---: | ---: | ---: |
| 1 MiB (256 entries) | 1,083,392 | 1,076,629 | 266 |
| 8 MiB (2,048 entries) | 8,665,344 | 8,618,905 | 2,064 |

These measurements motivated the metadata allowance. They are not proof of an
upper physical-memory bound on every Go release or workload. Verification jobs
overlapped the run, so timings are not used to claim a speedup. See the local
ignored artifact `.artifacts/kitdb-snapshot-memory-bench-20261011.txt`.

The real TCP campaign supports `KITDB_NETWORK_PAYLOAD_REPEATS=1..64` (default 8)
to increase row payload size while keeping 10,000 rows. Its drain oracle now
also requires zero snapshot bytes, reserved bytes, frontend write bytes, and
kernel payload bytes. See [network campaign](kitsql/NETWORK_LOAD.md).

The final 24-second-per-phase run (120 seconds requested) used 832-byte row
payloads, 64 native connections, and the calibrated 128-byte entry allowance.
All 18,866 acknowledged writes matched both row and secondary-index oracles
before/after reopen; storage verification passed and unexpected errors were zero.
All byte counters and admission waiters were zero after drain. There were 40
successful background checkpoint calls. Combined client/server Go heap peaked
at 90.9 MiB and returned to 1.60 MiB after close/GC, versus 1.37 MiB before;
this is not RSS or long-duration leak proof. The same larger-payload campaign
under the race detector passed with 1,021 acknowledged writes and exact reopen
validation. Neither result claims a speedup or a whole-process memory ceiling.

Local reports:

- `.artifacts/kitdb-network-memory-final-20261011.json`
- `.artifacts/kitdb-network-memory-race-20261011.json`
- `.artifacts/kitdb-memory-20261011.json`: all 11 KitDB release checks passed.
