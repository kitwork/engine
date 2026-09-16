# KitDB Fault Lab

The fault lab turns restart safety into executable evidence. It does not add a
production failpoint or another durability mechanism. Each regular matrix
launches the current Go test binary as a child process, completes one exact
durability stage, and calls `os.Exit` without running defers, closing the node
manager, unlocking lifecycle mutexes, or closing database handles. The parent
then starts a fresh runtime and treats only engine-owned files and durable
cursors as truth.

The Production Supervisor adds no durable scheduler record or publication
stage. Its restart oracle re-registers process-local policy, rediscovers a fully
verified immutable anchor, reconciles through the existing resumable verified
backup operation, and performs a new logical-digest restore drill. Hard-process
truth for anchor publication, WAL/checkpoint/history recovery, and pin resume
therefore remains in the underlying kernel and node backup tests rather than a
second supervisor-specific cursor matrix.

## Hard-crash matrix

`go test ./kitdb/relational -run '^TestWriteCompositionConflictAndRecovery$'
-count=10` additionally exits a child process without defers after committed
INSERT SELECT/upsert operations and an uncommitted savepoint transaction.
Reopen must contain only the acknowledged source and audit rows. It also
checks that a stale optimistic upsert cannot overwrite a newer commit. This
is process-exit evidence, not a new power-loss or fsync-boundary matrix.

`go test ./kitdb/relational -run '^TestReverseForeignKeyRecovery$' -count=10`
exits after acknowledged unreferenced-parent UPDATE/DELETE, a rejected
referenced-parent DELETE, and staged but uncommitted child/parent deletions.
Reopen must retain the referenced parent and child, preserve acknowledged
changes, and still enforce the FK. Separate interleaved-transaction tests require
the second commit to fail for competing child INSERT and parent DELETE in both
commit orders. These reuse the existing kernel's recovery and conflict rules;
they are not an additional physical power-loss certification.

`go test ./kitdb/relational -run '^TestReferentialActionConflictAndRecovery$'
-count=10` extends this oracle to acknowledged CASCADE UPDATE/DELETE, including
child index/count changes, and an uncommitted cascaded deletion at hard exit.
The reopened file must preserve only the acknowledged parent/child images.
Both commit orders of a competing child INSERT and cascading parent DELETE
must still reject the stale writer. Row/wave ceilings and late child/trigger
failures are checked separately against full statement rollback.

`go test ./kitdb/relational -run '^TestReferentialDefaultRecovery$' -count=10`
exits after acknowledged parent UPDATE/DELETE with SET DEFAULT child actions,
index maintenance and AFTER UPDATE audit writes, followed by an uncommitted
parent DELETE. Reopen must preserve only acknowledged parent/child/audit images
and still reject deleting their fallback parent. Renaming the referenced table
and column must preserve the action. Separate tests cover concurrent deletion
of the fallback parent in both commit orders, sequence gaps after rollback,
child constraint failures and PostgreSQL savepoint recovery. This adds no WAL
format or new power-loss certification.

The standalone commerce drill additionally exercises a real `kitdbpg` process
with `products`, `orders`, `order_items` and trigger-owned audit writes:

```text
go test ./cmd/kitdbdist -run '^TestKitDBCommerceNativeJourney$' -count=10 -timeout 20m
```

It verifies previously acknowledged orders, stages another complete order and
audit transaction, kills the server before closing that transaction's client,
requires abnormal process exit, and reopens the same file in a new process.
Every row, monetary value and audit event is compared with an independent
model. Verified backup/restore and timestamp recovery then exercise restored
domain/function/trigger/sequence behavior. This is application-boundary crash
evidence, not injection inside fsync/checkpoint publication or hardware
power-loss coverage; the lower-level matrices below remain required.

Run the deterministic matrix from `engine/`:

```text
go test ./kitdb/node -run ^TestReplicaFileLinkHardCrashMatrix$ -count=10
```

Each scenario begins from a verified backup anchor with retained source
history and at least three later transactions.

| Boundary | Durable state intentionally left behind |
| --- | --- |
| `after-registration` | Process-local link configuration only; no transport progress |
| `after-publish` | One synced final batch; source pin and target remain old |
| `after-target-commit` | One target WAL prefix is durable; no acknowledgement exists |
| `after-apply` | Target reached the batch end and a synced ACK exists |
| `after-source-pin` | Source pin reached the ACK; batch and ACK still exist |
| `after-acknowledge` | Source pin and target agree; mailbox cleanup completed |

For every boundary, the recovery oracle requires all of the following:

1. A new `node.Manager` can register the same process-local topology.
2. At most eight bounded link cycles reach the fixed source boundary.
3. One additional cycle is a caught-up `NoChanges` result.
4. Every expected target value matches the source model.
5. The durable source pin exactly equals the source boundary.
6. Full source and target `Verify` passes.
7. The mailbox contains no batch, ACK, or staging pressure.
8. Link health reports no recovery failure and the manager retains no lease.

The `after-target-commit` case cancels through a target commit listener after
the first transaction of a multi-transaction batch. This leaves a real synced
target WAL prefix while the child process still owns the open handle. The
`after-source-pin` case advances the real durable source pin but deliberately
does not call transport cleanup before process exit.

## Node database catalog matrix

Run the deterministic catalog lifecycle matrix from `engine/`:

```text
go test ./work -run '^TestKitDBNodeCatalog(EmptyCreate)?HardCrashMatrix$' -count=10
```

The child exits with code `87` while the tenant, node manager, catalog mutex,
and opened handles remain live. Every scenario starts with one source database
whose retained history contains a sentinel value. Drop scenarios additionally
start with one independently identified active fork and a synced node-catalog
entry.

| Boundary | Durable state intentionally left behind | Fresh-runtime result |
| --- | --- | --- |
| `create-after-intent` | Synced `creating`; no target storage | Intent removed; target remains absent |
| `create-after-target` | Synced `creating`; complete fresh-identity fork | Target verified and entry promoted to `active` |
| `create-after-active` | Synced `active`; no in-memory exposure publication | Target re-exposed from catalog |
| `drop-after-hide` | Target hidden only in child memory; catalog still `active` | Drop rolls back and target is re-exposed |
| `drop-after-dropping` | Synced `dropping`; complete target storage | Startup removes storage and entry |
| `drop-after-storage` | Synced `dropping`; physical target already absent | Missing removal is treated idempotently; entry removed |
| `drop-after-catalog` | Target and catalog entry absent; stale child registry remains | Target remains absent and unexposed |
| `rename-before-catalog` | Old logical key remains committed | Old name is exposed; storage and identity are unchanged |
| `rename-after-catalog` | New logical key is committed; child registry is stale | Only the new name is exposed over the original storage |

For every boundary, the recovery oracle requires all of the following:

1. A new tenant runtime and node manager can recover without process-local
   lifecycle state.
2. Full verification of the node catalog and source database passes.
3. The source identity and sentinel value remain unchanged.
4. A recovered active target has a distinct identity, passes full verification,
   retains the sentinel, and resolves the current source capability.
5. A removed target has no main, WAL, history, or lock path and is not exposed.
6. The recovered catalog contains exactly the expected active entry or no entry.
7. Every borrowed node handle is released before the oracle completes.

This matrix exercises the same catalog write, fork, verification, registration,
atomic rename, and node-managed removal helpers as production
`CREATE/ALTER/DROP DATABASE`; it does
not add a production crash hook. SQL parsing and clean connection behavior stay
covered by the PostgreSQL integration tests.

`TestKitDBNodeCatalogEmptyCreateHardCrashMatrix` repeats all three create
publication boundaries with the production empty-file creation helper rather
than `ForkToTime`. Its active-target oracle requires a distinct verified
database identity, an empty Schema IR catalog, no copied source sentinel, the
current referenced capability, and zero retained node leases. Together the two
matrices distinguish shared lifecycle safety from the different physical
creation mechanisms.

## Resumable import matrix

Run the deterministic KIMP/COPY matrix from `engine/`:

```text
go test ./work -run ^TestKitDBPostgresResumableImportHardCrashMatrix$ -count=10
```

Each child writes the first bounded import chunk and exits with code `88`
without closing the tenant, database handle, session, or managed lease.

| Boundary | Durable state intentionally left behind | Fresh-runtime result |
| --- | --- | --- |
| `before-publication` | COPY decoded one row but has not committed | Neither row nor KIMP exists |
| `after-wal-before-ack` | Row and KIMP committed; no PostgreSQL acknowledgement exists | Both recover once; resume starts at chunk two |
| `after-checkpoint` | Row and KIMP checkpointed while child handles remain live | Both recover once; resume starts at chunk two |

The recovery oracle opens a fresh tenant, compares row presence with KIMP,
continues exactly from the durable chunk/offset/checksum, completes chunk two,
and verifies both primary IDs. The direct adapter path deliberately omits a
wire acknowledgement at the WAL boundary, so this is process-death evidence for
the replay decision rather than a clean reconnect test.

## Seeded soak campaign

The long campaign is opt-in so the ordinary test suite remains fast. In
PowerShell:

```powershell
$env:KITDB_REPLICA_SOAK_ITERATIONS = "1000"
$env:KITDB_REPLICA_SOAK_SEED = "20260825"
go test ./kitdb/node -run ^TestReplicaFileLinkHardCrashSoak$ -count=1 -timeout=30m
```

Each iteration commits two new source transactions, selects one of the six
boundaries from the seeded sequence, hard-exits a child process, and recovers
through a new manager. Full file verification runs periodically and on the
final iteration; the final target is compared with the complete in-memory
model. `KITDB_REPLICA_SOAK_ITERATIONS` is rejected outside `1..10000`. The seed
defaults to `1` and is always printed, so a failure can be replayed exactly.

## Evidence boundary

This lab proves process-death recovery on the host filesystem used by the test.
It composes with the existing torn-WAL, failed-sync, checkpoint-publication,
history-seal, malformed-wire, partial-apply, and clean PostgreSQL restart tests.
It does not prove:

- firmware honesty after a physical power cut;
- storage that falsely reports `fsync` or directory-sync completion;
- network filesystem semantics;
- authenticated or lossy network transport behavior;
- multi-process writers or distributed consensus.

Those claims require separate hardware power-cut campaigns, filesystem-specific
qualification, and a future URL/token transport fault model. A green fault lab
must never widen the durability claim beyond the environment actually tested.
