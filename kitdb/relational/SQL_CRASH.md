# SQL Process-Crash Recovery

`TestSQLHardCrashRecovery` launches a separate test executable that owns a
PostgresNode and a loopback pgwire listener. The parent client commits 32
two-row transactions and writes each received ACK to an independently synced
JSONL journal. The parent forcibly kills the server process without running
database, node, listener or test cleanup in that process.

The regular bounded matrix covers:

- Kill after acknowledged transactions, without a clean close.
- Kill with a two-row transaction staged but not committed; neither row may
  survive.
- Kill in the COMMIT client-call window. A missing reply permits either zero
  or both rows, never a partial transaction. A successful reply requires both
  rows and is added to the ACK journal. No ambiguous request is replayed.
- Kill after issuing a managed checkpoint while a SQL reader retains its
  snapshot. This is a scheduling-window test: the checkpoint may be queued,
  running or already completed when kill arrives. It does not prove a kill
  at a particular append, superblock, rotation or full-compaction boundary.

Every case recovers with a fresh standalone owner, runs kernel `Verify`,
compares the exact permitted row count, and checks IDs and complete payloads
through every batch equality query on the declared secondary index. An
`EXPLAIN ANALYZE` assertion requires the `index-scan` path and two scanned
entries for the first batch. Unknown
outcomes are restricted to the single two-row transaction deliberately sent
by this client; arbitrary extra rows are not permitted.

Run from `engine/`:

```powershell
go test ./kitdb/relational -run '^TestSQLHardCrash' -count=10 -v -timeout=5m
go test -race ./kitdb/relational -run '^TestSQLHardCrash' -count=10 -v -timeout=5m
```

The helper skips when run directly. Each child has a 35-second test deadline
and a 40-second parent process deadline. Tests use only temporary directories,
loopback listeners and the existing PostgreSQL driver. No public API, storage
format, production fault hook or engine dependency is added.

Process kill is not physical power loss. This campaign does not qualify
dishonest `fsync`, network filesystems, Linux behavior, or exact checkpoint
publication boundaries. Those require separate evidence.

The separate [exact checkpoint publication matrix](CHECKPOINT_CRASH.md)
now supplies process-exit evidence at specific append, superblock, compaction
and WAL-rotation boundaries using an explicitly tagged fault-lab build.

## Local Evidence (2026-10-11)

Windows local-filesystem run: all four cases passed ten repeated race runs
on the final test, including the explicit secondary-index execution assertion
(40 killed server processes, 1,280 confirmed two-row transactions). The final
race command passed in 14.661 seconds. An earlier ordinary run recovered both
rows of the COMMIT-window transaction despite receiving no successful client
reply, demonstrating an allowed unknown outcome rather than lost atomicity.

The full node, relational and KitSQL suites passed (17.097, 82.364 and 7.184
seconds respectively); that suite started before the final index-plan
assertion was added, which the final repeated race run covers. KitDB vet
passed. Whole-engine build exited successfully, but emitted an access-denied
warning writing the external Go module stat cache in the sandbox; that warning
was not treated as a database defect. No full release gate or production
canary was run in this change.
