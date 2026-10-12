# Exact Checkpoint Publication Crash Matrix

This complements [SQL process-kill recovery](SQL_CRASH.md). Instead of racing
an external kill against a checkpoint, a tagged child executable hard-exits
at an exact successful I/O boundary. No defer, file close, manager drain or
staging cleanup runs in that child.

## Isolation

Only an explicit `-tags kitdbfault` build enables the internal
`kitdb/internal/checkpointfault` probe. Ordinary builds compile `Reach` as an
empty function, with no environment reads or fault behavior. A regular test
proves setting fault environment variables cannot arm the ordinary build.
No public API, storage format, database option or dependency is added.

Tagged helpers arm the exact database path and point only after setup and SQL
ACK journaling finish. A synced point marker and exit code 73 prove which
boundary was reached; marker failures exit 74 and fail the test. The marker
and ACK journal are test evidence, never consulted by database recovery.

## Boundaries

- `append-body-written`: segment and manifest written, before data sync.
- `append-body-synced`: data synced, before alternate superblock publication.
- `superblock-written`: alternate slot written, before slot sync.
- `superblock-synced`: alternate slot synced, before installing the new reader.
- `compact-staging-synced`: complete compacted staging synced, before replace.
- `compact-main-replaced`: main replaced, before directory sync.
- `wal-staging-synced`: new WAL staging prepared and synced, before old WAL close.
- `wal-replaced`: WAL replaced, before directory sync and append-handle reopen.

The WAL cases run after both incremental and full checkpoint publication,
making ten cases. All incremental cases retain an actual SQL transaction
snapshot. Full-compaction fixtures first reach the existing 32-segment limit,
then publish fresh SQL mutations with no reader blocking compaction.

## Oracle

Each child commits 32 two-row SQL inserts through the native SQL adapter over
one managed kernel, syncing its ACK journal after each success. Final
checkpoint execution uses the node's existing typed maintenance scheduler.

The parent requires the exact marker and hard-exit code, validates all ACKs,
opens a fresh owner and requires:

1. Kernel `Verify` and exact last-transaction equality.
2. The old checkpoint transaction before publication, or the newly published
   transaction after successful slot write/replace. These are process-crash
   expectations, not power-loss guarantees for unsynced writes.
3. Exactly 64 expected rows, including every ID, batch and complete payload.
4. Every batch equality query returning its exact two rows; `EXPLAIN ANALYZE`
   must use `index-scan` and inspect two secondary-index entries.
5. Published full compaction containing one main segment.
6. Actual abandoned staging at the staging boundaries, left untouched while
   recovery reads, writes a new two-row batch, checkpoints, verifies and
   reopens once more. All 66 rows and index entries must then match.

Recovery deliberately ignores unpublished `.kitdb-main-*.tmp` and
`.kitdb-wal-*.tmp` files. This test does not add directory-wide cleanup: names
may belong to another database sharing the directory, so deleting them by
glob would be unsafe.

## Run

From `engine/`:

```powershell
go test -race -tags kitdbfault ./kitdb/relational -run '^TestSQLCheckpoint(PublicationCrash|CrashHelper)$' -count=10 -timeout=5m
go test ./kitdb/internal/checkpointfault -count=1
```

Each child has a 25-second test timeout and a 30-second parent deadline.
The runtime-hardening CI matrix runs three repetitions on Windows and Linux
before the broader focused race gate. Configuring CI is not evidence that a
remote run has passed.

## Limits

These are exact process-exit boundaries after successful writes, not injected
short writes, corrupted bytes, failed `fsync`, or actual power loss. In
particular an unsynced superblock write visible after process exit does not
prove it survives a machine losing power. The separate kernel corruption and
recovery tests remain necessary. No network filesystem or release-wide
qualification is implied.

Returned-error behavior now has its own
[storage I/O failure matrix](STORAGE_IO_FAULTS.md). Its simulations do not
turn either matrix into physical device or power-loss qualification.

## Local Evidence (2026-10-11)

On Windows with the local filesystem, the final ten-case matrix passed ten
race repetitions in 52.248 seconds: 100 exact-point child exits and 3,200
acknowledged two-row SQL transactions. Every case passed old/new checkpoint
selection, primary rows, secondary-index execution, `Verify`, and the next
write/checkpoint/reopen with no pre-recovery staging deletion.

All ordinary `go test ./kitdb/...` packages passed, including kernel (17.352s),
node (10.176s), relational (48.637s), and KitSQL (6.370s). That suite started
before the disabled-probe regression test was added; the regression passed
separately afterward. Existing kernel and node checkpoint tests passed three
ordinary-build race repetitions (4.855s and 7.376s). Tagged KitDB vet and the
ordinary whole-engine build passed.

No Linux execution, physical power-loss drill, remote CI result or full
release campaign was obtained locally. The working tree remains uncommitted.
