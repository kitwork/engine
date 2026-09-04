# KitDB Storage Format v3 and Replica Wire v1 (KitDB v0.16)

All integers are little-endian. Sizes count bytes. Readers reject nonzero
reserved fields and format versions they do not understand. CRC32C uses the
Castagnoli polynomial and detects accidental corruption, not malicious edits.

KitDB v0.16 does not change the main-file v3, WAL v1, history v1 segment, `META`,
or `PINS` bytes introduced by earlier milestones. One-way replica bootstrap
reuses a canonical format-v3 backup anchor, and catch-up commits the source's
verified transaction frames through the target's ordinary WAL. No replica
cursor sidecar exists: the target main/WAL transaction and boundary checksum
are its durable restart cursor. Retained history is not a main-state recovery
candidate. Replica wire v1 and its filesystem mailbox are transport artifacts,
not members of the live database storage set. SHA-256 remains artifact metadata
rather than a main-file or WAL field.

## Files

For `Open("tenant.kitdb")`, the storage set is:

```text
tenant.kitdb       canonical generation main file
tenant.kitdb.wal   transactions after the snapshot base
tenant.kitdb.lock  stable exclusive-writer lock
tenant.kitdb.history/META
                   optional retained-history metadata
tenant.kitdb.history/PINS
                   optional durable retention watermarks
tenant.kitdb.history/*.khist
                   optional immutable WAL ranges
```

Incremental checkpoints append to the main file. Compaction and WAL staging
files are created in the same directory, synced, and atomically replaced.
Staging names are never recovery candidates.

A backup anchor is an independently named, exact-boundary format-v3 main file.
It is not a required sidecar in the live storage set shown above.

## Tenant node database catalog v2

The node-mode PostgreSQL adapter reserves `.data/.kitdb-node.kitdb`. This is a
normal format-v3 KitDB database with its own `.wal` and `.lock`; no new main-file
or WAL format is introduced. Tenant schema code cannot claim this filename.
The database is not remotely exposed and stores only lifecycle metadata for
SQL-managed databases under keys prefixed by the ASCII bytes
`kitdb/node/database/v1/` followed by the logical database name.

Each value has a 40-byte header, variable UTF-8 payload, and trailing CRC32C:

| Offset | Size | Field |
| ---: | ---: | --- |
| 0 | 8 | magic `KITNDC01` |
| 8 | 2 | version `2` (`1` remains readable) |
| 10 | 2 | header size `40` |
| 12 | 1 | state: `1` creating, `2` active, `3` dropping |
| 13 | 3 | flags/reserved, zero |
| 16 | 8 | positive UTC creation time as Unix nanoseconds |
| 24 | 2 | storage-name byte length |
| 26 | 2 | capability-storage byte length |
| 28 | 2 | database-identity text byte length |
| 30 | 2 | reserved, zero |
| 32 | 4 | combined payload byte length |
| 36 | 4 | reserved, zero |
| 40 | variable | storage name, capability storage, then hex database identity |
| end-4 | 4 | CRC32C of every preceding value byte |

Creating entries require an empty identity. Active and dropping entries require
exactly 32 hexadecimal characters encoding the target's 16-byte identity. The
complete value is capped at 1,024 bytes and the catalog at 4,096 entries. It
stores a capability storage reference, never a token or password.

Version 1 requires `storage_name == logical_name + ".kitdb"`. Version 2
deliberately separates those identities: `ALTER DATABASE ... RENAME TO ...`
changes the catalog key and logical name atomically while preserving the
original storage name and database identity. A storage name remains one safe
tenant-local basename ending in `.kitdb`, and two entries may never reference
the same storage.

## Format-v3 main file

The first 4096 bytes are a fixed publication area. Immutable segments and
manifests are appended at or after offset 4096.

### Static header

The 64-byte header is written once:

| Offset | Size | Field |
| ---: | ---: | --- |
| 0 | 8 | magic `KITDBM03` |
| 8 | 2 | format version, currently `3` |
| 10 | 2 | header size, currently `64` |
| 12 | 4 | flags: bit `0` means the payload carries a commit timestamp |
| 16 | 16 | random database identity |
| 32 | 28 | reserved, currently zero |
| 60 | 4 | CRC32C of bytes `[0, 60)` |

### Generation slots

Two 128-byte slots start at offsets 64 and 192. A checkpoint writes the slot
that is not currently active.

| Offset | Size | Field |
| ---: | ---: | --- |
| 0 | 8 | magic `KITDBS03` |
| 8 | 2 | format version, currently `3` |
| 10 | 2 | slot size, currently `128` |
| 12 | 4 | flags, currently `0` |
| 16 | 16 | database identity |
| 32 | 8 | generation |
| 40 | 8 | latest transaction included by this generation |
| 48 | 4 | checksum of that transaction's WAL frame, or `0` at transaction `0` |
| 52 | 4 | active segment count |
| 56 | 8 | active manifest offset |
| 64 | 8 | active manifest size |
| 72 | 8 | exact active file boundary |
| 80 | 4 | CRC32C of the complete active manifest |
| 84 | 4 | reserved, currently zero |
| 88 | 8 | logical live-record count |
| 96 | 28 | reserved, currently zero |
| 124 | 4 | CRC32C of bytes `[0, 124)` |

Generation zero has no manifest, no segments, no records, and an active file
boundary of 4096. Recovery validates both slots and selects the valid slot with
the greatest generation. A slot with torn magic, fields, or checksum is not a
candidate. Two different valid slots claiming the same generation are
corruption. Once a valid newest slot commits a manifest, corruption in that
manifest is reported rather than silently rolling back to an older generation.

The active file boundary is authoritative. Bytes after it are abandoned tail,
not database state. They are ignored while opening and truncated before the
next append.

### Generation manifest

Every nonzero generation ends with one complete manifest. Its 64-byte header
is followed by one 64-byte descriptor per active segment, oldest to newest.
The complete manifest checksum is stored in the generation slot.

| Offset | Size | Manifest header field |
| ---: | ---: | --- |
| 0 | 8 | magic `KITDBMF3` |
| 8 | 2 | format version, currently `3` |
| 10 | 2 | header size, currently `64` |
| 12 | 4 | flags, currently `0` |
| 16 | 16 | database identity |
| 32 | 8 | generation |
| 40 | 8 | transaction boundary |
| 48 | 4 | WAL-frame boundary checksum |
| 52 | 4 | segment count |
| 56 | 8 | logical live-record count |

Each segment descriptor is:

| Offset | Size | Segment field |
| ---: | ---: | --- |
| 0 | 8 | segment generation |
| 8 | 8 | segment transaction boundary |
| 16 | 8 | first page offset |
| 24 | 8 | total page-data size |
| 32 | 8 | page-directory offset |
| 40 | 8 | page-directory size |
| 48 | 8 | physical mutation count |
| 56 | 4 | page count |
| 60 | 4 | CRC32C of the complete page directory |

At most 32 descriptors are active. Segment generation and transaction values
are strictly increasing. An empty logical database may have a nonzero
generation with a header-only manifest and zero descriptors.

### Mutation segments

A segment stores sorted puts and tombstones. Each mutation has a 9-byte header
followed by its key and optional value:

| Offset | Size | Field |
| ---: | ---: | --- |
| 0 | 1 | kind: `1` put, `2` delete |
| 1 | 4 | key size |
| 5 | 4 | value size |

Delete mutations require a zero value size. Keys are strictly increasing in a
segment. A writer starts a new page before the next mutation when the current
page has 128 mutations or approximately 64 KiB of encoded bytes. One mutation
may make a page larger than 64 KiB.

The segment directory immediately follows its final page. Each variable-size
entry is:

| Offset | Size | Field |
| ---: | ---: | --- |
| 0 | 8 | absolute page offset |
| 8 | 8 | encoded page length |
| 16 | 4 | mutation count |
| 20 | 4 | CRC32C of the complete encoded page |
| 24 | 4 | first-key size |
| 28 | 4 | last-key size |
| 32 | variable | first-key bytes, then last-key bytes |

Entries are ordered by page offset and key range. Page ranges are contiguous,
mutation counts sum to the descriptor count, and key ranges do not overlap.
The descriptor directory checksum protects routing bounds and page checksums.

`Open` reads the fixed publication area, active manifest, and active
directories without reading mutation pages. `Get` searches active segments
newest first and verifies each selected page before decoding it. `Walk`
k-way-merges active segments so the newest mutation wins and tombstones are
suppressed. `Verify` and compaction verify every active page.

## Legacy format-v2 main snapshot

### Main header

The canonical main file starts with an 80-byte header:

| Offset | Size | Field |
| ---: | ---: | --- |
| 0 | 8 | magic `KITDBM01` |
| 8 | 2 | format version, currently `2` |
| 10 | 2 | header size, currently `80` |
| 12 | 4 | flags, currently `0` |
| 16 | 16 | random database identity |
| 32 | 8 | latest transaction included by the snapshot |
| 40 | 8 | record count |
| 48 | 4 | checksum of the included transaction's WAL frame, or `0` at transaction `0` |
| 52 | 8 | page-directory offset |
| 60 | 8 | page-directory size |
| 68 | 4 | page count |
| 72 | 4 | reserved, currently zero |
| 76 | 4 | CRC32C of bytes `[0, 76)` |

### Row pages

Pages occupy every byte from offset `80` through the directory offset without
gaps or overlap. Records remain sorted by raw key bytes. Each record has an
8-byte header containing a 4-byte key size and 4-byte value size, followed by
the key and value bytes.

A writer starts a new page before the next record when the current page has
128 records or at least approximately 64 KiB of encoded bytes. One record may
make a page larger than 64 KiB. Pages are immutable within a published main
snapshot.

### Page directory

The directory immediately follows the final row page. It contains exactly the
page count declared by the header. Each variable-size entry is:

| Offset | Size | Field |
| ---: | ---: | --- |
| 0 | 8 | absolute page offset |
| 8 | 8 | encoded page length |
| 16 | 4 | record count |
| 20 | 4 | CRC32C of the complete encoded page |
| 24 | 4 | first-key size |
| 28 | 4 | last-key size |
| 32 | variable | first-key bytes, then last-key bytes |

Entries are ordered by page offset and key range. Page byte ranges are
contiguous, record counts sum to the main-header count, and the previous last
key must be less than the next first key.

`Open` verifies the header, completion trailer, and complete directory without
reading row pages. `Get` verifies the selected page checksum before decoding
it. `Verify` and checkpoint iteration verify every page. `VerifyOnOpen` opts
into that full scan before WAL recovery.

### Completion trailer

The file ends with a 24-byte completion trailer:

| Offset | Size | Field |
| ---: | ---: | --- |
| 0 | 8 | magic `KITDBEND` |
| 8 | 4 | CRC32C of the main header followed by every directory byte |
| 12 | 4 | reserved, currently zero |
| 16 | 8 | exact complete file size |

The metadata checksum protects the per-page checksums and routing bounds. Row
payload integrity is independently protected by each directory entry. The
staging file is fully written, synced, closed, atomically published, and
followed by a directory sync before main-file publication reports success.

### Format-v1 compatibility

Version 1 used bytes `[52, 76)` as zero-filled reserved space, had no persisted
directory, and stored one checksum over the header and every record byte in the
completion trailer. The current reader accepts v1 and v2. Opening v1 performs
its original full-file scan; opening v2 reads its persisted directory. Any
checkpoint rewrites either legacy format as v3, even when there is no newer
transaction. Format-v3 writers never emit v1 or v2.

## WAL header

The WAL starts with 64 bytes:

| Offset | Size | Field |
| ---: | ---: | --- |
| 0 | 8 | magic `KITDBW02` |
| 8 | 2 | format version, currently `1` |
| 10 | 2 | header size, currently `64` |
| 12 | 4 | flags, currently `0` |
| 16 | 16 | database identity copied from the main file |
| 32 | 8 | base transaction already represented by the main snapshot |
| 40 | 4 | base transaction's WAL-frame checksum |
| 44 | 16 | reserved, currently zero |
| 60 | 4 | CRC32C of bytes `[0, 60)` |

The first frame transaction is `base_transaction + 1`. A freshly rotated WAL
contains only its header.

## Transaction frame

Each transaction starts with a 32-byte header:

| Offset | Size | Field |
| ---: | ---: | --- |
| 0 | 8 | magic `KITDBTX1` |
| 8 | 2 | frame version, currently `1` |
| 10 | 2 | header size, currently `32` |
| 12 | 4 | flags, currently `0` |
| 16 | 8 | transaction ID |
| 24 | 8 | payload size |

The payload begins with a 4-byte operation count. When header flag bit `0` is
set, the next 8 bytes are the positive UTC Unix-nanosecond commit timestamp.
Every new local commit carries this timestamp, and timestamps are strictly
increasing in transaction order even when the wall clock does not advance.
Legacy flag-zero frames have no timestamp and remain readable. Each operation
then has a 9-byte header followed by its key and value:

| Offset | Size | Field |
| ---: | ---: | --- |
| 0 | 1 | kind: `1` put, `2` delete |
| 1 | 4 | key size |
| 5 | 4 | value size |

Delete operations require a zero value size. The complete payload is bounded
before allocation or decoding.

Every complete frame ends with a 24-byte trailer:

| Offset | Size | Field |
| ---: | ---: | --- |
| 0 | 8 | magic `KITDBCMT` |
| 8 | 8 | repeated transaction ID |
| 16 | 4 | CRC32C of frame header and payload |
| 20 | 4 | complete frame size |

The trailer is the commit marker. A partial final frame after the required main
boundary is uncommitted and truncated at its start. A partial frame needed to
prove a newer main snapshot is corruption.

Group commit writes multiple complete, consecutively numbered frames before one
WAL `Sync`. No batch envelope is stored. Recovery validates and replays each
frame independently in transaction order, so changing the runtime batch size
or delay does not change durable bytes or recovery semantics.

## Retained history

`OpenOptions.RetainHistory` creates `tenant.kitdb.history`. The existence of a
valid history directory keeps retention enabled on later opens so an omitted
option cannot silently create a gap.

### History metadata

`META` is one 64-byte anchor:

| Offset | Size | Field |
| ---: | ---: | --- |
| 0 | 8 | magic `KITDBH01` |
| 8 | 2 | format version, currently `1` |
| 10 | 2 | header size, currently `64` |
| 12 | 4 | flags: bit `0` means a base commit timestamp is present |
| 16 | 16 | database identity |
| 32 | 8 | exclusive retained base transaction |
| 40 | 4 | base transaction's WAL-frame checksum |
| 44 | 8 | UTC Unix-nanosecond timestamp of the base transaction when flag bit `0` is set |
| 52 | 8 | reserved, currently zero |
| 60 | 4 | CRC32C of bytes `[0, 60)` |

The base identifies the complete current state observed when retention was
enabled or the latest safely pruned whole-segment boundary. On first enable,
KitDB recovers and checkpoints any pre-existing WAL before creating this
timestamped base; those earlier frames are represented in the base state but
are not claimed as retained history. History begins at
`base_transaction + 1`, and enabling retention on an existing database does not
reconstruct transactions before that anchor. A fully retired `.khist` prefix
whose final transaction is at or below this base is cleanup debris and is not
part of the retained range. A segment that straddles the base is corruption.

Timestamp-enabled history also owns one verified compacted base image named
`base-<20-digit-transaction>.kbase`. The file binds the database identity,
base transaction, checksum, and complete logical state. Pruning prepares and
verifies the next base image before publishing an advanced `META`; only then
may older history segments and base images be removed. A missing or mismatched
base image fails time recovery closed.

`RestoreToTime` first checkpoints to one fixed boundary, verifies the base
image and complete retained timestamp/checksum chain, and selects the last
transaction whose commit timestamp is not later than the requested UTC time.
Targets before the retained base return `ErrRecoveryTargetTooOld`. A legacy
range containing untimestamped frames returns `ErrRecoveryTimeUnavailable`
instead of guessing wall-clock order. `ForkToTime` uses the same selection but
compacts the selected state under a fresh database identity so source and fork
may be written independently.

### History segments

A segment is named with its inclusive transaction range:

```text
00000000000000000001-00000000000000000042.khist
```

Its bytes are an exact, complete copy of the WAL being rotated, including the
64-byte WAL header and every transaction frame. Therefore its header base is
`first_transaction - 1`, its first frame is `first_transaction`, and its final
frame is `last_transaction`. Adjacent segment names must be contiguous, and a
segment header's base transaction/checksum must match the preceding segment's
final transaction/checksum.

The writer copies the active WAL into a temporary file inside the history
directory, syncs and closes it, validates every frame and the expected final
boundary, atomically renames it to the canonical range name, and syncs the
directory where supported. Temporary names are never history segments. An
already published canonical segment is validated and directory-synced again,
making checkpoint retry idempotent after an uncertain directory sync.
History v1 accepts at most 65,536 canonical segments. Directory enumeration is
batched and fails with `ErrHistoryLimit` before retaining unbounded metadata.

### Retention pins

`PINS` is optional. Its fixed 48-byte header is followed by up to 1,024
strictly name-sorted entries and one CRC32C trailer over every preceding byte:

| Offset | Size | Header field |
| ---: | ---: | --- |
| 0 | 8 | magic `KITDBP01` |
| 8 | 2 | format version, currently `1` |
| 10 | 2 | header size, currently `48` |
| 12 | 4 | flags, currently `0` |
| 16 | 16 | database identity |
| 32 | 4 | entry count |
| 36 | 4 | total entry payload bytes |
| 40 | 8 | reserved, currently zero |

Each entry has a 16-byte header followed by its UTF-8 name:

| Offset | Size | Entry field |
| ---: | ---: | --- |
| 0 | 2 | name byte length, `1..128` |
| 2 | 2 | flags, currently `0` |
| 4 | 4 | transaction boundary checksum |
| 8 | 8 | last transaction already applied by the named consumer |
| 16 | variable | pin name bytes |

The file ends with a four-byte CRC32C. A pin at transaction `T` permits pruning
through `T` while requiring `T+1` and later to remain available. `SetHistoryPin`
verifies the database identity, transaction, and checksum against the retained
chain before atomically replacing and syncing `PINS`.

### Safe pruning

`PruneHistory` is explicit and works only at complete `.khist` boundaries. It
refuses a target newer than the retained tail or beyond the oldest pin. Before
deleting bytes it verifies every segment being retired and proves that the
first remaining segment links to the new base checksum. Publication order is:

1. Write and sync replacement `META` in the history directory.
2. Atomically replace `META` and sync its directory entry where supported.
3. Remove fully retired `.khist` files and sync the directory again.

A crash after step 2 may leave old canonical files, but `META` makes them an
ignored retired prefix. Deleting segments before publishing the new base is
forbidden because a crash would create a permanent active-range gap. Calling
`PruneHistory` again at the already-published base removes that retired prefix
without replacing or advancing `META` a second time.

## Verified backup anchors

An anchor is built from one bounded logical read snapshot. The snapshot binds
the main generation, a copied synced-WAL overlay, transaction, and transaction
boundary checksum. Anchor construction merge-streams that state into a new
compacted format-v3 file with generation `1` and at most one mutation segment.
A database with no committed transaction uses the canonical empty generation
`0` with no manifest or segment. Anchor creation does not copy abandoned
physical generations or require a checkpoint.

The anchor carries the source database identity, snapshot transaction, and
boundary checksum in its ordinary generation slot and manifest. These fields
allow later transaction chunks to prove that they follow the anchor. No WAL,
lock, history directory, timestamp, remote location, or SHA-256 field is stored
inside the anchor.

Publication follows this order:

1. Write the complete anchor to a temporary file beside the destination.
2. Sync and close the temporary file.
3. Reopen it, require its physical size to equal the active slot's exact file
   boundary, and checksum every active page and logical row.
4. Stream all exact bytes through SHA-256.
5. Require that the destination does not exist, publish it with an exclusive
   same-filesystem link, and sync the containing directory where supported.

The staging name is never an anchor. Cancellation or validation failure before
publication removes staging and leaves no destination. A directory-sync failure
after the exclusive link reports `ErrDurabilityUncertain`; the caller can run
`VerifyBackupAnchor` to inspect the destination.

Opening an anchor as a database preserves its identity and transaction and
creates a new WAL linked to that boundary. Anchor validity alone does not claim
that a later history chain is complete.

## Restore materialization

`RestoreToTransaction` introduces no new durable format. It materializes a
caller-selected format-v3 anchor plus canonical history v1 files into a new
format-v3 main file. The target transaction must be at or after the anchor
transaction. The destination is never a recovery candidate while staging.

Before replay, restore validates the anchor completely and requires the history
`META` identity to match. Canonical history filenames must form a contiguous
range from `META.base_transaction` through the requested target. If the first
used history file starts at `anchor_transaction + 1`, its WAL-header base
checksum must equal the anchor boundary checksum. If that file also contains
older transactions, its frame at the anchor transaction must carry the exact
anchor checksum before the next frame is accepted. Every used history file is
read and validated through its declared final frame, including when the target
occurs earlier in that file.

Committed operations after the anchor are coalesced by key in a bounded
in-memory overlay. At 32 MiB or 262,144 distinct keys, and once more at the
target, restore appends a sorted immutable generation carrying the last applied
transaction and its source frame checksum. The ordinary 32-segment bound still
applies; crossing it performs the same streaming compaction used by a live
checkpoint. No replay WAL or lock sidecar is created.

After reaching the target, restore closes the staging reader, requires the
physical file size to equal the active generation boundary, verifies every
active page and logical row, and computes SHA-256. It then publishes the
destination with an exclusive same-filesystem link and syncs the containing
directory. Existing destinations are never replaced. Failures before
publication remove staging. A directory-sync failure after the link reports
`ErrDurabilityUncertain`; the linked destination may be inspected with
`VerifyBackupAnchor`.

## One-way replica catch-up

Replica bootstrap and catch-up introduce no new database-durability file.
Bootstrap first
pins a proven source cursor, publishes a verified format-v3 anchor, seals
history through that anchor, and then moves the pin to the anchor boundary. A
failure after anchor publication keeps the older pin rather than risking a
missing successor range.

The target is opened in replica mode and must carry the same 16-byte database
identity. Its current main/WAL transaction and checksum must still be provable
by the source history before streaming begins. Replica protocol v1 is the
transport-neutral API envelope below; replica wire v1 is one exact encoding of
that envelope, not another target cursor or recovery log:

| Field | Meaning |
| --- | --- |
| `version` | protocol envelope version, currently `1` |
| `database_id` | canonical source database identity |
| `from` | exact exclusive transaction/checksum cursor |
| `to` | exact inclusive cursor reached by this batch |
| `source_boundary` | fixed checkpoint cursor for the complete catch-up |
| `transactions` | contiguous committed operation sets with frame checksums |
| `bytes` | exact sum of their encoded WAL-frame sizes |

`ReadReplicaBatch` fixes a source boundary when one is not supplied, validates
the complete retained chain through that boundary, and returns at most 1,024
transactions by default. A batch can never exceed 4,096 transactions or
67,108,920 exact frame bytes; the byte ceiling is large enough for one maximum
legal transaction. Continuation requests retain the original source boundary,
so new commits cannot turn one catch-up into an unbounded chase.

`ApplyReplicaBatch` validates cursor order, hard limits, operation shapes,
declared byte size, and every re-encoded frame CRC32C before its first target
mutation. It then applies only the missing suffix through the ordinary commit
writer, WAL append, `Sync`, catalog validation, overlay publication,
checkpoint, and recovery paths. A target cursor equal to the batch end is a
no-op; a cursor at a checksum-matching transaction inside the batch resumes its
remaining suffix. Any other target position is divergence.

A source pin advances only after the target returns a small acknowledgement for
an exact durable batch end. It contains only the protocol version and that
database/transaction/checksum cursor, never transaction bodies.
`AcknowledgeReplicaBatch` validates the message and re-proves its cursor against
source history before moving the pin; local `CatchUpReplica`
acknowledges only its final batch after reaching the complete fixed boundary.
Cancellation or failure may leave a durable target prefix while the older
source pin remains conservative; the same batch can resume from the target's
embedded cursor. A missing retained range fails with `ErrHistoryGap`, and an
identity, ordering, limit, envelope, or checksum mismatch fails closed. Replica
mode is a handle policy, not a persisted role. Protocol v1 deliberately defines
no authentication, scheduling, synchronous replication, or peer-discovery
policy.

### Replica batch wire v1

`WriteReplicaBatchMessage` encodes exactly one protocol-v1 batch. All integers
are little-endian. A decoder checks the declared transaction and body limits
before allocating body storage and requires EOF immediately after the digest.
The 128-byte header is:

| Offset | Bytes | Meaning |
| ---: | ---: | --- |
| 0 | 8 | `KITDRPB1` |
| 8 | 2 | wire version `1` |
| 10 | 2 | header size `128` |
| 12 | 4 | flags, currently zero |
| 16 | 16 | raw database identity |
| 32 | 16 | `from`: transaction `u64`, CRC32C `u32`, reserved `u32` |
| 48 | 16 | `to`: transaction `u64`, CRC32C `u32`, reserved `u32` |
| 64 | 16 | fixed source boundary in the same cursor layout |
| 80 | 4 | transaction count |
| 84 | 2 | replica protocol version `1` |
| 86 | 2 | transaction-frame format version `1` |
| 88 | 8 | exact body byte count |
| 96 | 8 | exact total message byte count |
| 104 | 20 | reserved, all zero |
| 124 | 4 | CRC32C of bytes `[0,124)` |

The body is the concatenation of canonical WAL transaction frames, without a
WAL header. Reusing frame v1 means each event's transaction checksum is exactly
the checksum source history retained and the target WAL must reproduce; there
is no second operation codec.

The 64-byte trailer is:

| Offset | Bytes | Meaning |
| ---: | ---: | --- |
| 0 | 8 | `KITDRPE1` |
| 8 | 2 | wire version `1` |
| 10 | 2 | trailer size `64` |
| 12 | 4 | flags, currently zero |
| 16 | 8 | repeated body byte count |
| 24 | 8 | repeated total message byte count |
| 32 | 32 | SHA-256 digest |

The digest covers the complete header, body, and first 32 trailer bytes. The
maximum encoded batch is 67,109,112 bytes: 128 header bytes, at most 67,108,920
frame bytes, and 64 trailer bytes. SHA-256 proves byte identity but is unkeyed;
it does not authenticate an untrusted sender.

### Replica acknowledgement wire v1

An acknowledgement is exactly 96 bytes:

| Offset | Bytes | Meaning |
| ---: | ---: | --- |
| 0 | 8 | `KITDRPA1` |
| 8 | 2 | wire version `1` |
| 10 | 2 | message size `96` |
| 12 | 4 | flags, currently zero |
| 16 | 16 | raw database identity |
| 32 | 8 | acknowledged transaction |
| 40 | 4 | acknowledged transaction checksum |
| 44 | 2 | replica protocol version `1` |
| 46 | 18 | reserved, all zero |
| 64 | 32 | SHA-256 of bytes `[0,64)` |

### Filesystem mailbox v1

`OpenReplicaFileTransport(root, limits)` owns this explicit layout:

```text
root/
  batches/<database>-<from-tx>-<from-crc>-<to-tx>-<to-crc>.krpb
  acks/<database>-<to-tx>-<to-crc>.krpa
```

Transactions use fixed-width lowercase hexadecimal names (`16` digits for a
transaction and `8` for a checksum), so lexical order is deterministic. Names
are routing hints only; readers decode and require the message cursors to match
the filename.

A publisher creates a mode-`0600` temporary file in the destination directory,
writes and syncs the complete message, closes it, creates the final name with an
exclusive hard link, removes the temporary name, and syncs the directory. The
final name is therefore never a partial message and is never overwritten.
Re-publishing identical bytes is idempotent; a same-name/different-message
collision fails with `ErrReplicaTransportConflict`.
This v1 publication primitive requires local hard-link support. A filesystem
without it is rejected rather than falling back to a visible partial copy.

The target decodes a batch completely, applies it through `ApplyReplicaBatch`,
and publishes an ACK only after its WAL reaches the batch end. If it crashes
after WAL commit but before ACK publication, replay sees the target cursor at
the batch end, performs no mutation, and regenerates the ACK. The source first
publishes or confirms its durable history pin, then removes the batch, syncs,
removes the ACK, and syncs. Replaying an old ACK never moves a newer pin
backward.

Temporary names are never apply candidates. `CleanupStaging` removes them only
when the host declares the mailbox quiescent. No automatic watcher or goroutine
runs. Defaults bound a mailbox to 64 batches and 512 MiB; hard ceilings are
4,096 batches and 64 GiB. Counts, bytes, directory entries, individual wire
messages, and ACK sizes are checked before use. These files may be copied,
retried, or deleted after acknowledgement; target WAL and source `PINS` remain
the durability truth.
The mailbox directory itself is therefore a host trust boundary until a future
authenticated transport signs or authenticates messages.

## Checkpoint ordering

An incremental format-v3 checkpoint uses this publication order:

1. Truncate bytes beyond the active file boundary and append one sorted
   mutation segment plus its checksummed directory.
2. Append a complete manifest containing every active segment descriptor.
3. Sync the main file so segment and manifest bytes are durable.
4. Write the inactive generation slot with generation `active + 1`.
5. Sync the main file again; the new slot is now the publication point.
6. Install a reader for the new generation and clear the in-memory overlay.
7. If retained history is enabled, copy the complete previous WAL to a staging
   segment, sync, validate, atomically publish it, and sync the history
   directory.
8. Write and sync a WAL-header staging file linked to that main boundary.
9. Close the previous WAL, atomically replace it, sync its directory, and reopen
   it before admitting another commit.

Once step 5 has published the main boundary, a retained-history seal failure or
a subsequent WAL-rotation failure makes the live handle unavailable. Reopen
uses the still-authoritative old WAL to validate or republish the missing
history range before accepting writes. It may reuse an already canonical,
fully validated segment after an uncertain directory sync.

At 32 active segments, or while migrating v1/v2, checkpoint merge-streams the
logical rows into a fresh format-v3 file with at most one base segment. It
syncs and closes that staging file, atomically replaces `tenant.kitdb`, syncs
the containing directory, and then follows the same WAL rotation order.

A crash before the slot write leaves the older slot authoritative. During the
slot write or before its successful sync, recovery may select either the older
slot or a completely persisted newer slot; a torn newer slot is rejected by its
CRC, and either valid generation is recoverable from the still-authoritative
WAL. A successful slot sync guarantees publication of the newer generation.

A crash after main publication but before WAL replacement leaves a newer main
generation with an older WAL. Recovery validates old WAL frames through the
transaction and checksum recorded by the main slot. If the old WAL ends exactly
there, recovery first publishes the missing retained-history segment when
retention covers that range, then finishes rotation. If it contains later
complete frames, they are replayed and remain authoritative until the next
checkpoint.

Publishing the WAL before the main file is forbidden because it could discard
the only durable copy of committed transactions.

## Core schema catalog keyspace

The schema catalog uses ordinary transactional records in a kernel-reserved
raw-key prefix. It introduces no new main-file page, WAL frame, or sidecar
format:

```text
byte 0x01 | 16-byte stable struct ID  ->  UTF-8 versioned definition JSON
```

Every catalog key is exactly 17 bytes. The hexadecimal `id` inside
the definition must encode the same 16 bytes as the key. The top-level JSON
object must contain `version` (positive integer), `id`, `name`, `hash`, and a
nonempty `fields` array. Duplicate top-level JSON names, invalid UTF-8, NUL in
the struct name, duplicate exact struct names, and mismatched IDs are rejected.
The kernel intentionally does not interpret field descriptors or recompute a
frontend-specific schema hash.

One catalog holds at most 16,384 structs and 128 MiB of encoded definitions.
One definition is at most 16 MiB, a name or hash is at most 1 KiB, and one
definition declares at most 65,535 fields. Catalog mutations are normal WAL
operations and share the transaction, checksum, sync, recovery, checkpoint,
history, and replication ordering of row mutations.

The Kitwork adapter may place several catalog definitions, their bounded row
and derived-index mutations, and migration audit records in one transaction.
It validates the complete target dependency graph and a final in-memory
mutation overlay before opening that transaction, so a missing referenced
tuple cannot publish only part of a schema batch. Version-2 migration audits
from the same batch carry one deterministic `batch` ID and identical
`appliedAt` timestamp. These audit fields are adapter metadata; they add no WAL
or main-file format.

Default `Open` validates storage metadata but loads catalog semantics lazily by
seeking to prefix `0x01` and stopping when that keyspace ends. `VerifyOnOpen`
loads and validates the catalog eagerly. A malformed durable definition is
reported as both `ErrCorrupt` and `ErrInvalidCatalog`.

## Kitwork relational planner statistics

The Kitwork adapter stores one rebuildable statistics record per struct in a
separate ordinary keyspace:

```text
byte 0x04 | 16-byte struct ID | 16-byte stable statistics ID -> KSTA envelope
byte 0x04 | 16-byte struct ID | 16-byte stable dirty ID      -> fixed dirty marker
```

The final IDs are deterministically derived from `"statistics"` or
`"statistics-dirty"` and the struct ID. The keys are therefore fixed across
repeated analysis of one struct and do not collide with catalog (`0x01`),
relational metadata (`0x03`), row, unique, or secondary-index records. The
kernel treats both as opaque application data.

The value uses this envelope:

| Offset | Size | Field |
| ---: | ---: | --- |
| 0 | 4 | magic `KSTA` |
| 4 | 1 | version, currently `1` |
| 5 | 3 | reserved zero |
| 8 | 4 | JSON payload size, little-endian |
| 12 | N | bounded statistics JSON |
| 12+N | 4 | little-endian CRC32C of every preceding byte |

The complete envelope is at most 1 MiB. The payload binds the stable struct ID,
Schema IR hash, analyzed transaction, exact row count, and at most 64 active
index records. Each index binds its logical signature/name/fields, exact entry
count, and up to 8 estimated distinct leading-prefix counts. Those estimates
are serialized integers; the fixed-memory sketches used to produce them are
not persisted. After statistics exist, a row mutation of that struct writes the
fixed value `KITDB-STATISTICS-DIRTY\x01` at its dirty key in the same record
transaction as rows and indexes. Repeated mutations in one transaction observe
that marker and do not append duplicates. An unrelated struct or metadata-only
commit does not dirty the record. Re-analysis replaces `KSTA` and removes the
dirty marker in one optimistic WAL transaction; a concurrent commit conflicts.
A schema mismatch or dirty marker makes statistics stale for planning, but does
not make the database corrupt. `DROP TABLE` removes the complete `0x04` struct
prefix with its other physical keyspaces.

## Kitwork resumable import progress

The PostgreSQL CSV/JSONL importer stores one durable source watermark per
logical import ID in an adapter-owned ordinary-record keyspace:

```text
byte 0x05 | SHA-256("kitdb-import-state\0" || UTF-8 import ID)[32] -> KIMP envelope
```

The complete import ID remains in the payload, so an impossible digest mismatch
fails closed. An ID and source label are bounded to 256 and 1,024 bytes,
respectively. One state binds the source format, stable struct and ordered field
identities, display names, seed/checksum, last chunk, cumulative source rows,
exact source byte offset, mutually exclusive completion/cancellation bits, and
predicted commit transaction.

The value envelope is:

| Offset | Size | Field |
| ---: | ---: | --- |
| 0 | 4 | magic `KIMP` |
| 4 | 1 | version, currently `1` |
| 5 | 3 | reserved zero |
| 8 | 4 | JSON payload size, little-endian |
| 12 | N | bounded state payload, at most 64 KiB |
| 12+N | 4 | CRC32C of every preceding byte |

The kernel treats KIMP as opaque application data. A resumable COPY validates
the previous chunk/checksum/offset from its fixed snapshot, writes rows and the
next KIMP value through one record transaction, and publishes both or neither.
The transaction watermark is `base + 1`; optimistic publication guarantees
that any intervening commit conflicts rather than making that prediction
incorrect. KIMP does not weaken row constraints and is not a backup of the
source file. Cancellation updates the same envelope through an ordinary WAL
transaction. Forgetting a completed or cancelled import deletes only this key;
already committed relational rows remain authoritative.

## Kitwork relational physical metadata

The kernel treats the following records as opaque application data. The
Kitwork relational adapter reserves fixed 33-byte keys beneath prefix `0x03`:

```text
byte 0x03 | 16-byte struct ID | 16-byte physical metadata ID
```

The metadata ID is deterministic and distinguishes the secondary-index codec
marker, codec-build state, schema-index build state, segmented row-migration
state, row-generation metadata, and index-generation metadata. The active v2 codec marker value is
`KITDB-SECONDARY-INDEX\x02`.

Ordered secondary-index keys use these disjoint record-layer layouts:

```text
0x30 | struct-id[16] | logical-index-id[16] | 0x00 0x02 |
    ordered components...                                      generation 0

0x30 | struct-id[16] | logical-index-id[16] | 0x00 0x03 |
    generation[8] | ordered components...                      generation > 0
```

For legacy Schema IR entries without an explicit index ID, the logical index ID
is derived from struct ID, index name, and ordered columns. Current Schema IR
may persist that 16-byte ID on every member of an ordinary index. Once present,
it is immutable across an index or table rename; recreating a released logical
name allocates a different ID and therefore cannot alias the earlier physical
keyspace. The separate 16-byte index signature covers logical name, columns,
and partial predicate and may be remapped to the same physical generation by an
atomic metadata-only rename. Generation is big-endian and nonzero in v3.
Existing v2 keys are the implicit generation zero and require no eager rewrite.

A resumable build value uses the following `KIBS` envelope:

| Offset | Size | Field |
| ---: | ---: | --- |
| 0 | 4 | magic `KIBS` |
| 4 | 1 | envelope version, currently `2` |
| 5 | 1 | mode: codec `1`, schema index transition `2` |
| 6 | 1 | phase: rows `1`, cleanup `2` |
| 7 | 1 | reserved zero |
| 8 | 8 | transaction visible when the build started, big-endian |
| 16 | 8 | number of source rows processed, big-endian |
| 24 | 32 | source Schema IR hash bytes |
| 56 | 32 | target Schema IR hash bytes |

The fixed header is followed by a canonical variable body:

```text
uvarint progress-key-size | progress-key
uvarint source-definition-size | source-definition
uvarint target-definition-size | target-definition
uvarint index-count |
    index-count * (16-byte index signature | uint64 generation)
uvarint retired-prefix-count |
    repeated (uvarint prefix-size | physical prefix)
uvarint cleanup-prefix-index
uint32 big-endian CRC32C of every preceding envelope byte
```

Source and target definitions are the exact catalog JSON documents on either
side of the transition; product rows remain `KROW` binary values. Schema shadow
generations are nonzero. Codec-mode generations remain zero because that mode
builds the implicit v2 layout. Version `1` remains readable and resumable with
its original target-only body and 16-byte signature records; it is never
silently re-encoded as v2 state while in progress.

Each chunk commits derived index puts and its updated `KIBS` cursor in one WAL
transaction. The final schema-index chunk publishes the target catalog,
migration audit, generation metadata, and optional cleanup-phase `KIBS` in that
same transaction. A codec build instead writes the active marker and a cleanup
state. Cleanup deletes one bounded physical prefix at a time and transactionally
removes that prefix from generation metadata. Shadow-key presence is never a
publication signal.

A shadow row migration uses the `KRMS` envelope:

| Offset | Size | Field |
| ---: | ---: | --- |
| 0 | 4 | magic `KRMS` |
| 4 | 1 | version, currently `1` |
| 5 | 3 | reserved zero |
| 8 | 4 | JSON payload size, big-endian |
| 12 | N | bounded migration-state JSON |
| 12+N | 4 | big-endian CRC32C of header and payload |

The payload carries the start transaction, one fixed UTC migration timestamp,
processed, verified, and cleanup row counts, the admission row count, the last committed
phase cursor (`rows`: source key; `verify`: target key; `cancel`: target key;
`cleanup`: retired source key), phase (`rows`, `verify`, `cancel`, or `cleanup`), source and target row generations, retired
source prefix, source and target Schema IR hashes, and the exact source and
target catalog documents. A primary-key migration additionally carries
`primaryKeyChange`, the source index-layout epoch, canonical target-index
signatures, retired source-index prefixes, and a durable cleanup-prefix ordinal.
These are optional version-1 JSON members, so older ordinary row-migration
states remain byte-compatible. For a shadow generation, `totalRows` is the bounded
admission lower bound observed before state publication; online insert/delete
activity means it is not an immutable cardinality and processed rows may exceed
it. Target generation zero means a legacy in-place state; its `totalRows` stays
exact and those values remain readable with their original writer-gated resume
semantics.

Logical row identities remain generation-independent:

```text
0x10 | struct-id[16] | scalar primary component...                  generation 0
0x11 | struct-id[16] | generation[8] | scalar primary component... generation > 0
```

There is exactly one self-delimiting scalar component per ordered primary
field. Existing one-field keys are therefore byte-for-byte unchanged. A
composite key is unambiguous without separators, and changing membership or
order is a physical row-identity migration rather than metadata-only DDL.

Indexes and unique records always store the logical `0x10` row key. Record
execution translates it through the active row generation before loading the
physical value. Existing databases therefore use implicit generation zero
without an eager rewrite.

A bounded admission scan stops as soon as it proves that the 10,000-row atomic
ceiling was crossed. It then publishes the durable `rows` state before any
full-table validation. The source schema remains readable and every ordinary
create/update/delete dual-writes source and target generations. The accepting
ALTER or a node-owned resume dispatch advances one chunk: transform, target
validation, and at most 2,048 target row puts/8 MiB share one WAL commit with
the next cursor. An incompatible row leaves the cursor at the preceding durable
chunk; a compatible source-schema repair or delete may then resume it. Shadow-key
presence is never publication authority. For an ordinary field migration, the
last row chunk atomically publishes target catalog, audit, `KRGM`, and a
`cleanup` state. A primary-key build first enters the verification phase below.
Bounded cleanup
deletes the retired source prefix and finally removes it from `KRGM` together
with `KRMS`. The kernel sees all of these records as opaque application WAL
data.

Background ownership adds no bytes to this format. Each node dispatch reloads
the exact source/target documents, phase, and cursor from `KRMS`; process-local
queues and drivers are hints only. A pending dispatch is requeued after its
lease and file-scoped relational gate are released. Driver failure or invalid
row data leaves the last committed envelope unchanged.

When `primaryKeyChange` is true, the row cursor still walks the source physical
generation, but each target row key is derived from the target Schema IR rather
than copied from the source logical key. Its write-fenced build may inspect at
most 8,192 rows per dispatch, while the shared 8-MiB/16,384-mutation ceilings
still stop the transaction earlier. A same-chunk set rejects local
collisions before commit. Cross-chunk shadow overwrites are not publication
authority: build completion enters `verify`, which scans visible target row keys
in chunks of at most 65,536 keys and persists `verifiedRows` with its cursor.
Cutover is
allowed only when `verifiedRows == rows`; a smaller target proves at least one
duplicate tuple and fails closed. This keeps a successful all-unique build
linear rather than issuing one increasingly expensive miss lookup per source
row. Every target ordered index is built in its own nonzero v3 generation; row
and index puts share the build-cursor WAL transaction. The cutover transaction
publishes `KRGM`, `KIGM`, catalog, audit, and cleanup state together. During
`rows` and `verify`, table writes are fenced instead of dual-written; reads
continue against the source generation. During `cleanup`, the target catalog is
authoritative and writes use the new primary key.

Before target publication, `ALTER TABLE name CANCEL MIGRATION` atomically moves
`rows` or `verify` to `cancel` and clears the old build/verification cursor. That state makes source-only
CRUD authoritative before any target deletion begins. Each cancellation pass
for a primary rekey deletes at most 8,192 shadow keys/8 MiB and commits its cleanup cursor in
the same WAL transaction; a primary-key cancellation advances across the target
row prefix and every target index prefix before the last pass deletes `KRMS`.
Restart resumes from that prefix ordinal and cursor. Cancellation is deliberately
unavailable for legacy in-place state or `cleanup`, because source rows may
already have been rewritten or the target catalog/generation is already
authoritative. Published primary-key cleanup similarly advances across the
retired source row prefix and every retired source index prefix.

The published generation map uses this `KIGM` envelope:

| Offset | Size | Field |
| ---: | ---: | --- |
| 0 | 4 | magic `KIGM` |
| 4 | 1 | version, currently `1` |
| 5 | 3 | reserved zero |
| 8 | 8 | monotonic index-layout epoch, big-endian |

Its canonical variable body is:

```text
uvarint active-count |
    active-count * (16-byte index signature | uint64 nonzero generation)
uvarint retired-prefix-count |
    repeated (uvarint prefix-size | physical prefix), strictly ordered
uint32 big-endian CRC32C of every preceding envelope byte
```

Generation-zero indexes are omitted from the active map. Catalog, active map,
epoch increment, audit, and cleanup intent publish atomically. Read execution
checks the epoch and opens its immutable kernel snapshot under the same
record-layer writer gate, binding one operation to either the pre-cutover or
post-cutover layout.

The row publication map uses the `KRGM` envelope:

| Offset | Size | Field |
| ---: | ---: | --- |
| 0 | 4 | magic `KRGM` |
| 4 | 1 | version, currently `1` |
| 5 | 3 | reserved zero |
| 8 | 8 | monotonic row-layout epoch, big-endian |
| 16 | 8 | active row generation, big-endian; zero is the legacy layout |

Its canonical variable body is:

```text
uvarint retired-prefix-count |
    repeated (uvarint prefix-size | physical prefix), strictly ordered
uint32 big-endian CRC32C of every preceding envelope byte
```

The final backfill transaction changes active generation and catalog under one
epoch increment. Read execution validates that epoch and opens its immutable
kernel snapshot while holding the same record-layer gate, so a request binds to
either the complete source layout or the complete target layout. Cleanup does
not change the active generation or epoch.

## Relational row payload v2

The format-v3 kernel treats keys and values as opaque bytes. The Kitwork
`struct()` adapter stores relational rows inside those values using a separate,
versioned envelope. Changing this envelope does not change the main-file or WAL
format described above.

Kitwork Schema IR v2 gives every field a nonzero `uint32` tag. Its complete
document is the current catalog value payload. Tags survive field rename and
declaration reorder, are never inferred
from a field name while reading a row, and are allocated monotonically through
`nextFieldTag`. The small catalog and migration audit records remain JSON
metadata; product rows do not.

The catalog key, not the current struct name, is the struct's storage identity.
An explicit table rename replaces the definition at the same stable struct ID,
updates every catalog-owned incoming reference in the same WAL transaction, and
does not rewrite row or derived-index records. The old logical name disappears
from the catalog name map only after commit and may later be reused by a new,
distinct struct ID. Ordinary indexes may carry an optional immutable 16-byte
`id` in each `StructIndexMember`; all members of one composite index must agree.
Missing IDs retain the legacy deterministic derivation for backward
compatibility.

Schema IR v2 named constraints are also catalog metadata. A named unique stores
one or more ordered local `uint32` field tags. A named foreign key stores one or
more ordered local tags, target struct ID, ordered target field IDs, and action
policy. Consequently, a deliberate field rename can preserve both sides of the
relationship without rewriting row payloads or changing the opaque kernel
key/value format. One referenced field may resolve to a primary, generated
unique, or named unique identity; multiple referenced field IDs must resolve to
the exact ordered composite primary key or one exact ordered named unique
constraint before the adapter accepts the catalog.

Schema IR v2 fields may carry an optional positive `primaryOrder`. A legacy
single primary field omits it; an ordered composite primary requires every
member to carry one contiguous ordinal from `1` through `N`. `primary` remains
true on each member, while `unique` remains false unless that individual field
has a separate unique constraint. The catalog hash covers these ordinals.

Schema IR v2 CHECK constraints are catalog metadata rather than stored SQL.
Each constraint has a stable ID/name and one bounded expression tree. Field
nodes store immutable `uint32` tags; literal, unary, binary, deterministic
function, and CASE nodes store canonical operators and children. The catalog
hash covers this tree. Hydration verifies all tags and operators and compiles
the expression before publication. Runtime row writes evaluate the prepared
tree with SQL three-valued logic, where only false rejects a row. SQL text and
the compact column `.check()` builder are frontends over this same IR.

An explicit SQL column rename preserves the field ID/tag and appends the old
name to catalog aliases; no row-format version is introduced. A bounded field
drop rewrites each affected row without the removed known tag while retaining
all genuinely unknown tags byte-for-byte. `nextFieldTag` never moves backward,
so a later field cannot inherit the dropped field's physical identity. A type
change rewrites the value under the same tag using a deterministic cast and
rebuilds affected derived keys. Rows, indexes, schema catalog, index-layout
metadata, and migration audit publish in one ordinary WAL transaction or not
at all. These are Schema IR transitions above the opaque kernel format, not new
main-file or WAL record types.

Every new row value starts with this 16-byte header:

| Offset | Size | Field |
| ---: | ---: | --- |
| 0 | 4 | magic `KROW` |
| 4 | 1 | row version, currently `2` |
| 5 | 1 | flags, currently `0` |
| 6 | 2 | header size, currently `16` |
| 8 | 4 | field count |
| 12 | 4 | CRC32C of the complete field body |

The body contains exactly `field count` entries ordered by strictly increasing
tag:

```text
uvarint field-tag | byte value-type | uvarint payload-size | payload
```

Unsigned integers must use their shortest canonical varint representation.
The current value types are null, boolean, signed-varint integer, float64
number, UTF-8 string, bytes, time, duration, array, and map. Fixed numeric
payloads are little-endian.
Arrays contain a count followed by length-delimited typed values. Maps contain
a count followed by strictly ordered UTF-8 keys and length-delimited typed
values. The existing 8 MiB row, 128-level nesting, and 100,000-node bounds are
enforced while encoding and decoding.

A reader uses the catalog to map tags to current field names. An unknown tag is
kept as its original type byte and payload and is emitted unchanged by a later
update; this prevents an older process from erasing a field written by a newer
one. A known tag with an unsupported type fails closed because its logical
value cannot be interpreted safely.

Rows written before this envelope are detected as legacy JSON. They remain
readable, including through persisted rename aliases, and are converted to the
binary envelope the next time that row is written or a rewriting migration
touches it. Upgrading a Schema IR v1 catalog assigns deterministic initial tags
and commits only the new catalog plus an audit record; it does not pretend to
have rewritten every legacy row.

## Exact Integer Key Components

Standalone catalog kinds `smallint`, `int32` and `bigint` use scalar tag `6`
with eight big-endian bytes of `uint64(value) XOR 0x8000000000000000`. Admission
enforces signed 16/32/64-bit bounds before encoding. The shared representation
preserves signed order and distinguishes adjacent int64 values above 2^53. A
historical row/unique component is length-delimited, so its bytes are
`0x09 | 0x06 | eight bytes`. An ordered secondary-index component is
`0x06 | eight bytes`. Components have fixed width, including inside composite
keys; NULL retains tag 0.

Encoding is selected by the durable field kind, not a value magnitude or Go
runtime type. Legacy `integer` and numeric tag 2 retain their existing bytes and
53-bit admission contract. Newly declared SQL SMALLINT, INTEGER and BIGINT use
`smallint`, `int32` and `bigint`; existing schemas are not automatically
reinterpreted. Older logical readers without a kind must fail closed. The
Kitwork Number-based adapter preserves smallint/int32 and rejects bigint until
it can preserve the complete int64 domain.

The KROW value is still the existing signed-varint integer value type. Kernel
main-file v3, WAL framing and KROW v2 are unchanged. Migrating a legacy integer
field to an exact-width kind requires rebuilding all affected identity/index
keys and handling dependencies; no implicit reinterpretation is permitted.

## SQL Function Catalog Records

Function catalog v1 uses the reserved key `0x01 | 'F' | 16-byte function ID`
(18 bytes), distinct from the existing 17-byte struct catalog keys. Values are
bounded UTF-8 JSON metadata containing `version`, `id`, `name`, `hash`, named
parameter kinds, a return kind and the pure expression AST. This metadata is
not row storage and introduces no additional physical file.

The kernel validates identity/name uniqueness and shared catalog byte limits;
the standalone relational owner validates the definition hash, supported types
and pure-expression semantics. Function records participate in the same WAL
commit, catalog revision, checkpoint, backup and replay paths as struct
records, but are exposed separately in `CatalogSnapshot.Functions`.

Limits are 1024 functions and 64 KiB per definition, charged to the existing
128 MiB catalog budget. No function records means the catalog revision digest
is unchanged from the previous algorithm. Older builds that only understand
struct catalog keys fail catalog hydration on function records; backward
reading of existing files is supported, downgrade of a function-bearing file
is not. Main-file v3, WAL and KROW envelopes are unchanged.

## Standalone Sequences

Sequence definitions use `0x01 | 'S' | 16-byte random sequence ID` (18 bytes).
Canonical JSON metadata contains version 1, ID, name, start, increment, minimum,
maximum and cycle, optional `dataType` (`smallint`, `integer`, or `bigint`),
optional cache (1..4096), plus optional ownerStruct (stable struct ID) and ownerTag
(stable field tag). Both owner fields are present together or omitted together;
unowned definitions retain their previous canonical encoding. Missing dataType
means BIGINT and missing cache means CACHE 1; both remain omitted when an older
definition is canonicalized so its bytes and logical digest do not move.
Definitions are
immutable, limited to 4 KiB each and 1024
per database, charged against the shared catalog budget. Sequence names cannot
collide with struct names. CatalogSnapshot.Sequences is ordered by name.
The revision digest adds a domain-separated sequence section; databases with
no sequences keep the previous digest algorithm unchanged.

The current counter uses `0x02 | 16-byte sequence ID` (17 bytes) and a 10-byte
binary value: version byte 1, is_called byte (0 or 1), and an eight-byte
big-endian two's-complement int64. Counter-only reservations do not change the
catalog revision. Create/delete publishes/removes both keys in one WAL frame.
These are reserved kernel keys; raw key/value callers must not mutate them.

Lease publication uses the existing WAL checksum/Sync and checkpoint format,
not a new durability subsystem. The persisted counter is the lease high-watermark.
Each lease has an ordinary transaction ID, timestamp, history frame and commit
event; individual values served from that already-published lease do not. Backup,
replica and restore include the high-watermark at exactly that boundary. The allocation/session contract is documented
in relational/README.md. Main-file v3, WAL frame and KROW formats are unchanged.
Older builds without sequence catalog support reject sequence-bearing catalogs;
backward reading of existing files is supported, downgrade is not.

### Sequence-Bound Schema IR v3

A sequence-backed field promotes its struct definition to schema IR version 3,
independent of the main-file version. Its optional `sequence` member records
`id`, `name` and `mode` (`default`, `serial`, `always`, or `by_default`). These
fields participate in the schema hash. The binding requires SMALLINT, INTEGER
or BIGINT and an exactly matching sequence data type; it excludes literal/
clock/on-update defaults, and owned modes require NOT NULL. Ordinary newly
declared schemas remain version 2, and omitting the member preserves their
existing JSON encoding/hash.

The catalog validates dependencies after all operations of a transaction, not
between individual puts/deletes. A bound sequence must exist with matching ID
and name; owned modes must match ownerStruct/ownerTag, and every owned sequence
must have its owner field. Load/replay applies the same graph validation.
Table/column rename keeps the sequence's original name and stable identity.
There is no independent dependency sidecar or counter file. An older SQL reader
must reject version 3; pre-ownership sequence decoders also reject unknown owner
members rather than silently discarding them.

### Numeric-Modifier Schema IR v4

A field declared as constrained `NUMERIC(p,s)` or `DECIMAL(p,s)` promotes its
struct definition to Schema IR version 4. The field's optional JSON members
`precision` and `scale` participate in the schema hash. They are valid only for
the decimal family, require precision 1..1000 and scale 0..precision, and must
either both be represented by a positive precision or both remain omitted for
the legacy unconstrained DECIMAL contract.

This promotion changes catalog meaning only. Decimal KROW values keep their
existing canonical UTF-8 representation, and row keys/index components retain
their existing encoding. Main-file, WAL, history, backup and replica formats do
not change. PostgreSQL fixed-scale display and base-10000 binary NUMERIC are
wire projections of the v4 modifiers, not duplicate stored values. Older schema
readers reject version 4 rather than discarding precision/scale and admitting
values under a weaker contract.

### Temporal-Precision Schema IR v5

Every newly authored exact `date`, `time`, `timestamp`, `timestamptz`, or
`interval` field promotes its struct definition to Schema IR version 5. The
optional JSON member `timePrecision` participates in the schema hash, is valid
only for `time`, `timestamp`, and `timestamptz`, and must be in the inclusive
range 0..6. New `timestamp`, `timestamptz`, and `interval` definitions are
rejected below v5. Legacy pre-v5 `date`/`time` catalogs remain readable for
compatibility, but exact SQL authoring never emits them below v5. The legacy
`datetime` kind and catalogs remain unchanged.

This is a catalog-contract promotion, not a physical storage-format change.
DATE, TIME, TIMESTAMP, TIMESTAMPTZ and INTERVAL retain one canonical UTF-8 KROW
value; main-file, WAL, history, backup and replica formats are unchanged.
Execution-only parsed values provide exact comparison and calendar arithmetic,
and PostgreSQL text/binary encodings are wire projections. Older Schema IR
readers reject version 5 rather than conflating timestamp-with-zone and
timestamp-without-zone or discarding declared precision.

### Character-Length Schema IR v6

A field declared as `VARCHAR(n)`, `CHARACTER VARYING(n)`, `CHAR(n)`, or
`CHARACTER(n)` promotes its struct definition to Schema IR version 6. The
optional JSON member `textLength` participates in the schema hash, is valid
only for the canonical `varchar` and `char` kinds, and must be in the inclusive
range 1..10,485,760. New `CHAR` without an explicit modifier means `CHAR(1)`;
new `VARCHAR` without a modifier remains unbounded. Pre-v6 catalogs with no
`textLength` remain readable under their previous relaxed contract.

This promotion changes catalog meaning only. KROW and WAL continue to store one
canonical UTF-8 string. `VARCHAR(n)` stores at most n Unicode characters and
retains trailing spaces. `CHAR(n)` stores the same value blank-padded to n
characters; ordinary character comparison ignores that padding while `LIKE`
observes the stored characters. Main-file, WAL, history, backup, replica and
row-key envelope versions do not change. PostgreSQL varchar/bpchar OIDs and
typmods are wire projections of the v6 field modifier, not another copy of the
value. Older Schema IR readers reject version 6 rather than silently dropping a
length constraint or changing CHAR equality.

### Exact-UUID Schema IR v7

Every UUID field newly authored by standalone SQL promotes its struct
definition to Schema IR version 7 and sets the field's optional `exactUUID`
member. The marker participates in the schema hash and is valid only for the
canonical `uuid` kind. Pre-v7 UUID fields without the marker remain readable
under their original identifier-text contract; they are not reinterpreted or
silently validated when an existing database is reopened.

An exact UUID accepts PostgreSQL-compatible upper/lower hexadecimal input,
optional braces, omitted hyphens, or hyphens after complete four-digit groups.
It stores and returns exactly one lower-case 8-4-4-4-12 spelling. The type
accepts every 128-bit UUID value regardless of UUID version; KitDB's implicit
value generator currently emits UUIDv4. Primary, unique and ordered secondary
keys use the canonical string, so equivalent input spellings cannot produce
different durable keys.

This is a catalog and admission promotion only. KROW, main-file, WAL, history,
backup, replica and row-key envelope versions are unchanged. PostgreSQL UUID
OID 2950 and its 16-byte binary representation are wire projections of the
same canonical value. Exact UUID foreign keys and field-to-field comparisons
require another exact UUID; conversion to identifier/text semantics must be
explicit. Older Schema IR readers reject version 7 rather than treating exact
UUID columns as arbitrary strings.
