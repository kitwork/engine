# KitDB v0.7 Invariants

These claims are preserved by implementation and executable tests.

## Commit

1. Commits are serialized by one database owner.
2. Transaction IDs are monotonic and contiguous across WAL rotations.
3. A transaction is one immutable frame containing every operation.
4. The in-memory WAL overlay changes only after the complete frame is written and `Sync`
   succeeds.
5. Every operation in one committed frame becomes visible under one state lock.
6. Caller-owned keys and values are copied before KitDB retains them.
7. An append or sync failure makes the handle unavailable. Close and reopen are
   required to resolve the durable state.

## Main file

1. `DB.Path()` names one regular canonical main file, not a database directory.
2. The format-v3 header protects the database identity and reserved fields with
   CRC32C.
3. Two independently checksummed superblock slots alternate. Recovery selects
   the valid slot with the greatest generation and rejects different valid
   slots that claim the same generation.
4. The active slot binds the database identity, generation, transaction
   boundary and checksum, logical record count, segment count, manifest
   location and checksum, and exact active file boundary.
5. Every active manifest, segment directory, and immutable mutation page is
   checksum-protected. CRC32C detects accidental corruption, not hostile edits.
6. Segment generations and transaction boundaries are strictly increasing.
   Mutations inside each segment are encoded once in strictly increasing raw-key
   order.
7. Incremental publication syncs new segment and manifest bytes before writing
   the inactive slot, then syncs that slot before reporting publication.
8. Bytes beyond the active slot's file boundary are not part of the database.
   A later incremental checkpoint truncates this abandoned tail before append.
9. Compaction syncs and closes a complete staging file before atomic
   replacement. The containing directory is synced where Go and the host expose
   that operation.
10. Readers never select temporary staging files. Format-v1 and format-v2
    snapshots remain readable and a checkpoint rewrites them as format v3
    without changing visible rows.

## Row reads and memory

1. Opening a format-v3 main file reads and verifies only fixed metadata, the
   active manifest, and active segment directories; mutation pages remain on
   disk.
2. Each directory entry retains first and last keys for at most 128 mutations
   or approximately 64 KiB of encoded bytes.
3. WAL mutations shadow every main segment, including tombstones, before any
   main-file page is read.
4. A main-file point lookup searches segments newest first and reads at most one
   selected page per segment. The first matching put or tombstone is final.
5. A selected page is checksum-verified before it is decoded or returned.
6. `Walk` performs a bounded k-way merge of active segments, keeps the newest
   mutation for each key, suppresses tombstones, and validates the declared
   logical record count.
7. `Verify` reads and verifies every active page without populating the LRU
   cache.
8. Decoded pages are immutable and the LRU's accounted bytes never exceed the
   configured per-database limit. A cache-disabled database remains correct.
9. Every value returned to a caller is a caller-owned copy.

## WAL and recovery

1. A WAL header identifies one database and one base transaction/checksum.
2. A WAL base may equal the main transaction or be older only when it can prove
   the exact main transaction frame and boundary checksum.
3. A WAL base newer than the main file is corruption.
4. Complete frames are validated before their operations are applied.
5. Frames at or before the main transaction are verified but not applied twice.
6. Complete frames after the main transaction are replayed in order.
7. An incomplete final frame after the required main boundary is uncommitted
   and truncated at its start.
8. A missing, incomplete, or mismatched frame required to prove the main
   boundary is corruption rather than a recoverable tail.
9. Recovery never scans past declared frame bounds or allocates an unbounded
   payload from disk metadata.
10. Reopening the same durable bytes produces the same state and transaction.

## Read snapshots

1. `DB.Snapshot` captures one main generation, one copied WAL overlay, and one
   committed transaction while holding the database state read lock.
2. A snapshot owns an independent read handle; replacing the live in-process
   reader during incremental checkpoint cannot invalidate it.
3. Snapshot point reads and cursors never observe commits made after capture.
4. Range start is inclusive, end is exclusive, prefix intersects both bounds,
   and cursor output remains in strict raw-key order.
5. A range cursor seeks to the first candidate sparse page in every segment; it
   does not scan from the beginning merely to satisfy a later start key.
6. Cursor keys and values returned to callers are copies.
7. At most 32 snapshots are active per database. `DB.Close` invalidates and
   closes all remaining snapshots.
8. Active snapshots permit commits and incremental checkpoints but prevent
   full compaction, which fails explicitly with `ErrSnapshotsActive`.

## Checkpoint

1. A checkpoint contains only state visible after a synced commit and is
   serialized against later commits.
2. A normal format-v3 checkpoint below the 32-segment bound appends one sorted
   segment containing one mutation per overlay key, followed by a complete
   bounded manifest.
3. The append path publishes in this order: write segment and manifest, sync,
   write the inactive superblock slot, sync, install the new in-process reader,
   then rotate the WAL.
4. A failed or uncertain slot write or sync makes the handle unavailable. The
   durable winner is resolved by closing and reopening.
5. A torn newer slot leaves the older valid slot selectable. A fully published
   newer slot wins even if later physical tail bytes exist.
6. Incremental checkpoint does not rescan immutable older pages. Corruption in
   an older page remains detectable by `Get`, `Walk`, `Verify`, or compaction and
   is never repaired or hidden.
7. A checkpoint that starts with 32 active segments triggers streaming
   compaction into at most one base segment. Compaction removes obsolete
   mutations and tombstones and requires zero active snapshots.
8. Compaction and legacy migration walk every source page through the
   checksum-verifying iterator and use memory proportional to the overlay,
   sparse directories, segment heap, one row, and fixed buffers.
9. Reads continue during compaction staging construction. File-handle close,
   atomic replacement, reopen, and pointer swap occur under one short exclusive
   state lock for Windows compatibility.
10. Main-file publication always precedes WAL rotation. A failed main-file
    publication leaves the existing WAL authoritative.
11. A crash after main publication and before WAL rotation leaves enough old
    WAL bytes to validate the new main transaction boundary and checksum.
12. WAL rotation publishes a header-only WAL linked to the main transaction
    before any later commit is admitted. Checkpoint success bounds future replay
    to frames committed after that base.

## Ownership

1. At most one KitDB writer process owns a database path.
2. The stable lock sidecar is not replaced during main-file or WAL publication.
3. Reads and commits on one open handle are race-safe.
4. Closing waits for an active commit before closing the WAL and writer lock.
5. Operations on a closed or unavailable handle fail explicitly.

`Sync` and file locking are only as strong as the host operating system and
filesystem. CRC32C detects accidental corruption, not malicious rewriting.
Network filesystems remain outside the supported boundary.
