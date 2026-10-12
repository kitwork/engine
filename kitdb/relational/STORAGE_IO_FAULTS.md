# Storage I/O Failure Matrix

This complements [process-kill SQL recovery](SQL_CRASH.md) and
[exact checkpoint publication exits](CHECKPOINT_CRASH.md). It exercises
returned I/O failures and the existing caller's error handling rather than
hard-exiting the process at successful I/O boundaries.

## Isolation

Fault behavior exists only with `-tags kitdbfault`. Ordinary builds compile
the internal checks to no-ops; tests prove matching environment settings
cannot arm either a crash or an I/O error in an ordinary build. No public
database options, dependencies, scheduler or storage format are added.

Only the configured exact database path and named point are selected. A
short-write fault writes a real half-buffer prefix and returns
`io.ErrShortWrite`; the remaining bytes are never passed to the normal
write-all loop. The disk-full case returns `syscall.ENOSPC` before writing
bytes. Other failures return `syscall.EIO` before the selected sync, replace
or prepare operation. A per-database marker records each hit; tests require
exactly one hit and no automatic retry after an uncertain result.

These are deterministic simulations. They do not fill the physical disk,
make a real device fail `fsync`, or prove behavior under machine power loss.

## Cases

`TestSQLStorageIOFaultMatrix` has 17 cases:

- WAL write: actual incomplete prefix, and simulated no-space rejection.
- WAL sync: complete frame written but no successful durability response.
- Incremental manifest: partial write and pre-publication data-sync failure.
- Alternate superblock: partial slot write and post-write sync failure.
- Full compaction: partial manifest, staging sync, replace, directory sync.
- WAL rotation: prepare, replace and post-replace directory-sync failures,
  each after both incremental and full checkpoint publication.

One node manager owns the file; native SQL borrows an attached relational
facade. Checkpoint faults run through the existing maintenance ticket. Full
fixtures reach the current 32-segment boundary before injection.

## Required Outcomes

Known pre-publication checkpoint failures are not labeled uncertain: the
already-acknowledged SQL rows and secondary index remain readable, resources
drain, and an explicit subsequent write/checkpoint succeeds after disarming
the fault. A failed WAL replacement must restore the old append handle.

WAL write/sync, slot write/sync and post-replace directory-sync failures are
conservatively `ErrDurabilityUncertain`. The owner must reject another write
and checkpoint with `ErrUnavailable`, preserving the underlying error cause.
Even a simulated WAL no-space rejection is conservative because the generic
commit writer cannot safely infer the effects of every real failing writer.

After all SQL connections, facade, lease and manager close, a fresh owner
must pass kernel `Verify`, exact primary rows/payloads, and every batch's
secondary-index query. `EXPLAIN ANALYZE` proves `index-scan` execution and
exact entry counts. The new owner must also write and checkpoint successfully.

The incomplete/rejected single WAL frame must recover neither candidate row.
The WAL-sync case permits either zero or both candidate rows: no ACK is not
proof of rollback. The candidate insert is never replayed; the recovery probe
updates an existing known row instead. Earlier acknowledged rows must survive
every case, and no partial two-row transaction is accepted.

`TestWALIOFaultDrainsCommitResources` adds three kernel cases. It directly
checks active transaction bytes, queued commits, snapshots, reservations,
waiters and their memory charges after failure and snapshot release. Close
must stop the commit worker. The SQL matrix separately checks released pool
connections and drained manager leases/jobs; it does not bypass unavailable
kernel `Stats` to expose new production diagnostics.

## Run

From `engine/`:

```powershell
go test -race -tags kitdbfault ./kitdb ./kitdb/relational -run '^(TestWALIOFaultDrainsCommitResources|TestSQLStorageIOFaultMatrix)$' -count=10 -timeout=5m
go test ./kitdb/... -count=1 -timeout=5m
```

The Windows/Linux runtime-hardening CI matrix runs three repetitions before
the broad focused race gate. These tests remain sequential because tagged
fault configuration is scoped by test-controlled environment variables;
there is no global mutable fault registry.

## Local Evidence (2026-10-11)

Windows/local filesystem: the final 17-case SQL matrix passed ten race
repetitions (170 injected failures, 89.808s). The three kernel resource cases
passed ten race repetitions (30 failures, 2.692s). No unexpected partial
transaction, missing acknowledged row, index mismatch or reported race was
observed. No existing durability algorithm needed changing for these cases.

The earlier 14-case SQL matrix and ten-case exact-publication crash matrix
also passed three combined race repetitions (38.572s), after the I/O call-site
changes. Thus the new fault checks did not prevent the prior exact-point
child exits from being reached and recovered.

All ordinary KitDB package tests passed, including kernel (17.133s), node
(10.957s), relational (54.844s), KitSQL (11.207s), and the disabled-fault
regression (0.487s). Tagged KitDB vet and ordinary whole-engine build passed.

CI configuration now includes both matrices on Windows/Linux, but no remote
CI or Linux result was obtained in this local change. No physical disk-full,
power-loss drill, long canary or full release qualification was performed.
Changes remain uncommitted.
