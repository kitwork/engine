# KitDB

KitDB is an embedded, pure-Go database kernel under `1.0.0`
release-candidate qualification. It is incubated inside the Kitwork engine
repository, but the package does not import the Kitwork VM, runtime, tenant,
database, or search packages. The exact supported profile, compatibility
promise, exclusions, and promotion evidence live in
[RELEASE_1_0.md](RELEASE_1_0.md).
The concrete admission, backup, monitoring, rollout, and incident procedure for
a real project lives in [PRODUCTION.md](PRODUCTION.md).

The canonical product mindset, AI-native boundary, diagonal node-scaling model,
and decision rules live in [VISION.md](VISION.md). Implementation milestones
must not silently redefine those principles.

## Standalone relational boundary

`kitdb/sql` owns the VM-independent logical-type registry, Schema IR, bounded
lexer, statement AST and parser for KitDB's first standalone SQL profile.
`kitdb/relational` binds and executes that profile directly over the KitDB
kernel. Neither package imports Kitwork `work`, Tenant, VM, routing, app-folder,
or search packages.

The embedded entry point is `relational.Open(path)`. The standalone
`cmd/kitdbpg` process opens the same file directly and exposes it through the
bounded PostgreSQL wire adapter. Both paths use the existing durable Schema IR,
KROW v2 row encoding, key layout, catalog transactions, WAL, checkpoint and
recovery implementation. Weighted SEARCH uses a rebuildable immutable segment
projection with a source-transaction watermark and retained-history catch-up;
the `.kitdb` rowstore remains authoritative. Experimental analytics uses the
same boundary: KCOL v3 can dictionary-encode text declared `ANALYTICS` (Kitwork
spells it `text().analytics()`) in the rebuildable `.analytics` sidecar while
KROW/WAL remain the only canonical copy. There is no JSON side database and no
second relational source of truth. Compatibility tests prove files in both
directions between the standalone engine and the current Kitwork adapter.

Hosts that already own a kernel handle use `relational.Attach`; closing the
facade leaves that borrowed handle open. Embedded compilers may call
`ExecutePlan` with a fresh typed `kitdb/sql` statement and bypass SQL rendering,
lexing and parsing entirely. This is Kitwork's target local path: one thin
VM-value adapter into the standalone relational core, never an in-process
PostgreSQL/Hrana/HTTP round trip and never a second relational authority.

Kitwork `struct()` remains the compact, type-safe reference frontend and
Kitwork remains KitDB's first production customer. It is no longer required to
create a schema, write rows, reopen a file, query it, or serve PostgreSQL. The
exact standalone commands and intentionally bounded SQL surface are documented
in [`relational/README.md`](relational/README.md).

PostgreSQL syntax and protocol familiarity are KitDB's primary external access
direction. KitDB is not a miniature PostgreSQL: it borrows a familiar SQL and
wire vocabulary so existing tools and developers can approach an otherwise
independent database. That frontend runs over KitDB's own transaction, catalog,
KROW, recovery, search, and analytics architecture; it does not embed or clone
PostgreSQL. Hrana remains an optional legacy adapter for existing Kitwork
deployments; it is not KitDB's query IR, file format, or default target for new
standalone SQL capabilities.

Standalone Schema IR currently promotes exact modifiers through v8: sequence
identity (v3), constrained NUMERIC (v4), exact temporal meaning/precision (v5),
constrained VARCHAR/CHAR length and padding semantics (v6), and canonical
native UUID meaning (v7), plus analytics partition policy (v8). The optional
field-level `ANALYTICS` policy is part of the same hashed catalog contract.
These are catalog contracts over the same KROW/WAL representation, not physical
format forks.

The end-to-end database release gate and the current capability gaps live in
[COMPLETION.md](COMPLETION.md). It is the priority filter for new work.
Run the composed database journey through the engine gate:

```text
go run ./cmd/releasegate --mode verify --report .artifacts/verify.json
```

Use `--mode kitdb-verify` for the focused development gate and
`--mode kitdb-release --require-clean` for the complete KitDB candidate gate.
`go run ./cmd/kitdb version` prints the machine-readable `kitdb/1` profile.
Run `go run ./cmd/kitdb doctor <database>` with the application writer stopped
to prove source verification plus same-filesystem backup, restore, reopen, and
logical digest equality before admitting a real project.

On success, the KitDB step writes `.artifacts/kitdb-database-gate.json` with
the generated fixture database identity, exact source/backup/restore
transaction, backup digest, row count, and bounded step timings. The gate owns
an isolated synthetic tenant and never opens a production tenant database.

Architecture candidates and the evidence required before adopting them live in
[RESEARCH.md](RESEARCH.md). That document is an experiment map, not a promise
that every listed data structure belongs in KitDB.

Process-death boundaries, the deterministic replica crash matrix, and the
seeded soak command live in [FAULT_LAB.md](FAULT_LAB.md). A green process-crash
campaign is evidence for the tested filesystem, not a claim about dishonest
drive firmware or unsupported network filesystems.

The current milestone proves a durable path with one canonical main file,
append-only immutable generations, lazy checksummed row reads, bounded read
snapshots, bounded concurrent transaction preparation, single-owner group
commit, a transactional schema catalog, bounded compaction, and opt-in
immutable transaction history with durable pins and safe pruning, verified
standalone backup anchors, exact retained restore, bounded replica wire
messages, and an explicit crash-safe filesystem mailbox:

```text
Open -> Begin -> Put/Delete -> Commit -> Checkpoint -> Close -> Reopen -> Get
```

`tenant.kitdb` contains immutable sorted mutation segments. Each segment has
checksummed pages and a checksummed sparse directory containing page key
bounds, offsets, lengths, mutation counts, and CRC32C values. Two fixed
superblock slots alternate between generations. The newest valid slot points
to a checksummed manifest describing the complete logical state.

New commits are stored in `tenant.kitdb.wal` as checksummed append-only frames
and become visible in memory only after the WAL has been synced. A normal
`Checkpoint` appends only the sorted WAL overlay plus a new manifest, syncs
those bytes, publishes the inactive superblock slot, syncs again, and only
then rotates the WAL. Old segments are immutable and remain readable during
publication.

If a process dies between those two publications, recovery validates the stale
WAL through the main-file boundary before completing rotation. An incomplete
tail after the durable boundary is removed; a missing or mismatched boundary is
corruption.

When `RetainHistory` is enabled, every WAL range being rotated is first copied
byte-for-byte into a checksummed immutable segment under
`tenant.kitdb.history`. This adds no write or sync to the commit hot path;
history publication happens only at checkpoint. A stale WAL left after main
publication is sealed during recovery before rotation completes.

After main publication, a retained-history seal failure or WAL-rotation failure
makes the live handle unavailable. Reopen validates the old WAL and any
already-published segment before completing the interrupted boundary. This
fail-closed rule prevents later commits from creating overlapping history.

Opening a format-v3 database verifies its header, both superblock slots, the
active manifest, and segment directories without reading mutation pages.
`Get` checks the WAL overlay first and then searches segments from newest to
oldest, so the newest put or tombstone wins. Every selected page is verified
before decoding into a bounded LRU cache. `Walk` performs a bounded k-way merge
of the active segments. `Verify` explicitly scans every active persisted page.

```go
db, err := kitdb.Open("tenant.kitdb")
if err != nil {
	return err
}
defer db.Close()

tx, err := db.Begin()
if err != nil {
	return err
}
if err := tx.Put([]byte("product/1"), []byte(`{"title":"Kitwork"}`)); err != nil {
	return err
}
transaction, err := tx.Commit()
if err != nil {
	return err
}
_ = transaction

if _, err := db.Checkpoint(); err != nil {
	return err
}

value, found, err := db.Get([]byte("product/1"))
```

## Operator CLI

`cmd/kitdb` is a thin operator surface over the same exported kernel and node
primitives. Successful commands emit one versioned JSON document with
`"format": "kitdb-cli/v1"`; source paths must already be regular files, so an
inspection typo cannot create an empty database.

```text
go run ./cmd/kitdb inspect tenant.kitdb
go run ./cmd/kitdb verify tenant.kitdb
go run ./cmd/kitdb backup --pin backup/nightly tenant.kitdb anchor.kitdb
go run ./cmd/kitdb restore anchor.kitdb restored.kitdb
go run ./cmd/kitdb restore --transaction 42 --history tenant.kitdb.history anchor.kitdb restored-42.kitdb
go run ./cmd/kitdb restore-time --at 2026-08-27T14:30:00+07:00 tenant.kitdb recovered.kitdb
```

`backup` deliberately uses the bounded node maintenance path: it verifies the
anchor, checkpoints the source to seal history, and publishes the named durable
pin before reporting success. `restore` never overwrites its destination. The
CLI owns no alternate encoding, WAL, backup, or recovery implementation.

## Concurrent commit coordinator

Multiple goroutines may call `Begin`, prepare independent write-only
transactions, and submit `Commit` concurrently. One database-owned writer
assigns their order, emits one complete WAL frame per transaction, and syncs a
group once. There is no MVCC read timestamp or write-conflict detection yet;
queue admission order is commit order and later writes to the same key win.

The default queue holds 256 active transactions, the aggregate payload retained
by all active transactions is bounded to 64 MiB, and at most 64 queued requests
share a sync. A zero delay groups only requests already waiting, avoiding added
latency for an isolated commit. Hosts may trade a bounded delay for throughput:

```go
db, err := kitdb.OpenWithOptions("tenant.kitdb", kitdb.OpenOptions{
	CommitQueueSize:  256,
	MaxCommitBatch:   64,
	GroupCommitDelay: 200 * time.Microsecond,
})
```

## Retained transaction history

History is opt-in for a database that has never retained it. Once the history
directory exists, later opens keep retention enabled even when default options
are used; this prevents an accidental configuration change from silently
creating a permanent transaction gap.

When retention is first enabled on an existing database, KitDB recovers and
checkpoints its current WAL before publishing the timestamped base anchor.
Existing state is therefore preserved, but transactions from before enablement
are not retroactively assigned wall-clock history.

```go
db, err := kitdb.OpenWithOptions("tenant.kitdb", kitdb.OpenOptions{
	RetainHistory: true,
})
if err != nil {
	return err
}

lastApplied := uint64(0)
retained, err := db.WalkHistory(ctx, lastApplied, func(event kitdb.CommitEvent) error {
	// Apply the complete transaction to a projection or replica staging area.
	return consume(event)
})
if err != nil {
	return err
}
pin, err := db.SetHistoryPin(ctx, "search/products", retained.Cursor())
if err != nil {
	return err
}
lastApplied = pin.Cursor.Transaction
```

`WalkHistory` first obtains a fixed durable boundary by checkpointing, then
streams complete transactions through that boundary. A commit that wins the
checkpoint lock may be included; commits published after the chosen boundary
are left for the next walk. The callback receives caller-owned keys and values,
and consumers must advance their durable watermark only after the complete
walk returns nil. `HistoryRange.Cursor()` includes the database identity,
transaction, and boundary checksum needed for a durable retention pin.

The retained base is the main-file transaction at which history was first
enabled. Asking for an older transaction returns `ErrHistoryGap`; enabling
history does not fabricate earlier changes. Segment filenames and WAL headers
must form one contiguous transaction/checksum chain. Missing segments return
`ErrHistoryGap`, while malformed or checksum-invalid bytes return `ErrCorrupt`.

Named pins are stored in the bounded, checksummed `PINS` control file. A pin at
transaction `T` allows history through `T` to be pruned while protecting every
later transaction. Moving a pin verifies its database identity and checksum
against the retained chain:

```go
pins, err := db.HistoryPins()
result, err := db.PruneHistory(ctx, safeThrough)
err = db.ReleaseHistoryPin(ctx, "search/products")
```

`PruneHistory` is explicit, refuses to pass the oldest pin, and removes only
complete `.khist` segments. It publishes the new checksummed `META` base before
deleting retired files. A crash can therefore leave observable cleanup debris,
but cannot leave an active history gap. `HistoryRetiredSegments` and
`HistoryRetiredBytes` expose that debris. Retrying the same already-published
boundary removes the retired prefix without moving `META` again. Manual segment
deletion is still unsupported.

Bounded retention is process policy, not durable database authority. The
kernel evaluates age from checksummed commit timestamps and size from verified
active segment bytes, then delegates to the same crash-safe whole-segment
prune path. The oldest durable pin always wins; a pin-limited result succeeds
but reports `LimitedByPin` and `BytesOverLimit` so the host can surface pressure:

```go
policy := kitdb.HistoryRetentionPolicy{
	MaxAge:   7 * 24 * time.Hour,
	MaxBytes: 2 << 30,
}
result, err := db.EnforceHistoryRetention(ctx, policy, time.Now())
```

No timer or goroutine is created by a database handle, and policy evaluation
never runs on the commit hot path. `recovery: true` and
`OpenOptions{RetainHistory: true}` remain deliberately unbounded. A node-managed
checkpoint evaluates `OpenOptions.HistoryRetention` only after successful
checkpoint publication; `ScheduleHistoryRetention` exposes the same bounded
operation explicitly. There is still no managed backup-anchor catalog. The
format bounds one directory to 65,536 canonical segments and 1,024 pins,
failing closed rather than accepting unbounded metadata.

Every new commit also stores a checksummed, strictly increasing UTC timestamp.
With retained history enabled, KitDB can resolve a wall-clock target to the
last exact transaction at or before that time:

```go
restored, err := db.RestoreToTime(ctx, "recovered.kitdb", target)
forked, err := db.ForkToTime(ctx, "independent.kitdb", target)
```

`RestoreToTime` preserves the source database identity for lineage-sensitive
operator restore workflows. `ForkToTime` gives the selected state a fresh
identity so source and destination can safely accept independent writes. Both
publish a fully verified file without overwriting an existing destination.
The recoverable window starts when history is first enabled; KitDB returns
`ErrRecoveryTargetTooOld` or `ErrRecoveryTimeUnavailable` rather than infer a
timestamp for data it did not record.

## Verified backup anchors

`CreateBackupAnchor` captures the latest committed logical state, including
mutations still present only in the synced WAL overlay, and streams it into a
new compacted format-v3 main file:

```go
anchor, err := db.CreateBackupAnchor(ctx, "backup/tenant-000042.kitdb")
if err != nil {
	return err
}

verified, err := kitdb.VerifyBackupAnchor(ctx, anchor.Path)
```

The destination must not exist and its parent directory must already exist.
KitDB writes and syncs a same-directory staging file, verifies every page and
the exact active boundary, computes SHA-256 over the complete bytes, then
publishes the destination exclusively and syncs its directory where supported.
A cancellation or validation failure never publishes the destination.

Anchor construction owns a regular bounded read snapshot. Commits and
incremental checkpoints may continue, while full compaction is refused until
the snapshot closes. The source main file, checkpoint transaction, WAL, and
history are not changed merely to create an anchor.

The returned database identity, transaction, and boundary checksum connect the
anchor to later retained-history frames. The file is also a standalone KitDB
main file and can be opened independently. This milestone does not yet provide
remote upload or a managed anchor catalog. Callers can register the returned
anchor boundary with `SetHistoryPin` before pruning later history. Because
anchor creation does not checkpoint the source, first seal history through that
boundary with `Checkpoint` or `WalkHistory`, then pin `anchor.Cursor()`.

## Restore by transaction

`RestoreToTransaction` combines a caller-selected verified anchor with the
source retained-history directory and publishes a new standalone main file at
one exact transaction:

```go
restored, err := kitdb.RestoreToTransaction(
	ctx,
	"backup/tenant-000042.kitdb",
	"tenant.kitdb.history",
	"restore/tenant-at-1200.kitdb",
	1200,
)
if err != nil {
	return err
}
_ = restored.SHA256
```

The target must be at or after the anchor transaction. A target equal to the
anchor needs no history path and preserves the anchor bytes exactly. A later
target must be covered by a contiguous canonical history chain with the same
database identity. If the anchor falls inside a history segment, replay proves
the checksum of that exact frame before accepting its successor. Every used
history file is validated completely even when the target lies before its
last frame.

Restore streams committed operations into a coalescing overlay and seals that
overlay into immutable format-v3 generations after 32 MiB or 262,144 distinct
keys. It therefore does not retain the complete transaction range in memory or
sync a new WAL once per replayed transaction. The output carries the target
transaction and boundary checksum but no WAL, lock, or history sidecar.

The destination must not exist. KitDB copies the anchor into same-directory
staging, applies and syncs bounded generations, verifies every active page and
logical row, computes SHA-256, then publishes the destination exclusively and
syncs its directory. A gap, wrong identity, malformed frame, corruption,
cancellation, or validation failure leaves no destination. This is a local
materialization primitive, not automatic anchor discovery, history retention,
remote restore orchestration, or a low-latency `AS OF` query API.

## One-way replicas

Bootstrap creates both a verified standalone target and a durable source pin,
so history cannot be pruned between those two operator steps:

```go
bootstrap, err := primary.BootstrapReplica(
	ctx,
	"replica/singapore-1",
	"replicas/singapore-1.kitdb",
)
if err != nil {
	return err
}

replica, err := kitdb.OpenReplica(bootstrap.Anchor.Path, kitdb.OpenOptions{
	RetainHistory: true, // optional: permits a downstream replica later
})
if err != nil {
	return err
}
defer replica.Close()

result, err := primary.CatchUpReplica(ctx, "replica/singapore-1", replica)
if err != nil {
	return err
}
_ = result.To
```

Transport adapters use the same bounded protocol directly. The first read fixes
one checkpoint boundary; every continuation carries that boundary unchanged:

```go
cursor, err := replica.CurrentCursor()
if err != nil {
	return err
}
boundary := kitdb.HistoryCursor{}
for {
	batch, err := primary.ReadReplicaBatch(ctx, kitdb.ReplicaBatchRequest{
		From: cursor, SourceBoundary: boundary,
	})
	if err != nil {
		return err
	}
	applied, err := replica.ApplyReplicaBatch(ctx, batch)
	if err != nil {
		return err
	}
	if _, err := primary.AcknowledgeReplicaBatch(
		ctx, "replica/singapore-1", applied.Acknowledgement,
	); err != nil {
		return err
	}
	cursor = applied.To
	boundary = batch.SourceBoundary
	if batch.Complete() {
		break
	}
}
```

Protocol v1 has no durability sidecar. Wire v1 is its deterministic pure-Go
serializer: one fixed header, canonical WAL-frame bodies, a fixed trailer, and
SHA-256 over the complete envelope. A successful apply returns a fixed 96-byte
cursor acknowledgement rather than repeating transaction bodies. Batches
default to at most 1,024 transactions and one maximum legal WAL-frame worth of
exact bytes; lower caller limits are allowed, while hard ceilings remain 4,096
transactions and 67,108,920 body bytes.

The first concrete transport is an explicit host-owned filesystem mailbox:

```go
mailbox, err := kitdb.OpenReplicaFileTransport(
	"replication/singapore-1",
	kitdb.ReplicaFileTransportLimits{},
)
if err != nil {
	return err
}

batch, err := primary.ReadReplicaBatch(ctx, kitdb.ReplicaBatchRequest{
	From: bootstrap.Pin.Cursor,
})
if err != nil {
	return err
}
if _, err := mailbox.PublishBatch(ctx, batch); err != nil {
	return err
}

// This can run in another process or after copying the mailbox artifacts.
if _, found, err := mailbox.ApplyNext(ctx, replica); err != nil {
	return err
} else if !found {
	return nil
}

if _, found, err := mailbox.AcknowledgeNext(
	ctx, primary, "replica/singapore-1",
); err != nil {
	return err
} else if !found {
	return nil
}
```

`PublishBatch` writes and syncs staging, exposes the final `.krpb` name without
overwrite, then syncs the directory. `ApplyNext` verifies the complete message,
commits only through the target WAL, and exposes `.krpa` only afterward.
`AcknowledgeNext` moves the source pin monotonically before deleting batch and
ACK files. Every stage is idempotent after restart. Partial staging is ignored
and can be removed explicitly with `CleanupStaging` while the mailbox is
quiescent. Default mailbox ceilings are 64 batches and 512 MiB; there is no
watcher or background goroutine. Mailbox publication currently requires local
same-directory hard-link support; an unsupported filesystem fails explicitly
rather than exposing a partially written final name.

`OpenReplica` refuses `Begin`, preventing application writes from silently
forking the target. Catch-up first proves the target's database identity,
transaction, and checksum against source history. Each event must be the exact
next transaction and reproduce the source frame checksum before it reaches the
ordinary target commit writer. Catalog operations and row operations therefore
share the same WAL sync and recovery path.

The target main/WAL boundary is its durable cursor. A complete duplicate batch
is a no-op, and a batch interrupted after a committed prefix resumes from the
matching interior cursor. Cancellation can leave that safe prefix while the
older source pin remains in place; close, reopen with `OpenReplica`, and retry
the batch or call `CatchUpReplica` again. A malformed envelope, history gap, or
diverged checksum fails closed. The source pin moves only after an exact batch
acknowledgement; local catch-up acknowledges only its final fixed boundary.

This kernel API remains a host-operated one-way primitive, not an authenticated
network service, peer discovery, leader election, synchronous primary commit
acknowledgement, or multi-primary merge. The optional `kitdb/node` controller
can repeatedly compose its filesystem stages without changing this durability
boundary. An intentional promotion closes the replica handle and reopens the
file normally.

`Close` refuses new work, drains commits already admitted to the writer, and
invalidates transactions that were prepared but never submitted. The Kitwork
`struct()` adapter deliberately retains its per-database validation lock so
unique and foreign-key checks cannot race before relational conflict detection
is implemented in the kernel.

An explicit snapshot keeps one committed logical view while later commits and
incremental checkpoints continue. Range cursors seek through sparse page
directories and stream in ascending or descending raw-key order. `Start`
remains inclusive and `End` exclusive in either direction:

```go
snapshot, err := db.Snapshot()
if err != nil {
	return err
}
defer snapshot.Close()

cursor, err := snapshot.Cursor(kitdb.RangeOptions{
	Prefix: []byte("product/"),
	Limit:  100,
	Reverse: true,
})
if err != nil {
	return err
}
defer cursor.Close()

for cursor.Next() {
	key := cursor.Key()
	value := cursor.Value()
	_, _ = key, value
}
if err := cursor.Err(); err != nil {
	return err
}

scanStats := cursor.Stats() // Read before Close.
_ = scanStats.PagesRead
```

Code that needs keys but never row values can use the callback-scoped scan:

```go
stats, err := snapshot.ScanKeys(kitdb.RangeOptions{
	Prefix: []byte("index/products/status/"),
}, func(key []byte) (bool, error) {
	// key is read-only and valid only until this callback returns.
	return false, consume(key)
})
if err != nil {
	return err
}
_ = stats.PagesRead
```

`ScanKeys` preserves snapshot ordering, ranges, limits, immutable-generation
merge, tombstones, and the captured WAL overlay. Forward scans reuse one
checksummed page buffer per immutable segment and never copy row values. Use a
normal Cursor whenever a key or value must survive the callback.

Snapshots own independent read handles and copied WAL overlays. At most 32 may
be active per database. They do not block commits or incremental checkpoints,
but full compaction returns `ErrSnapshotsActive` until they close. `DB.Close`
closes any snapshots the caller forgot to release.

`Snapshot.GetWithStats` and `Cursor.Stats` expose query-local main-file
evidence without global counters. They distinguish KitDB LRU hits, misses and
bypasses; successful page reads, payload bytes and decoded records; physical
generation entries consumed by merge lookahead; and WAL-overlay entries
examined. Range cursors deliberately bypass the point-lookup LRU, so a scan may
report page reads with zero cache misses. The values describe KitDB `ReadAt`
and decode work, not hardware reads: the operating system may still satisfy a
main-file read from its own page cache. Cursor statistics must be sampled before
`Close` and belong to the cursor's single goroutine.

The default page-cache ceiling is 16 MiB per open database. Hosts managing many
tenants can set a smaller budget or disable it:

```go
db, err := kitdb.OpenWithOptions("tenant.kitdb", kitdb.OpenOptions{
	PageCacheBytes: 4 << 20,
	VerifyOnOpen:   true,
})

stats, err := db.Stats()
```

`VerifyOnOpen` is optional. The default fast-open path verifies each page on
first access; hosts can run `db.Verify()` as an explicit startup check or a
background scrub. Verification reads do not populate the LRU cache.

`Stats` exposes the active generation, logical record count, physical mutation
count, segment count, block count, active file boundary, WAL size, overlay
size, active snapshots and transactions, pending commits, successful commit
batches/syncs, largest observed batch, and page-cache use. Hosts can make
maintenance and admission decisions without inspecting user data. When history
is active, the same snapshot reports its base transaction, active and retired
segment bytes, pin count, and oldest pinned transaction. It also reports
catalog struct count, encoded bytes, and a deterministic revision.

## Fleet resource governor

Opening every tenant forever would multiply the per-handle writer goroutine,
file descriptors, lock, commit queue, and page-cache ceiling. Package
`kitdb/node` therefore owns a bounded set of shared handles above the database
kernel:

```go
fleet, err := node.NewManager(node.Limits{
	MaxOpenDatabases:      64,
	MaxPageCacheBytes:     256 << 20,
	DefaultPageCacheBytes: 1 << 20,
	MaxConcurrentOpens:    4,
	MaxConcurrentMaintenance:   2,
	MaxQueuedMaintenance:       1024,
	MaxMaintenancePerDatabase: 2,
	MaintenanceTimeout:          10 * time.Minute,
})
if err != nil {
	return err
}
defer fleet.Close()

lease, err := fleet.Acquire(ctx, "tenant-42.kitdb", kitdb.OpenOptions{})
if err != nil {
	return err
}
defer lease.Release()

db := lease.DB()
```

A host can explicitly keep an important database handle warm without making
that policy part of the database file:

```go
lease, err := fleet.AcquireWithPolicy(
	ctx,
	"orders.kitdb",
	kitdb.OpenOptions{},
	node.DatabasePolicy{Warm: true},
)
```

`Warm` protects the idle handle, catalog, and bounded caches from ordinary LRU
eviction. It does not read the complete database into RAM. Explicit policy
requests for an already managed handle must match; `SetDatabasePolicy` performs
a deliberate live change. If warm databases consume all fleet capacity, a new
acquisition fails with `ErrWarmCapacity` rather than waiting indefinitely.

One canonical path shares one handle. A lease pins it until every operation,
transaction, snapshot, and cursor borrowed by the caller has finished. Released
handles enter an idle LRU; pressure on either the handle count or total reserved
page-cache budget closes the least-recently-used non-warm idle database. If
every evictable candidate is leased, `Acquire` waits with context cancellation
instead of exceeding the declared bounds. `TrimIdle` can proactively return
ordinary idle handles to the cold, zero-handle state while preserving warm
handles.

The same manager provides typed background maintenance without creating a
second file owner:

```go
ticket, err := fleet.ScheduleCheckpoint(
	ctx,
	"tenant-42.kitdb",
	kitdb.OpenOptions{},
	node.MaintenanceNormal,
)
if err != nil {
	return err
}

// Waiting is optional for soft maintenance. A canceled waiter does not cancel
// the shared checkpoint.
result, err := ticket.Wait(ctx)

backup, err := fleet.ScheduleBackup(ctx, node.BackupRequest{
	Source:      "tenant-42.kitdb",
	Destination: "backups/tenant-42-2026-08-24.kitdb",
	Options:     kitdb.OpenOptions{RetainHistory: true},
	PinName:     "backup/2026-08-24",
	Priority:    node.MaintenanceBackground,
})
if err != nil {
	return err
}
verified, err := backup.Wait(ctx)

catchUp, err := fleet.ScheduleReplicaCatchUp(ctx, node.ReplicaCatchUpRequest{
	Source:        "tenant-42.kitdb",
	Target:        verified.Backup.Anchor.Path,
	SourceOptions: kitdb.OpenOptions{RetainHistory: true},
	TargetOptions: kitdb.OpenOptions{
		RetainHistory: true, // optional: permits downstream replication
	},
	PinName:  "backup/2026-08-24",
	Priority: node.MaintenanceBackground,
})
if err != nil {
	return err
}
replicated, err := catchUp.Wait(ctx)

published, err := fleet.ScheduleReplicaFilePublish(ctx, node.ReplicaFilePublishRequest{
	Source:        "tenant-42.kitdb",
	Mailbox:       "replication/singapore-1",
	SourceOptions: kitdb.OpenOptions{RetainHistory: true},
	PinName:       "backup/2026-08-24",
	BatchLimits:   kitdb.ReplicaBatchLimits{MaxTransactions: 256},
	Priority:      node.MaintenanceBackground,
})
if err != nil {
	return err
}
publication, err := published.Wait(ctx)

applied, err := fleet.ScheduleReplicaFileApply(ctx, node.ReplicaFileApplyRequest{
	Target:   verified.Backup.Anchor.Path,
	Mailbox:  "replication/singapore-1",
	Priority: node.MaintenanceBackground,
})
if err != nil {
	return err
}
targetProgress, err := applied.Wait(ctx)

acknowledged, err := fleet.ScheduleReplicaFileAcknowledge(
	ctx,
	node.ReplicaFileAcknowledgeRequest{
		Source:        "tenant-42.kitdb",
		Mailbox:       "replication/singapore-1",
		SourceOptions: kitdb.OpenOptions{RetainHistory: true},
		PinName:       "backup/2026-08-24",
		Priority:      node.MaintenanceBackground,
	},
)
if err != nil {
	return err
}
sourceProgress, err := acknowledged.Wait(ctx)

prune, err := fleet.ScheduleHistoryPrune(ctx, node.HistoryPruneRequest{
	Path:     "tenant-42.kitdb",
	Through:  replicated.ReplicaCatchUp.To.Transaction,
	Options:  kitdb.OpenOptions{RetainHistory: true},
	Priority: node.MaintenanceBackground,
})
if err != nil {
	return err
}
pruned, err := prune.Wait(ctx)
```

For a local topology that should reconcile continuously, the same manager can
compose those three stages without creating a goroutine per tenant:

```go
health, err := fleet.RegisterReplicaFileLink(node.ReplicaFileLinkConfig{
	Name:          "tenant-42/singapore-1",
	Source:        "tenant-42.kitdb",
	Target:        verified.Backup.Anchor.Path,
	Mailbox:       "replication/singapore-1",
	SourceOptions: kitdb.OpenOptions{RetainHistory: true},
	TargetOptions: kitdb.OpenOptions{RetainHistory: true},
	PinName:       "backup/2026-08-24",
	BatchLimits:   kitdb.ReplicaBatchLimits{MaxTransactions: 256},
	Priority:      node.MaintenanceBackground,
})
if err != nil {
	return err
}

// Wake is a cheap hint after a source commit; the idle poll remains a fallback.
err = fleet.WakeReplicaFileLink(health.Name)
```

For a real project, the host may register a bounded recurring protection policy
on the same manager. Tenant code never receives these filesystem capabilities:

```go
publisher, err := node.NewVerifiedDirectoryPublisher(
	"/mnt/kitdb-offhost/tenant-42",
	node.VerifiedDirectoryPublisherOptions{MaxEntries: 32, KeepAnchors: 8},
)
if err != nil {
	return err
}
if err := fleet.RegisterProductionAnchorPublisher("offhost", publisher); err != nil {
	return err
}

health, err := fleet.RegisterProductionPolicy(node.ProductionPolicyConfig{
	Name:            "tenant-42",
	Source:          "/srv/kitwork/live/tenant-42.kitdb",
	BackupDirectory: "/srv/kitwork/backups/tenant-42",
	Publisher:       "offhost",
	SourceOptions:   kitdb.OpenOptions{RetainHistory: true},
	BackupInterval:  15 * time.Minute,
	MaxBackupAge:    30 * time.Minute,
	RestoreInterval: 24 * time.Hour,
	MaxRestoreAge:   36 * time.Hour,
	KeepBackups:     8,
	Priority:        node.MaintenanceBackground,
})
if err != nil {
	return err
}

// Manual runs join an active cycle and remain available while paused.
cycle, err := fleet.RunProductionPolicyOnce(ctx, health.Name)
```

All policies share one dispatcher and a fixed worker pool. A cycle verifies the
exclusive local backup store, creates or safely resumes an immutable anchor via
`ScheduleBackup`, publishes the durable history pin, optionally restores to a
unique temporary file, compares `kitdb-logical-digest/v1`, and prunes only old
fully verified same-identity anchors. `ProductionPolicyHealth` exposes path-free
`ready`, `degraded`, or `unsafe` evidence. Registration creates no directory or
database; configuration is process-local and must be registered again after a
restart. Existing verified anchors are then rediscovered without creating a
duplicate, while a fresh restore drill is still required before `ready`. The
dispatcher chooses the earlier backup or restore deadline, so a frequent
restore drill is not delayed by a longer backup interval.

An optional host-owned `ProductionAnchorPublisher` extends that cycle without
entering the transaction kernel. Success requires a path-free receipt for the
exact current anchor after the publisher retrieves and fully verifies it; an
upload acknowledgement alone is insufficient. `VerifiedDirectoryPublisher`
uses deterministic immutable names, bounded discovery and retention, cleans
only its reserved abandoned restore-staging namespace, and fails closed on
corruption, mixed identities, backward transactions, forks, or overlapping
owned destinations. It is suitable for a separately mounted volume or transfer
spool, but cannot prove physical independence. Native R2/S3 transport,
credential ownership, and alert delivery remain reviewed host and deployment
work.

Checkpoint, verification, verified-backup, retained-history-prune and
retention-policy evaluation, direct
replica catch-up, filesystem replica jobs, and Kitwork row-migration and
secondary-index chunks are bounded globally and per
touched path, coalesced by canonical path and exact operation identity, and run
under ordinary database leases. Different prune boundaries remain different
jobs and are never widened during coalescing. Direct catch-up reserves and opens
both source and target. Backup destinations and replica mailboxes are
reservation-only resources: they serialize conflicting filesystem work but do
not consume a database handle or page-cache budget and are never opened as a
KitDB file. Multi-database jobs acquire handles in canonical order and only one
runs at a time; independent one-database jobs using different mailboxes can use
the rest of the bounded worker pool.

The managed filesystem path deliberately exposes three separate tickets so the
source and target may run on different nodes. `Publish` keeps at most one batch
or ACK in flight, `Apply` commits at most one batch through the target WAL, and
`Acknowledge` advances the source pin before cleanup. A manager restart between
any two steps loses no authoritative progress: the target cursor, source pin,
and mailbox artifacts are sufficient. Repeated publish while artifacts remain
returns `Pending`; an empty source boundary returns `NoChanges`. The manager
removes interrupted staging while it owns the reserved mailbox and reports only
bounded, label-free aggregate counts, bytes, and transaction lag. It does not
discover peers or provide network transport. An explicitly registered local
link may compose the same three tickets repeatedly. Every link shares one
dispatcher and a fixed worker pool, uses capped retry backoff plus separate
active/idle cadence, and exposes bounded named health through
`ReplicaFileLinkHealth`. Configuration is intentionally process-local and must
be registered again after restart; protocol-owned files and cursors, not the
manager, retain authoritative progress. Within one manager, each target,
mailbox, and `source + pin` pair has exactly one link owner, and a mailbox path
cannot also be a registered source or target database.

Urgent work receives preference, but weighted fairness guarantees progress for
normal and background work. Workers start lazily on the first submission. Queue
overload is explicit rather than spawning an unbounded goroutine.

The Kitwork relational adapter registers one stable row-migration driver with
the node. `ScheduleRowMigration` retains no Schema IR body or cursor: every
dispatch leases the file, takes its file-scoped relational gate, reloads the
checksummed `KRMS` state, and commits at most one bounded chunk. A successful
pending result is placed at the back of the background queue, so urgent and
normal work can run between chunks. Duplicate wake-ups coalesce. Invalid data
or a driver error stops the task without moving the durable cursor; a later
source-schema repair commit wakes a new task. Manager shutdown cancels and
drains this work with the same maintenance pool. The process-local driver and
queue are never migration authority and need no recovery file.

The adapter registers a separate stable secondary-index driver for both legacy
codec upgrades and ordinary index generations. `ScheduleSecondaryIndex`
retains no Schema IR, generation map, or cursor. Every dispatch takes the same
file gate, reloads checksummed `KIBS`, commits at most one bounded build or
cleanup chunk, releases its lease, and rejoins the background queue only after
durable progress. Accepted schema/index DDL, restart catalog hydration, and a
compatible dual-write wake the coalesced task. A worker failure leaves the last
committed cursor authoritative; no scheduler journal is introduced.

A verified backup creates an immutable anchor without blocking commits, verifies
the published destination, checkpoints the source to seal retained history
through the anchor transaction, and durably sets the requested transaction and
checksum pin. The destination is never overwritten. Retrying the same request
adopts an existing file only after complete verification, identity matching,
and checksum-chain proof, which safely resumes a process stopped after anchor
publication. A successful anchor plus its retained history can be passed to
`RestoreToTransaction`. The source must be opened with retained history; this
host-trusted filesystem API is not exposed to tenant code.

A managed history prune delegates the exact requested transaction to the
kernel. The oldest durable pin remains authoritative, only complete segments
can advance the base, and the typed result reports both logically pruned bytes
and any physical cleanup still pending. Retrying an already-published boundary
is an idempotent cleanup pass. The operation is host-trusted and never exposed
as a tenant database method.

A managed checkpoint with `OpenOptions.HistoryRetention` evaluates the policy
after the checkpoint succeeds. Hosts may also call `ScheduleHistoryRetention`
with the same options. Both paths coalesce through the fleet governor and use
the kernel's one publication primitive; neither owns a second timer or
durability protocol. Pin pressure is successful, typed output rather than an
implicit deletion or retry loop.

The verified backup is also the managed replica bootstrap; the node layer does
not create another copy protocol. Managed catch-up forces the target handle into
replica mode, validates both existing files before admission, and delegates
identity/checksum/order proof to `CatchUpReplica`. Applied target transactions
use the ordinary target WAL and remain durable if cancellation or failure stops
the job later. The typed result exposes that partial boundary for retry, and the
source pin advances only after a complete fixed range arrives. This topology
authority remains host-only.

The manager defaults to 64 open databases, 256 MiB of aggregate reserved page
cache, 1 MiB per managed database, and at most four concurrent opens (further
bounded by `GOMAXPROCS`). Maintenance defaults to two concurrent jobs, 1,024
queued jobs, two jobs retained per database, and a ten-minute execution
deadline. The local replica controller defaults to 256 registered links, at
most two concurrent cycles (also capped by maintenance concurrency), 25 ms
between progressing batches, a 30-second caught-up poll, and retry backoff from
250 ms through one minute. These are ceilings, not a claim that all reserved
bytes are resident. The production supervisor defaults to 1,024 registered
policies, two concurrent cycles (also capped by maintenance concurrency), 4,096
entries per exclusive local backup directory, one-second through five-minute
retry backoff, and a 30-minute cycle timeout.
`node.Stats` exposes active/idle/opening/closing handles, leases, waiters,
reservations, opens, reuse, eviction, maintenance queue/running counts,
coalescing, rejection, completion, cancellation, failure, verified-backup,
backup-resumption, successful history-prune, retention evaluation and
pin-pressure, pruned-segment/byte, and duration
counters plus replica catch-up completions, no-ops, and durably applied
transactions, plus row-migration and secondary-index task/chunk/advanced-chunk
counters, including whether a multi-database job currently owns the
cross-path slot, plus bounded controller link/running/queued/paused and
success/failure/backoff counters, without touching tenant data.
Production counters separately expose policy readiness, queued/running/paused
counts, attempts, success/failure/cancellation/backoff, created/resumed anchors,
restore drills, and pruned anchors without paths or tenant values.

The node package never participates in WAL durability or transaction ordering.
Its timeout cancels admission and context-aware work; a checkpoint or verify
already inside a non-preemptible kernel call is allowed to return safely.
The Kitwork host owns one manager in `core.Engine`, injects it into every app
identity, and holds a lease for each complete ORM operation. A standalone
Tenant outside Engine receives an app-owned fallback. Kitwork schedules a
checkpoint after 4 MiB of WAL or 2,048 overlay mutations without blocking the
request; at 8 MiB or 4,096 mutations it joins that job with urgent priority and
waits, applying bounded backpressure before more writes. Total process memory,
operating-system file-descriptor accounting, idle timers, and admission-rate
governance remain later node work.

## Transactional schema catalog

KitDB owns a bounded, versioned registry of durable struct definitions. The
kernel validates the catalog envelope and stable identity while leaving field
semantics to a record-layer frontend such as Kitwork Schema IR. A definition
can commit in the same WAL frame as its rows and indexes:

```go
tx, err := db.Begin()
if err != nil {
	return err
}
if err := tx.DefineStruct(schemaIR); err != nil {
	return err
}
if err := tx.Put(rowKey, rowValue); err != nil {
	return err
}
if _, err := tx.Commit(); err != nil {
	return err
}

catalog, err := db.Catalog()
version, err := db.CatalogVersion()
definition, found, err := db.CatalogStructByName("products")
hash, found, err := db.CatalogStructHashByID(definition.ID)
```

Catalog names are unique, IDs are exactly 16 bytes encoded as hexadecimal, and
returned definitions are caller-owned copies. A rejected catalog transaction
does not consume a transaction ID or publish any of its accompanying row
mutations. Catalog state becomes visible only after the same WAL sync as the
rest of its transaction. The hash-only lookup avoids cloning a full definition
when a record layer needs allocation-light write admission.

`CatalogVersion` returns the current transaction boundary plus a deterministic
catalog revision without cloning definition documents. Record-only commits
advance the transaction while retaining the same revision. Kitwork uses this as
the common ORM, SQL-light, Hrana, and PostgreSQL entry-point check: an unchanged
revision retains the existing immutable schema pointers; a changed revision is
decoded and validated once under the per-file relational writer gate, then the
complete catalog-owned map is replaced atomically. Source-declared `struct()`
definitions remain authoritative, while a catalog-owned rename or drop cannot
leave an old name visible in another connection or app generation.

A record transaction captures the catalog revision under the same writer gate
as its kernel snapshot. Its transaction proxy keeps that schema for the whole
callback even if another connection commits DDL. Commit reports the ordinary
retryable transaction conflict if the catalog revision has advanced; a
read-only transaction therefore cannot silently validate work against one
schema and complete after another schema becomes authoritative.

Fast `Open` does not scan row pages or eagerly decode catalog documents. The
first catalog operation seeks directly to the reserved catalog keyspace and
caches the validated registry. `VerifyOnOpen` performs that semantic catalog
validation eagerly in addition to verifying persisted pages.

## Kitwork struct/ORM pilot

Kitwork exposes KitDB through the same schema-aware ORM used by SQLite. Raw
key/value operations remain kernel internals:

```javascript
import { database } from "kitwork";

const { kitdb, struct, id, text, int, choice, updated } = database;

const products = struct({
  id: id(),
  sku: text().notNull().unique().index(),
  title: text().notNull(),
  status: choice("active", "disabled").default("active").index("status_price"),
  price: int().default(0).check("price_nonnegative", ">=", 0).index("status_price"),
  updated_at: updated(),
});

const db = kitdb("catalog.kitdb", { products });

db.products.create({ sku: "KIT-1", title: "Kitwork", price: 10 });
const product = db.products.where("sku", "=", "KIT-1").first();
const active = db.products.where("status", "=", "active").limit(20).list();
const affordable = db.products
  .where("status", "=", "active")
  .where("price", ">=", 10)
  .where("price", "<", 100)
  .orderBy("price", "asc")
  .limit(20)
  .list();
const activeValue = db.products.where("status", "=", "active").sum("price");
const access = db.products.where("status", "=", "active").explain();

const receipt = db.transaction((tx) => {
  const order = tx.orders.create({ customer_id, status: "pending" });
  tx.inventory.where("sku", "=", sku).update({ available: nextAvailable });
  return { order: order.id, available: tx.inventory.find(sku).available };
});
```

A shared named `.unique()` declaration defines one ordered tuple without a
second schema language:

```javascript
const links = struct({
  id: id(),
  domain: text().notNull().unique("links_domain_code", 1),
  code: text().notNull().unique("links_domain_code", 2),
  title: text().default(""),
});
```

`.unique()` with no arguments remains a generated single-field constraint;
`.unique("products_sku_key")` gives one field an explicit constraint identity.
Every member of a named multi-field tuple may either declare an explicit
contiguous position starting at 1, as above, or omit positions together and use
field declaration order. The constraint identity is stable and its members are
persisted by immutable field tag, so an intentional `.from()` rename does not
silently create a new constraint.

A named `ref()` group references that ordered tuple without introducing a
second schema form:

```javascript
const orders = struct({
  id: id(),
  account_tenant: ref(accounts.tenant, "orders_account", 1, { onUpdate: "cascade", onDelete: "cascade" }).notNull(),
  account_code: ref(accounts.code, "orders_account", 2, { onUpdate: "cascade", onDelete: "cascade" }).notNull(),
});
```

Every member targets the same struct and uses the same action policy. A named
one-field `ref(target, "constraint_name")` may target a primary, generated
unique, or named unique field. Multiple target fields must exactly match one
named unique constraint in the same order. Members can all omit positions to
use declaration order. KitDB
uses SQL `MATCH SIMPLE` null behavior: if any local member is null, no parent
tuple is required. Local membership is persisted by immutable field tag and
target membership by immutable field ID, so deliberate `.from()` renames on
either side preserve the relationship. `NO ACTION`/`RESTRICT`, `CASCADE`,
`SET NULL`, and `SET DEFAULT` execute immediately through the same row/index
mutation pipeline and one record transaction. A restrictive descendant rolls
the complete action tree back. Trees are bounded to 10,000 affected rows and
depth 64; recursive updates that revisit an active row fail closed.

The compact field form `.check(">=", 0)` or named
`.check("price_nonnegative", ">=", 0)` creates a structured constraint without
accepting raw SQL. SQL DDL may declare bounded row expressions such as
`CHECK (discount IS NULL OR discount <= price)`. Both frontends normalize to
one persisted expression IR whose field references use immutable tags. The IR
is compiled before schema publication and evaluated on every create/update,
including referential updates. SQL three-valued semantics apply: only `FALSE`
fails; `NULL`/unknown passes. Adding or changing a check validates existing
rows before the catalog transaction is published.

`struct()` normalizes fields into versioned Schema IR with deterministic
struct/field identities, immutable numeric field tags, and a schema hash. Row
values use a checksummed binary tagged format rather than repeating field names
as JSON. The pilot supports the shared
`create`, bounded `createMany`, `where`, `find`, `first`, `list`, `sort`,
`limit`, `count`, `sum`, `avg`, `min`, `max`, `analyze`, `explain`, `exists`,
`update`, and `delete` surface.
Scalar aggregates stream all matching rows in one pass and do not inherit the
list result cap. `explain()` returns the exact primary, unique, secondary-index
equality-prefix, range, index-order, or scan access path consumed by execution.
It also reports the matched equality prefix, range field, residual filtering,
forward/reverse direction, whether ordering is index-provided, early-stop
eligibility, limit/offset, and snapshot/transaction source. Before statistics
exist it reports estimates as unavailable. `analyze()` scans one fixed
snapshot and transactionally stores exact table/index counts plus bounded
distinct-prefix sketches. Later row mutations set a per-struct dirty marker in
the same transaction as rows and indexes; unrelated table commits do not
invalidate it. Current statistics may then estimate candidate cardinality and
break otherwise equal deterministic planner scores; missing, schema-changed,
transaction-changed, or stale statistics never affect execution. This is
deliberately not a general cost-based optimizer. Primary keys,
not-null fields, single-field and composite-unique constraints, secondary
indexes, choice/default/timestamp behavior, structured checks, and all
immediate foreign-key actions are enforced atomically with each row
mutation. Schema IR types are data
constraints rather than display metadata: KitDB rejects fractional or textual
integers, non-finite numbers, invalid booleans and decimals, malformed JSON,
non-array array/vector values, non-numeric vector components, and text written
to blob fields before the row transaction commits.

`db.transaction(tx => ...)` composes reads and writes across structs into one
record transaction. Its fixed snapshot plus local overlay provides
read-your-writes for rows, unique claims, foreign keys, and secondary indexes.
The callback never holds the per-file writer gate. Commit acquires that gate
and succeeds only when the source transaction has not advanced; otherwise it
returns an explicit retryable transaction-conflict error instead of losing an
intervening write. A callback error, cancellation, failed constraint, or
conflict publishes none of its staged mutations. Nested transactions are
refused. The record transaction is bounded to 100,000 physical mutations and
32 MiB; each update/delete statement retains the 10,000-row mutation bound.
After `.safe()`, conflict is identified by the stable code
`KITDB_TRANSACTION_CONFLICT`. This is optimistic record-layer concurrency, not
kernel MVCC.

`createMany(rows, { batch: 128 })` ingests up to 10,000 rows per call using
ordinary record transactions of at most 256 rows. Every completed batch is
independently durable and uses the same type, constraint, reference, index,
WAL, and cancellation path as `create`. The returned object contains
`inserted`, `batches`, `next`, and `complete`; a failed batch rolls back in
full and also reports `failedRow`. After `.safe()`, distinguish no-progress
`KITDB_CREATE_MANY_FAILED` from resumable `KITDB_CREATE_MANY_PARTIAL`. Inside
`db.transaction()`, `createMany` accepts at most one batch and remains atomic
with the caller's other mutations. It is a bounded materialized application
API; PostgreSQL `COPY FROM STDIN` is the separate streaming transport surface.

The Kitwork adapter writes that Schema IR through the kernel catalog API. A
later Kitwork generation may open `kitdb("catalog.kitdb")` without redeclaring
the structs and hydrate its ORM and SQL-light metadata from the file itself.
Source declarations, when present, remain the desired schema used for migration
planning; catalog-only definitions are the durable current schema.

Files stay below the tenant's `.data` directory. AppRuntime owns relational
validation gates while the host-wide node manager owns bounded physical
handles rather than retaining every touched database forever. Reads use
immutable snapshots under operation leases. Point/equality queries prefer
primary, complete single/composite-unique equality, or declared secondary
indexes. The deterministic planner also uses the leading equality prefix of a
composite index, an optional lower/upper range on its next field, and
homogeneous ascending or descending index order when it matches `ORDER BY`.
Descending execution is a true reverse merge across immutable generations and
the WAL overlay, not a forward scan followed by reversal. A covered order stops
after `LIMIT + OFFSET` accepted rows instead of scanning and sorting the
remainder. Mixed directions and unsupported sortable kinds deliberately retain
bounded in-memory sorting; a row scan remains the fallback when no suitable
index exists. Current `ANALYZE` statistics may choose the lower-cardinality
candidate when deterministic structural scores tie. Fluent ORM reads retain
their 120-row hard cap. PostgreSQL and Hrana `SELECT` use a separate 1,000-row
remote cap: an explicit `LIMIT` is honored through that boundary and a larger
request is rejected rather than silently truncated.

Secondary indexes use a versioned, self-delimiting ordered codec. Fixed-width
number transforms and zero-escaped text components preserve the same ordering
used by predicates, including negative numbers, text prefixes, embedded zero
bytes, and composite boundaries. Existing primary and single-field unique key
encodings are unchanged; composite unique claims encode their ordered scalar
members self-delimitingly. As in SQLite and PostgreSQL, a composite tuple with
any `NULL` member creates no uniqueness claim, so multiple such rows are valid.
On first open, a legacy secondary-index set publishes one checksummed build
intent and the node rebuilds it into disjoint v2 keys in bounded chunks. The
physical state stores the exact target and last row key, so reopen resumes
rather than restarts. The planner uses no v2 index until the codec marker
commits with the final row chunk; legacy-key cleanup is then bounded and may
resume independently without taking v2 offline.

Online index transitions use physical codec v3. Existing v2 indexes are the
implicit generation zero and remain readable without an eager rewrite; every
new shadow index receives a nonzero generation ID in a disjoint keyspace. A
checksummed `KIGM` record publishes the active logical-signature-to-generation
map and a monotonic layout epoch. Replacement, predicate/order changes, and
removal therefore never overwrite an active index in place.

The adapter deliberately does not parse generated SQL. The fluent builder
exports storage-neutral query state, which KitDB executes directly. Existing
rows also pin their schema hash. A changed struct is refused by default; an
application must review `plan()` and explicitly enable planner-approved safe
migrations:

```javascript
const products = struct({
  id: id(),
  sku: text().notNull().unique().index(),
  name: text().from("title").notNull(),
  description: text().default(""),
});

const db = kitdb("catalog.kitdb", { products }, { migrate: true });
```

`.from("title")` preserves the stored field identity and numeric tag while
renaming `title` to `name`. It is a transition hint and can be removed after
the new catalog has committed. A pure rename is catalog-only, including for
legacy JSON rows through a persisted alias; a rename that changes a physical
index still rebuilds that index. An ordinary secondary-index addition,
replacement, or removal uses the same resumable builder. While row chunks run,
CRUD maintains both source-only active indexes and target shadow generations;
the planner cannot consume a shadow. The final chunk publishes the target
catalog, active generation map, layout epoch, migration audit, and cleanup
intent atomically. Read execution validates the epoch and opens its kernel
snapshot under the relational writer gate, so it observes either the old
complete layout or the new one. Retired generations are then deleted in bounded
resumable chunks; an already-open kernel snapshot keeps its older contents
pinned. Foreground admission persists one metadata-only `KIBS` transaction and
opens no row cursor, so its work is independent of table cardinality. The node
owns every row, cutover, and cleanup chunk through coalesced background
dispatches, reloading durable state each time. `PRAGMA index_status(table)`
reports that durable phase, row count, cursor presence, and publication state.
Catalog hydration resumes the work after restart without repeating `CREATE
INDEX`. The builder remains crash-resumable and permits correct CRUD between
chunks, but admission and each worker chunk still briefly hold the relational
writer gate, so strict zero-pause DDL is not claimed. Safe additions, unique
changes, choice/nullability validation,
defaults, and metadata changes that need row rewrites still commit their rows,
derived indexes, new catalog, and audit record in one bounded WAL transaction.
A failed validation leaves the old catalog and rows untouched.
For a source declaration containing several structs, KitDB plans and validates
the complete final dependency graph before opening that transaction. Catalog
definitions, bounded row rewrites, derived indexes, generation metadata, and
per-struct migration audits then publish together or not at all. The batch has
one shared audit ID/timestamp and may inspect or rewrite at most 10,000 rows in
total. A resumable ordinary secondary-index build is deliberately not mixed
with another logical schema change yet; that deployment fails closed and asks
the operator to finish the index-only change separately.

Schema IR v1 catalogs are upgraded automatically to stable tags without an
application migration flag. Existing JSON rows remain readable and become
binary when updated or touched by a rewriting migration. Binary readers retain
unknown tags byte-for-byte across updates so a rolling downgrade cannot
silently erase newer fields.

The atomic row/value-rewrite executor remains bounded to 10,000 rows.
Metadata-only changes, pure renames, nullable additions, and index-only
ordinary secondary-index transitions do not need that atomic rewrite. An
explicit SQL drop or type change above that boundary may use the segmented
row-migration executor when the changed field is independent of primary,
unique, secondary-index, partial-index, CHECK, and foreign-key layouts. It
performs a bounded 10,001-row admission scan, publishes a disjoint physical row
generation, then validates and materializes each source row in the same pass.
Source-schema reads continue and ordinary create/update/delete dual-write source
and target while each node-owned maintenance dispatch handles at most 2,048 rows
and 8 MiB per WAL transaction. The accepting ALTER performs the first bounded
pass and wakes one coalesced background task; a reopened catalog or compatible
repair commit wakes it again without retaining request state. An incompatible
row stops automatic progress at the previous durable cursor so it can be
repaired or deleted through the source schema. The final
chunk atomically publishes the target catalog, audit, active row generation,
layout epoch, and cleanup intent. Retired source rows are then removed in
resumable bounded chunks. Build and cleanup survive restart without an
unbounded writer-gated table scan; admission and each chunk still briefly hold
the relational writer gate, so this is not a strict zero-pause DDL claim. Larger
dependent field changes and unique/index generation changes remain future work.
`PRAGMA migration_status(table)` returns the durable processed/cleaned progress, source and
target identities/generations, publication state, and whether cancellation is
still safe. `ALTER TABLE table CANCEL MIGRATION` first disables target
dual-write durably, then removes the unpublished shadow generation in the same
bounded, restart-resumable background chunks. Repeating it is idempotent after completion.
It refuses legacy in-place migrations and any migration whose target has already
published; reversing a published schema requires a new migration.

An omission or type change in a source `struct()` remains refused when data
exists because source drift is not proof of destructive intent. SQL-light can
express explicit `RENAME COLUMN`, `DROP COLUMN`, or `ALTER COLUMN ... TYPE`;
those statements reuse the same planner, validation, tagged row codec, audit,
and WAL durability path. A catalog-owned table may also explicitly request
`ALTER TABLE name ALTER PRIMARY KEY (field, ...)`. This is a physical rekey,
not a catalog-only edit: KitDB builds a nonzero shadow row generation and new
ordered-index generations, then atomically publishes their catalog and layout
epochs before bounded cleanup. Source reads remain available while the build
runs; writes to that table are deliberately paused until cutover or
cancellation. The first contract is limited to one table with no independent
unique constraint or incoming/outgoing foreign key, and every target primary
value must be non-null and unique. Source-declared primary-key changes and
reference rewrites remain refused. Long-lived read snapshots can retain old
generations and delay background commit/checkpoint progress; read availability
is therefore not a claim that foreground scans have zero maintenance cost.
The fluent ORM does not yet expose joins, grouping/window aggregates, or the
search projection. The remote SQL-light adapter described
below does expose bounded indexed joins and `GROUP BY`/`HAVING` without
changing the ORM contract.

## Remote Hrana SQL-light profile

Hrana is an optional network adapter, not KitDB's internal query model or file
format. A tenant explicitly exposes a KitDB file with a token; omitting the
token leaves every remote endpoint closed:

```javascript
const db = kitdb("catalog.kitdb", { products }, {
  token: env.require("KITDB_TOKEN"),
  access: "readwrite",
});
```

The default access is `readonly`. Production deployments must put the endpoint
behind HTTPS because the token is a Bearer credential.

An ordinary libSQL client can then use the tenant URL and file path:

```javascript
import { createClient } from "@libsql/client";

const client = createClient({
  url: "https://shop.example.com/catalog.kitdb",
  authToken: process.env.KITDB_TOKEN,
});

const result = await client.execute({
  sql: "SELECT sku, price FROM products WHERE status = ? LIMIT ?",
  args: ["active", 20],
});
```

The experimental profile serves Hrana JSON pipelines at `/v2/pipeline` and
`/v3/pipeline` and has been exercised end-to-end with `@libsql/client` 0.17.4.
It accepts bound positional/named values and a fail-closed SQL subset:

- additive `CREATE TABLE [IF NOT EXISTS]` normalized into the same durable
  versioned Schema IR used by `struct()` (v2 base, promoted to v3 for sequence
  identity, v4 for constrained NUMERIC, or v5 for exact temporal precision),
  with one required primary key, supported
  typed columns, `NOT NULL`, single-field `UNIQUE`, named or deterministic
  table-level named single/composite `UNIQUE`, literal/default timestamp,
  `CHOICE`/`ENUM`, named or generated single/composite references with immediate
  `RESTRICT`/`NO ACTION`, `CASCADE`,
  `SET NULL`, or `SET DEFAULT`, plus named/unnamed column or table `CHECK`
  expressions. CHECK expressions reject parameters, aggregates, unknown fields,
  and non-boolean/non-numeric result types before catalog publication;
- additive `CREATE INDEX [IF NOT EXISTS]` for one or more ascending fields; it
  durably publishes one metadata-only intent for the same node-owned resumable
  builder used by the ORM rather than introducing a remote-only index format.
  Reads remain correct via fallback planning while a large accepted index is
  still building, and `PRAGMA index_status(table)` reports its durable state.
  `CREATE UNIQUE INDEX` accepts two or more fields and uses the bounded atomic
  unique-constraint migration path;
- bounded `DROP TABLE [IF EXISTS] name [RESTRICT|CASCADE]` for catalog-owned
  tables created through SQL. `CASCADE` is accepted for client compatibility
  when nothing depends on the target; actual dependent-constraint cascading is
  still refused. Source declarations owned by `struct()` are also refused. Row generations, unique
  keys, secondary indexes, migration state, physical metadata, and the catalog
  entry are removed in one WAL transaction. The operation fails closed above
  100,000 operations or 32 MiB instead of publishing a partial drop;
- `DROP INDEX [IF EXISTS] [schema.]name [RESTRICT|CASCADE]` for ordinary
  secondary indexes on catalog-owned tables. Logical removal uses the same
  checksummed resumable generation protocol as index replacement: the target
  catalog cuts over atomically and retired physical keys are cleaned in bounded
  node-owned chunks. Primary/unique constraint indexes and source-declared
  indexes are protected; remove an eligible constraint through its explicit
  table constraint identity instead;
- `ALTER TABLE [schema.]name RENAME TO new_name` for catalog-owned tables.
  The logical name changes while the immutable struct ID, row keyspace, field
  tags, and physical index keyspaces remain unchanged. Inline and named incoming
  foreign-key definitions are retargeted in the same WAL transaction, so no
  observer can see a half-renamed dependency graph. Existing implicit index
  names are materialized before the table rename and do not change as a side
  effect. The released table name may later be reused, but receives a fresh
  struct ID. Source-declared structs, source-declared dependents, and structs
  with unfinished row/index maintenance fail closed;
- `ALTER INDEX [IF EXISTS] [schema.]name RENAME TO new_name` for ordinary
  secondary indexes on catalog-owned tables. Schema IR persists an immutable
  physical index ID, so the operation changes catalog identity and remaps the
  active generation signature without rebuilding or aliasing index keys. A
  released name can be recreated with a new physical ID. Constraint-owned and
  source-declared indexes remain protected, and unfinished maintenance must
  complete before rename;
- `ALTER TABLE [schema.]name DROP CONSTRAINT [IF EXISTS] constraint
  [RESTRICT|CASCADE]` for single/composite `UNIQUE`, single/composite foreign
  keys, and `CHECK` constraints on catalog-owned tables. Primary keys and
  source-declared `struct()` constraints are protected. Incoming foreign keys
  make unique removal fail with `RESTRICT`; `CASCADE` is accepted only when no
  dependent constraint must be removed because dependent cascading is not yet
  implemented. Foreign-key and check removal is catalog-only. Unique removal
  validates the bounded migration and deletes its physical keys in the same WAL
  transaction; tables beyond 10,000 rows fail closed until unique generations
  exist;
- `ALTER TABLE [schema.]name ADD CONSTRAINT constraint UNIQUE (fields)`,
  `FOREIGN KEY (fields) REFERENCES target (fields)`, or `CHECK (expression)` on
  catalog-owned tables. Named unique and foreign-key constraints may contain
  one or multiple fields and keep the requested identity in Schema IR and
  PostgreSQL catalog views. Existing rows are validated before publication;
  unique keys, the target catalog, and its audit commit atomically through the
  existing bounded migration transaction. Violations publish nothing. Primary
  key addition and source-declared `struct()` changes are refused, and tables
  requiring inspection beyond 10,000 rows fail closed until segmented
  constraint validation/generation exists;
- `ALTER TABLE ... ADD COLUMN [IF NOT EXISTS]`; nullable additions are
  catalog-only, while safe defaults/constraints reuse the bounded atomic
  backfill and unsafe additions are refused before publication. Explicit
  `RENAME COLUMN old TO new` preserves the stable field tag and persisted alias.
  Single-field foreign keys that target the renamed field update in the same
  catalog transaction; composite references already follow immutable field IDs.
  `DROP COLUMN [IF EXISTS]` refuses primary, indexed, unique, CHECK, or
  foreign-key-dependent fields. `ALTER COLUMN field TYPE type` applies
  deterministic casts, rebuilds derived keys, and validates every row and
  constraint. Up to 10,000 rows publish atomically; larger physically
  independent field changes use checksummed shadow row generations,
  2,048-row/8-MiB chunks, source dual-write, atomic epoch/catalog cutover, and
  bounded retired-prefix cleanup. A failed online cast leaves the source catalog
  active and the last successful shadow cursor resumable; it never publishes the
  target catalog. `PRAGMA migration_status(table)` inspects that durable state;
  `ALTER TABLE table CANCEL MIGRATION` safely abandons and incrementally removes
  only an unpublished shadow target. Casts cover
  scalar text/numeric/boolean transitions, compatible temporal text,
  decimal, and JSON/array/vector representations; unsupported shapes,
  non-finite numbers, fractional integers, and text integers outside the exact
  numeric range fail closed;
- `SELECT` fields, bounded `SELECT DISTINCT` over scalar or computed
  projections, scalar expressions without `FROM`, or scalar
  `COUNT(*)`/`COUNT(field)`/`SUM`/`AVG`/`MIN`/`MAX`. A bounded typed expression
  IR supplies parenthesized arithmetic (`+`, `-`, `*`, `/`, `%`), `||`,
  searched/simple `CASE`, and `COALESCE`/`IFNULL`/`NULLIF`/`LOWER`/`UPPER`/
  `TRIM`/`LENGTH`/`ABS`/`ROUND`. The same evaluator is used by projections,
  expression ordering, JOIN output, grouped output, `WHERE`, and `HAVING`;
- SQL-precedence `WHERE` expressions with parentheses, `AND`/`OR`/`NOT`,
  comparisons, `IN`, `BETWEEN`, `LIKE`, and `IS [NOT] NULL`, `ORDER BY`, and
  bounded `LIMIT`/`OFFSET` including SQLite's `LIMIT offset, count` form;
  ordinary and catalog ordering accepts unambiguous projection aliases;
  multiple scalar aggregates share one streaming pass. SQL three-valued NULL
  logic is retained. Planner-safe predicates and conjunctive fragments still
  feed equality-prefix/range planning; the complete expression is then checked
  as a residual filter when needed. Guarded `UPDATE`/`DELETE` evaluate that
  complete residual expression on the same transaction snapshot used for the
  write; only matching primary keys enter the bounded mutation set;
- bounded `GROUP BY` over one or more scalar fields, with ordinary projected
  fields required to be group keys. All aggregates for a group share one source
  pass; computed projections may combine grouped fields and aggregate states,
  including arithmetic and `CASE`. `HAVING` and `ORDER BY` consume those same
  computed references instead of re-running a separate evaluator.
  The operator allows at most 8 group fields, 16 distinct aggregate states,
  32 projections, and 10,000 intermediate groups. It defaults to 60 result
  rows and retains the 1,000-row remote result cap. A complete non-partial
  ordered index can answer exact leading-key grouping with optional `COUNT(*)` by
  counting key runs without fetching KROW. A covering ordered index can also
  answer filtered or unfiltered `COUNT`, `SUM`, `AVG`, `MIN`, and `MAX` when it
  contains every predicate, group, and aggregate field; it preserves exact
  integer and NULL semantics, rechecks covered residual expressions, and
  includes transaction-overlay mutations without KROW point lookups.
  Unsupported shapes retain the same batch/scalar fallback and bounds;
- one `INNER JOIN` or `LEFT [OUTER] JOIN` per SELECT with aliases, qualified
  fields, equality `ON`, computed projections, expression-aware post-join
  `WHERE`, alias/expression `ORDER BY`, and
  SQL NULL-extension semantics. Both structs read one KitDB snapshot (or one
  record-transaction overlay). The joined field must be primary, unique, or
  the first field of an active non-partial index; KitDB refuses a repeated
  full scan. Scalar aggregates, `GROUP BY`/`HAVING`, and `DISTINCT` compose on
  the joined stream without opening a second snapshot. Execution is capped at
  64 ordinary projected fields, 8 order fields, 10,000 source rows, 20,000
  candidate pairs, and 1,000 returned rows; grouped joins also retain the group
  operator's 8-key/16-aggregate/32-projection/10,000-group bounds;
- `EXPLAIN SELECT` and `EXPLAIN QUERY PLAN SELECT`, returning the real KitDB
  access path, scan direction, statistics state/watermark, and a bounded
  cardinality estimate when current statistics exist. It never fabricates an
  estimate from stale metadata and does not claim a general cost model;
- `ANALYZE [table]`, which scans each selected struct through one fixed
  optimistic snapshot and publishes checksummed statistics while removing that
  struct's dirty marker through the ordinary WAL. A concurrent commit conflicts
  instead of allowing a stale analysis to appear current. `PRAGMA
  statistics(table)` exposes exact row/index-entry counts, bounded
  distinct-prefix estimates, status, and the analyzed transaction watermark;
- one-row/default/all-field and atomic multi-row `INSERT` capped at 256 rows,
  plus guarded `UPDATE` and `DELETE`. `UPDATE SET` accepts the same bounded
  scalar expression IR as reads; every assignment observes the original row,
  then all assignments are validated and applied together. A mutation may
  match at most 10,000 rows, and `RETURNING` is rejected before publication if
  it would exceed 120 rows. Constraint, expression, cancellation, or
  `RETURNING` validation failure rolls the whole statement back. `ON CONFLICT
  [(primary_or_unique_fields)] DO NOTHING` and targeted `DO UPDATE SET` accept
  primary, single-field unique, or complete composite-unique targets in any
  written order; literals or `excluded.field` supply update values. Mutation
  statements preserve KitDB schema
  validation, indexes, references, and one-transaction publication;
- one bounded Hrana `batch` may contain `BEGIN ... COMMIT` or `BEGIN ...
  ROLLBACK` in the same HTTP request. Its statements share the record snapshot
  and overlay, commit through one WAL transaction, and are capped at 256 steps;
- bounded `RETURNING`, `sqlite_master`/`sqlite_schema`, and the common
  `table_info`, `index_list`, `index_info`, and `foreign_key_list` pragmas.

SQL `CREATE TABLE` accepts the same relationship as table-level `FOREIGN KEY
(local_a, local_b) REFERENCES parent (target_a, target_b)`, with an optional
`CONSTRAINT name`. It is normalized into the composite Schema IR above;
`foreign_key_list` returns one shared foreign-key ID and ordered `seq` rows.

Primary keys may also be ordered tuples. SQL uses `PRIMARY KEY (merchant, id)`;
the Kitwork schema equivalent is `merchant: text().key(1)` and
`id: int().key(2)`. KitDB encodes all members into the physical logical row
identity, so equality on the complete tuple is one primary lookup rather than
a secondary-index hop. Each member is `NOT NULL`, but no member is incorrectly
reported as individually unique. The hidden ordered primary access path covers
tuple prefixes and `PRAGMA table_info` reports PostgreSQL/SQLite-compatible key
ordinals `1..N`. A foreign key may reference the complete ordered tuple, never
one non-unique component of it.

For a catalog-owned table, SQL may change that tuple explicitly with
`ALTER TABLE shopping ALTER PRIMARY KEY (merchant, id)`. A bounded admission
publishes a durable `KRMS` rekey intent, after which the node worker copies rows
and rebuilds every ordered index in restart-resumable chunks. A rekey chunk may
inspect at most 8,192 rows but remains capped at 8 MiB and 16,384 mutations. The source
generation continues serving reads, but writes to the table fail with a clear
paused-migration error so the old and new logical identities cannot diverge.
Null or invalid target tuples stop the build at its previous durable cursor.
Same-chunk collisions fail immediately; cross-chunk collisions reduce the
number of unique target row keys and are caught by a second bounded
`validating` pass of at most 65,536 keys per durable cursor advance before
publication. `PRAGMA migration_status(shopping)`
reports `mode = 'rekey'`, durable build/verification progress, the write fence,
publication state, and cleanup-prefix progress. Before publication,
`ALTER TABLE shopping CANCEL MIGRATION`
returns to the old primary key and removes shadow row/index generations in
chunks of at most 8,192 keys/8 MiB. Post-publication retired-prefix cleanup uses
the same bound. After publication, reversal is another explicit primary-key
migration.

`CREATE TABLE` commits through the ordinary catalog WAL and is published to
the live Kitwork ORM only after durability; a catalog-only declaration hydrates
again after restart. `IF NOT EXISTS` is an idempotent no-op for an existing
case-insensitive table, field, or index name. Line and block SQL comments are
accepted without weakening the one-statement boundary. Logical index and named
unique-constraint names share one case-insensitive database-wide namespace, matching
their unqualified `sqlite_master`/`PRAGMA index_info` identity. The first profile still refuses
source-declared or unbounded/online table drop, cascading dependent constraints,
constraint-backed/source-declared index drop, source-declared table/index rename,
source-declared primary-key changes, primary rekeys with unique or foreign-key
dependencies, dropping dependent fields,
source-declared constraint changes, unbounded constraint addition/removal,
unbounded destructive migration or generation changes for dependent fields,
single-field `CREATE UNIQUE INDEX` (use column `UNIQUE` instead),
descending/partial index DDL,
multiple/non-equality/right/full/cross joins, subqueries, set operations,
`UPDATE FROM`, joined deletes,
expressions as `GROUP BY` keys or non-scalar group keys, grouped
catalog metadata, window functions, disk-spilling aggregation,
multiple SQL statements in one SQL string, WebSocket/Protobuf/cursor variants,
and interactive transactions spanning pipeline requests or batons. Unsupported
syntax returns an explicit Hrana
error instead of being approximated. Remote result reads default to 60 rows,
honor an explicit `LIMIT` through 1,000 rows, and reject larger requests. A
simpler authenticated, read-only JSON endpoint is also available
at `POST /_db/query`.

## Standalone PostgreSQL wire profile

KitDB has a bounded pure-Go PostgreSQL protocol 3.0 adapter. The standalone
launcher opens one file directly; it does not create a Kitwork Tenant or load a
VM, route, domain, app folder, or source declaration:

```text
go run ./cmd/kitdbpg \
  -file ./products.kitdb \
  -database products \
  -listen 127.0.0.1:5433 \
  -retain-history
```

The PostgreSQL user defaults to `kitdb`; set the password with `KITDB_TOKEN` or
`-password`. Connect directly to the logical database name with
`sslmode=disable`:

```text
postgres://kitdb:<token>@127.0.0.1:5433/products?sslmode=disable
```

This direct-file profile currently guarantees the bounded SQL surface listed
in [`relational/README.md`](relational/README.md). It does not inherit broader
Kitwork-hosted features merely because both adapters share the same file.
That surface includes `UNION ALL` across at most 16 exact-type SELECT branches
on one snapshot, with first-branch output names, one global name/ordinal
`ORDER BY`, and bounded `LIMIT`/`OFFSET`. It also includes non-recursive `WITH`
queries and aliased derived tables on that same snapshot, with query-wide row,
32 MiB materialization, CTE-count, field-count, and nesting bounds. Unordered,
non-distinct row and scalar-expression materialization streams directly from
the fixed snapshot into the bounded temporary relation; each row is admitted
before its retained map is allocated. `EXPLAIN ANALYZE` reports both total and
directly materialized rows. Every variable-cardinality buffered executor shares
the same 32 MiB ceiling: `ORDER BY` and `DISTINCT` reserve source/result rows,
duplicate identities, and Top-N replacements; scalar and batch `GROUP BY`
reserve keys, group values, exact accumulators, HAVING scratch, and result rows;
indexed JOIN reserves per-source intermediate environments plus durable output;
and `UNION ALL` preserves branch-row ownership while reserving merged row
references. Admission occurs before each structure becomes retained, and JOIN
scratch is released after each source row. `EXPLAIN ANALYZE` reports both
retained and peak materialization bytes. These executors remain memory-bounded,
not spillable. Physical SELECT validates each complete binary KROW but decodes
only tags required by output, filtering, and ordering; buffered SELECT retains
only output/order fields. The reusable tag directory is built once per query,
while full CRUD decoding still preserves unknown durable fields. This is an
execution optimization and does not change KROW/WAL format. The retained
13.77-million-row evidence and spill decision are in
[`SHOPPING_13M_MEMORY_2026-09-04.md`](../benchmarks/dbcompare/SHOPPING_13M_MEMORY_2026-09-04.md).
Recursive
CTEs, correlated subqueries, SEARCH materialization, and joins whose source or
target is materialized remain explicit gaps. The separate Kitwork Hrana
SQL-light executor does not support these standalone set-operation or
materialization features.

The same standalone command can instead expose every regular non-hidden
`.kitdb` file immediately below one directory:

```text
go run ./cmd/kitdbpg \
  -root ./databases \
  -maintenance-database kitdb \
  -listen 127.0.0.1:5433 \
  -max-open-databases 64 \
  -max-page-cache-bytes 268435456 \
  -database-page-cache-bytes 1048576
```

The virtual read-only `kitdb` database exposes the discovery snapshot through
`pg_database` without opening user files. A client reconnects with a selected
logical database name, such as `products` for `products.kitdb`; PostgreSQL does
not switch databases inside an existing connection. Files are opened lazily,
same-database sessions share one relational/search owner, and idle owners are
LRU-evicted within the node limits. Directory discovery is dynamic but bounded,
non-recursive, and rejects symlinks and ambiguous suffixless names.

This standalone node is deliberately smaller than the Kitwork-hosted catalog:
one endpoint credential grants access to all discovered files, and maintenance
does not provide `CREATE`, `ALTER`, recovery, or `DROP DATABASE`. Those lifecycle
operations remain host-authorized contracts described below. Both profiles are
loopback cleartext only and require one owning process per live file.

## Kitwork-hosted multi-database adapter

Kitwork also composes the transport with its host-owned capability and node
catalog. That separate integration can expose a maintenance database and
multiple authorized files. It remains a client/host of KitDB rather than a
dependency of the standalone engine.

From that maintenance connection, create a fresh empty KitDB with ordinary
SQL:

```sql
CREATE DATABASE shop;
```

The short form is accepted only when the authenticated session has exactly one
source-declared KitDB capability. If the same token authorizes several source
capabilities, select the authority explicitly instead of letting physical or
iteration order decide:

```sql
CREATE DATABASE shop WITH CAPABILITY products;
```

The new database has its own durable identity and an empty Schema IR catalog.
Reconnect to `shop`, then use the existing SQL-light `CREATE TABLE`, index, and
CRUD surface. `WITH CAPABILITY` does not copy a token or clone the named
database: it records only which source declaration supplies current
authentication and access policy. The authority must be writable, and a
SQL-managed database cannot become another authority.

Point-in-time recovery is explicit per source database so an unbounded history
directory is never enabled accidentally:

```javascript
const db = kitdb("products.kitdb", { products }, {
  token: env.DATABASE_TOKEN,
  access: "readwrite",
  recovery: true,
});
```

Production deployments can bound that recovery window without changing the
database file format:

```javascript
const db = kitdb("products.kitdb", { products }, {
  token: env.DATABASE_TOKEN,
  access: "readwrite",
  recovery: {
    maxAge: "7d",
    maxBytes: "2gb",
  },
});
```

`maxAge` accepts Kitwork durations and `maxBytes` accepts a positive byte count
or `kb`/`mb`/`gb`/`tb` size. Supplying both applies whichever requires pruning
more old whole segments. Durable backup, replica, and projection pins can keep
history beyond either bound; node stats expose that pressure.

After history has covered the requested time, the node listener accepts one
canonical operator statement outside a transaction:

```sql
CREATE DATABASE products_recovered
FROM products
AS OF TIMESTAMP '2026-08-27T14:30:00+07:00';
```

The command checkpoints the source, verifies and resolves the retained chain,
publishes an independent fresh-identity fork, hydrates its durable Schema IR,
and exposes it immediately in `pg_database` under the source capability token.
Logical SQL names omit `.kitdb`; the physical destination is
`products_recovered.kitdb`. Existing destinations are never overwritten.
The target and its lifecycle entry both remain durable after restart. The node
stores SQL-managed database metadata in the reserved tenant-local
`.data/.kitdb-node.kitdb`, itself an ordinary KitDB file using the same WAL and
recovery path. Its bounded binary records contain the logical/storage names,
target database identity, lifecycle state, and a reference to the source
capability. They never contain the capability token. On restart the adapter
resolves that reference against current application declarations, so token and
access changes remain source-controlled while SQL-managed targets need no
duplicate `kitdb(...)` declaration. New PostgreSQL sessions and HTTP requests
resolve the same current capability rather than trusting a credential copied
when the target was registered. If that authority is unavailable, its managed
targets are hidden rather than accepting stale credentials.

Creation publishes a durable `creating` intent before either an empty target or
a recovery fork is published, and changes it to `active` only after the
complete target exists and its fresh identity is known. Startup removes an
intent with no storage and verifies/completes one whose target was already
published. Active targets stay cold: startup checks only bounded metadata and
file presence; the first real open verifies the file identity against the
catalog before serving data.

An idle SQL-managed database can be renamed from the maintenance connection:

```sql
ALTER DATABASE shop RENAME TO warehouse;
```

This is a PostgreSQL-style logical rename, not a filesystem move. The catalog
atomically replaces logical key `shop` with `warehouse` while preserving the
original `shop.kitdb` storage path, database identity, WAL, history, and pins.
The old connection name stops working immediately, the new name survives
restart, and `pg_database` updates for already authorized maintenance sessions.
Rename is refused while a target session is active, when the destination
exists, or for a source-declared database; source names are changed in Kitwork
configuration instead.

SQL-managed empty databases and recovery forks created by that listener may
also be removed with the matching operator statement. Disconnect every client
from the target, reconnect to the maintenance database `kitdb`, and run the
command outside a transaction:

```sql
DROP DATABASE products_recovered;
-- Repeated cleanup may use:
DROP DATABASE IF EXISTS products_recovered;
```

The node rejects attempts to drop the current database, a database with active
PostgreSQL sessions or engine leases, a replica-linked database, and a database
containing `struct(...)` definitions owned by application source. It also
rejects dropping a source capability while any SQL-managed database references
it; remove those dependent databases first. A successful
SQL-managed drop hides the capability, durably changes `active` to `dropping`,
removes the canonical `.kitdb`, WAL, history, and lock files through the node
lifecycle API, and only then deletes the catalog entry. Startup finishes an
interrupted `dropping` entry idempotently. This ordering prevents new sessions
from racing physical removal or a crash from re-exposing a partially removed
database. `DROP DATABASE` is intentionally available only through the
node-mode PostgreSQL maintenance database; it is not accepted by a single-file
Hrana endpoint.

The catalog publication order has a hard-process matrix, not only a clean
close/reopen test:

```text
go test ./work -run '^TestKitDBNodeCatalog(EmptyCreate|Rename)?HardCrashMatrix$' -count=10
```

The child exits without closing handles at seven create/drop boundaries for a
recovery fork and at all three create-publication boundaries for an empty
database, plus both sides of atomic rename publication. A fresh runtime must
verify, activate, roll back, rename, or finish removal
solely from the catalog and target files described above.

Its virtual `pg_database` lists source-declared and SQL-managed KitDB databases
authorized by the same current capability token. Selecting one in a database manager opens a new
PostgreSQL connection whose startup database binds the session to exactly that
KitDB file. PostgreSQL exposes a logical name without the storage suffix: a
declaration named `products.kitdb` is listed, selected, and returned by
`current_database()` as `products`. Clients may still send `products.kitdb` as
a compatibility alias, but the virtual catalog never emits it. Declarations
without the suffix use the same logical name. Ambiguous declarations such as
both `products` and `products.kitdb` are refused by the adapter. The maintenance
database has no user structs and refuses data SQL. The standalone launcher's
`-database products` option is not part of this hosted catalog flow; it instead
binds its one `-file` directly to that logical name.

The first profile implements bounded startup/password authentication, Simple
Query, the core Parse/Bind/Describe/Execute/Sync extended flow, positional
`$1` bindings, text results, binary integer/bytea/NUMERIC and exact temporal
results, SQLSTATE errors,
connection/query/message limits, cancellation, and common startup/session
probes such as `version()`, `current_database()`, and `SHOW` values. It has
end-to-end interoperability tests using `database/sql` plus `lib/pq` for auth,
token-scoped database selection and isolation, scalar SELECT, CRUD, extended
queries, binary results, error codes, and schema discovery. KitDB projects its
durable Schema IR into bounded virtual
`information_schema.tables`, `information_schema.columns`, table/key constraints,
`pg_database`, `pg_namespace`, `pg_class`, `pg_attribute`, `pg_type`, `pg_index`,
`pg_constraint`, and `pg_indexes` records so database managers can browse fields,
primary keys, unique constraints, ordinary indexes, checks, and foreign keys.
Those records are derived from Schema IR, not persisted as a second catalog, and
therefore cannot drift from the KitDB schema.

One connection may hold a bounded interactive data transaction across
`BEGIN`, multiple `SELECT`/`EXPLAIN SELECT`/`INSERT`/`UPDATE`/`DELETE`
statements, and `COMMIT` or `ROLLBACK`. The first data statement lazily opens
one fixed snapshot; later statements read their own writes, while other
connections see nothing until all mutations publish through one ordinary WAL
transaction. Commit fails with SQLSTATE `40001` if another writer advances the
database or its catalog, and any statement error moves the session to
PostgreSQL's failed transaction state until rollback. A transaction is bounded
to 256 statements, the record layer's 100,000 operations and 32 MiB overlay,
and a one-minute lifetime by default. `-transaction-timeout` changes that
lifetime. Timeout, cancellation, socket disconnect, and server shutdown all
share the same idempotent rollback path and release the snapshot and generation
lease. `SHOW transaction_isolation` reports `repeatable read`, matching the
fixed-snapshot contract rather than claiming PostgreSQL `read committed`
semantics.

The profile also accepts PostgreSQL text and CSV streams without materializing
the complete input in a SQL statement:

```sql
COPY products (id, sku, title, price)
FROM STDIN;

COPY products (id, sku, title, price)
FROM STDIN
WITH (FORMAT csv, HEADER true, DELIMITER ',', NULL '');
```

In `psql`, the client-side `\copy products (...) FROM 'products.csv' WITH
(FORMAT csv, HEADER true)` command opens that same `COPY FROM STDIN` protocol.
Drivers may use their ordinary COPY helper; `database/sql` plus `lib/pq.CopyIn`
is covered by the end-to-end suite. Network frames may split a text row, CSV
quoted field, escaped quote, CRLF, or embedded newline at any byte. The decoder
keeps only the current bounded record plus the ordinary record-transaction
overlay, converts fields through Schema IR, and then uses the same defaults,
not-null/choice/check/reference/unique constraints, row codec, indexes, dirty
statistics marker, commit conflict, and WAL publication as `INSERT`.

One COPY is atomic. A malformed field, constraint failure, client `CopyFail`,
cancel request, timeout, disconnect, transaction conflict, or server shutdown
rolls back every row from that COPY. Inside an explicit PostgreSQL transaction,
rows remain invisible to other connections until `COMMIT`, and a COPY error
moves the session to failed-transaction state until `ROLLBACK`. The server
drains bounded trailing `CopyData` before replying to an engine-side error so
the connection remains protocol-synchronized.

This first profile is intentionally bounded rather than pretending one
statement can own an arbitrary file. A record is capped at 4 MiB, encoded COPY
input defaults to 64 MiB, and the existing transaction ceiling remains 100,000
physical mutations or 32 MiB of overlay. COPY lifetime defaults to 30 minutes;
an explicit transaction's shorter lifetime also applies. A Kitwork host that
exposes this richer adapter must configure copy bytes, copy/transaction timeout,
and concurrent-copy bounds explicitly. Admission defaults to two streams across the listener,
one active stream per authenticated app/database key, 64 queued streams across
the listener, and eight queued streams per database. Admission happens before COPY opens its KitDB
transaction. Queued keys use bounded weighted virtual runtime; Kitwork sessions
currently have equal weight, so a busy database cannot bypass an unserved
database or occupy both default slots. A host can pass `pgwire.CopyMetrics` and
read atomic active/queued peaks, completed/failed/rejected counts, bytes, and
wait snapshots. Fairness covers identities sharing this listener; independent
tenant listeners do not yet share one fleet scheduler.

Large CSV and JSONL files use the separate resumable importer. It splits one
seekable source into repeated bounded COPY transactions and advances one
checksummed `KIMP` watermark in the same WAL transaction as each chunk's rows:

```powershell
go run ./cmd/kitdbimport `
  -url "postgres://kitdb:secret@127.0.0.1:5433/products?sslmode=disable" `
  -file products.csv -format csv -header `
  -table products -columns id,sku,title,price `
  -id products-2026-08 -chunk-rows 1000
```

The same command owns the durable lifecycle:

```powershell
go run ./cmd/kitdbimport -action status `
  -url "postgres://kitdb:secret@127.0.0.1:5433/products?sslmode=disable" `
  -id products-2026-08

go run ./cmd/kitdbimport -action verify `
  -url "postgres://kitdb:secret@127.0.0.1:5433/products?sslmode=disable" `
  -file products.csv -format csv -header -table products `
  -columns id,sku,title,price -id products-2026-08

go run ./cmd/kitdbimport -action cancel `
  -url "postgres://kitdb:secret@127.0.0.1:5433/products?sslmode=disable" `
  -id products-2026-08

go run ./cmd/kitdbimport -action forget `
  -url "postgres://kitdb:secret@127.0.0.1:5433/products?sslmode=disable" `
  -id products-2026-08 -confirm-forget products-2026-08
```

`PRAGMA import_status('products-2026-08')` exposes the durable chunk, source
row/byte offset, cumulative SHA-256 chain, completion/cancellation flags, and KitDB
transaction watermark. On resume, `kitdbimport` reparses the committed source
prefix and verifies its row count, exact byte offset, seed, and semantic hash
before writing anything new. A crash after database commit but before client
acknowledgement is safe: replay sees the already-advanced KIMP value and does
not insert the chunk twice. Two clients racing the same import ID converge
through KitDB's optimistic commit conflict. Use a new import ID only to begin an
intentionally independent import.

`PRAGMA import_cancel(id)` durably makes an incomplete ID terminal and is
idempotent. `PRAGMA import_forget(id)` accepts only a completed or cancelled ID
and removes its KIMP checkpoint. Neither operation removes rows from committed
chunks. The CLI therefore requires `-confirm-forget` to exactly match the ID.
Hard-process recovery at the publication, WAL-before-acknowledgement, and
post-checkpoint boundaries is covered by:

```text
go test ./work -run ^TestKitDBPostgresResumableImportHardCrashMatrix$ -count=10
```

The importer buffers at most one configured chunk plus one source record; it
does not materialize the complete file. Defaults are 1,000 rows and 8 MiB per
chunk, with hard client ceilings of 10,000 rows and 32 MiB. The server's lower
physical-mutation, overlay, wire-byte, record, and lifetime ceilings still win.
This is not one unbounded transaction, and KIMP is adapter metadata inside the
KitDB file rather than a sidecar or a second durability path.

For database-manager schema DDL, the profile recognizes the narrow
`BEGIN; DROP TABLE ...; COMMIT`, `BEGIN; DROP INDEX ...; COMMIT`, and
`BEGIN; ALTER TABLE ... ADD/DROP CONSTRAINT ...; COMMIT`,
`BEGIN; ALTER TABLE ... ALTER PRIMARY KEY ...; COMMIT`,
`BEGIN; ALTER TABLE ... RENAME TO ...; COMMIT`, and
`BEGIN; ALTER INDEX ... RENAME TO ...; COMMIT` envelopes used by
clients such as TablePlus. The DDL is staged until `COMMIT`, `ROLLBACK` really
discards it, and
ReadyForQuery reports PostgreSQL's transaction state. Exactly one
supported schema DDL statement is allowed in that envelope, and it cannot be
mixed with data statements in the same transaction.

This is intentionally a local development/operator profile. Cleartext password
exchange is accepted only on a loopback listener. It has no TLS, SCRAM,
savepoints, deferred constraints, complete `pg_catalog`/
`information_schema`, or general PostgreSQL dialect compatibility yet. DDL is
still a deliberately narrow staged envelope rather than transactional schema
mutation in the record kernel. Hrana remains the stateless URL-oriented
transport. Do not expose this listener to a network or describe KitDB as
PostgreSQL-compatible.

The visible storage unit is:

```text
tenant.kitdb       canonical main snapshot
tenant.kitdb.wal   active transaction tail
tenant.kitdb.lock  stable process-writer lock
tenant.kitdb.history/META
                   optional retained-history identity and base
tenant.kitdb.history/PINS
                   optional durable retention watermarks
tenant.kitdb.history/base-*.kbase
                   verified point-in-time recovery base
tenant.kitdb.history/*.khist
                   optional immutable WAL ranges
```

Bytes after the active slot's declared file boundary are abandoned tail and
are ignored on recovery. The next incremental checkpoint truncates that tail
before appending. Temporary compaction files are written beside the database
and never selected by readers.

New databases use format v3. Format-v1 and format-v2 files remain readable and
are upgraded by the next checkpoint, including an explicit checkpoint with no
newer transaction.

At most 32 active segments are allowed. A later checkpoint at that bound
triggers a streaming compaction into one base segment, removes obsolete
mutations and tombstones, and atomically replaces the main file. Normal
checkpoints write only their delta plus bounded metadata; compaction and legacy
migration scan and rewrite the logical database. See `NON_GOALS.md`,
`INVARIANTS.md`, and `FORMAT.md` before building on the package. KitDB remains
experimental and is not production-ready storage.
