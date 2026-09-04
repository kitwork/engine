# KitDB v0.16 Invariants

These claims are preserved by implementation and executable tests.

## Commit

1. At most the configured queue capacity of transactions may be prepared or
   awaiting completion, and their aggregate encoded payload is bounded by the
   single-frame payload limit.
2. One database-owned writer orders admitted commits. Transaction IDs are
   monotonic and contiguous across batches and WAL rotations.
3. A transaction remains one immutable frame containing every operation. A
   group has no durable batch envelope.
4. A group writes every consecutive frame and performs one WAL `Sync`. The
   in-memory overlay changes only after that sync succeeds.
5. Every operation in a committed group becomes visible under one state lock;
   no reader observes a partially published group.
6. Caller-owned keys and values are copied before KitDB retains them.
7. An append or sync failure makes the handle unavailable and completes every
   affected request with uncertain durability. Close and reopen are required
   to resolve the durable transaction boundary.
8. Close stops admission, drains commits already accepted by the writer, and
   invalidates transactions that were not submitted.

## Schema catalog

1. Prefix `0x01` is kernel-owned catalog space. Every catalog key is exactly
   the namespace byte plus the 16 bytes represented by its definition ID.
2. Catalog definitions are bounded, valid UTF-8 JSON envelopes with positive
   versions, nonempty fields, stable IDs, names, and hashes.
3. Exact struct names are unique across different IDs. A commit batch evaluates
   catalog transactions in transaction order, so concurrent claims cannot both
   succeed.
4. An invalid catalog operation rejects its complete transaction before WAL
   append; accompanying rows and indexes are not partially committed.
5. A valid definition and its row/index mutations share one WAL frame. The
   in-memory catalog and row overlay publish together only after WAL `Sync`.
6. WAL recovery and checkpoint preserve catalog bytes through the same logical
   mutation path as every other key. Reopen derives one registry from the
   recovered main-plus-WAL state.
7. Fast open does not scan row pages for schema. The first catalog access seeks
   directly to its keyspace; verified open validates it eagerly.
8. Catalog snapshots and lookups return caller-owned definition bytes and one
   transaction/revision boundary.
9. `CatalogVersion` returns that identity without cloning definition documents
   or performing filesystem I/O after lazy catalog hydration. A record-only
   commit advances its transaction field but does not change its revision.
10. A standalone relational transaction captures catalog bytes, the kernel data
    snapshot, and the confirming catalog version while holding one relational
    writer gate. A direct kernel frontend may force a bounded recapture, but a
    DDL commit after an already-consistent snapshot cannot invalidate it, and a
    mixed catalog/data boundary is never returned to the caller.

## Record-layer catalog coherence

1. Every ORM table lookup, SQL-light schema lookup, Hrana request, PostgreSQL
   catalog lookup, DDL preflight, and record-transaction start compares the
   proxy's observed catalog revision with the kernel revision.
2. An unchanged revision does not decode or republish Schema IR. A changed
   revision is rechecked under the per-file relational writer gate, decoded as
   one complete catalog graph, validated, and published by one outer-map swap.
3. Full replacement owns only catalog-derived definitions. Source-declared
   `struct()` definitions remain present and authoritative; a catalog rename or
   drop removes stale catalog-owned names instead of incrementally merging them.
4. Local DDL binds its in-memory publication to the committed kernel revision
   before releasing the same writer gate. Other proxies discover that revision
   on their next supported entry point; no process-wide notification is
   required for correctness.
5. A record transaction binds one schema-map snapshot and catalog revision to
   one kernel snapshot. It never refreshes that schema in place. A changed
   catalog revision makes commit conflict, including an otherwise read-only
   commit; existing optimistic data-write conflict rules remain unchanged.

## Record-layer relational constraints

These invariants belong to the Kitwork relational adapter; the kernel continues
to treat their physical keys and values as opaque transactional records.

1. CHECK constraints persist bounded expression IR with immutable field tags;
   SQL text is never the catalog authority. Expressions are validated and
   compiled before a definition is published, then remain read-only.
2. A CHECK rejects only a false result. Null/unknown passes, matching SQL
   three-valued semantics. Parameters, aggregates, missing fields, unsupported
   functions, and non-boolean/non-numeric results fail before publication.
3. `NO ACTION` and `RESTRICT` are immediate. Every restrictive relationship is
   checked before mutation actions at the same level, so relationship iteration
   order cannot hide a blocker behind a cascade.
4. Parent row/index changes are staged before `CASCADE`, `SET NULL`, or
   `SET DEFAULT`. Child work reuses the ordinary update/delete pipeline and the
   same fixed-snapshot transaction overlay, including types, checks, unique and
   foreign constraints, indexes, cancellation, and savepoints.
5. Any descendant failure rolls the top-level statement back to its original
   savepoint. One action tree affects at most 10,000 rows, descends at most 64
   levels, and rejects a recursive update that revisits an active row.

## Record-layer destructive migration

1. Source declaration drift never authorizes field loss or retyping. Only one
   explicit SQL `ALTER TABLE` statement can grant one field-ID-scoped
   destructive intent.
2. A rename preserves field ID/tag and records the old name as an alias. A drop
   cannot target a primary key or a field used by an index, unique, CHECK, or
   foreign-key dependency. Single-field references to a renamed target publish
   their canonical target name in the same schema batch; composite references
   already use target field IDs. A type change cannot target a primary key or
   either side of a foreign key.
3. The atomic drop/type path inspects and rewrites at most 10,000 rows. Every
   cast, row type, CHECK, unique, foreign-key, and derived-index result is
   validated before publication. A larger transition is admitted to the
   segmented path only when one explicitly authorized field is independent of
   primary, unique, secondary/partial-index, CHECK, and foreign-key layouts.
4. A large independent-field transition performs only a bounded admission scan,
   stopping after it proves the 10,000-row atomic ceiling was crossed, before
   publishing a checksummed `KRMS` cursor and nonzero target row generation. It
   never performs a full-table preflight under one writer-gate hold. Admission
   and each later chunk still use that gate, so this is not a lock-free or
   zero-pause DDL claim.
5. While `KRMS` is in its row phase, the source catalog remains active and
   ordinary source-schema reads continue. For an independent-field migration,
   every admitted create, update, and delete maintains both source and target
   physical row generations in one record transaction. A primary-key rekey
   instead rejects table writes for the whole row phase because the logical row
   identity changes. Each node-owned background dispatch transforms,
   validates, and writes at most 2,048 rows/8 MiB for an online field migration.
   A write-fenced primary rekey may inspect at most 8,192 rows, but the same
   8-MiB/16,384-mutation transaction ceilings still stop it earlier. Each chunk
   advances its cursor atomically. A failing row
   leaves earlier chunks and `KRMS` durable but cannot advance that chunk or
   publish the target; repairing or deleting it through the source schema wakes
   background resume. A stale target-schema request cannot read or write
   before cutover.
6. Before cutover, cancellation atomically changes `KRMS` from `rows` to
   `cancel`. From that transaction onward, source-schema CRUD writes only the
   active source generation. Ordinary target-prefix deletion is bounded to
   2,048 keys/8 MiB per WAL transaction; write-fenced primary rekey cancellation
   and post-cutover cleanup may delete at most 8,192 keys under the same 8-MiB
   ceiling. Each cleanup cursor shares one WAL transaction with those deletes;
   the final transaction removes `KRMS`. Restart resumes cleanup. Legacy
   generation-zero state and post-cutover `cleanup` cannot be cancelled.
7. The final backfill transaction publishes target catalog, migration audit,
   row-layout epoch, active generation, retired source prefix, and cleanup
   state together. Thus a snapshot observes either the complete source or the
   complete target. Cleanup is bounded and resumable; it deletes only a retired
   prefix and removes that prefix and `KRMS` atomically on completion. A legacy
   generation-zero `KRMS` remains readable with its original fail-closed,
   writer-gated resume semantics.
8. The node registers one stable process-local driver and coalesces row work by
   canonical database path. One maintenance dispatch reloads `KRMS`, holds the
   same file-owned relational gate as foreground CRUD, commits at most one
   bounded chunk, releases its lease, and requeues pending work at background
   priority. It retains no definition body, row, or cursor outside the file.
   Invalid data, no-progress, panic, cancellation, and shutdown stop safely;
   repair commits and reopen hydration may wake a new task. Weighted scheduling
   allows urgent and normal maintenance between row chunks.
9. The bounded atomic path commits rewritten rows, rebuilt indexes, catalog,
   index-layout metadata, and audit in one ordinary WAL transaction. On either
   path, failed validation, dependency mismatch, collision, or failed commit
   cannot publish the target catalog; the online path may retain only its
   unpublished, resumable shadow state.
10. Dropped tags are not copied as unknown fields and `nextFieldTag` never moves
   backward. Truly unknown tags remain byte-preserved across the rewrite.
11. Only an explicit SQL `ALTER TABLE ... ALTER PRIMARY KEY (...)` may authorize
   a physical rekey, and only for a catalog-owned table. Its schema transition
   must affect exactly one table, preserve every non-primary field contract,
   have no independent unique constraint, and have no incoming or outgoing
   foreign key. Source-declared primary keys remain protected.
12. A rekey computes the target logical key from every migrated row. Null or
   invalid tuples and same-chunk collisions fail before that build chunk and
   cursor commit. Each successful chunk commits the target row, every target
   ordered-index entry, and the next `KRMS` cursor together. Cross-chunk
   collisions may overwrite only unpublished shadow keys, so build completion
   enters a separate bounded `verify` scan. Each verification dispatch visits
   at most 65,536 target keys and commits its cursor plus unique-target-row
   count. That durable count must exactly equal the source rows processed before the final transaction
   atomically publishes catalog, row generation, index generations, both layout
   epochs, audit, and cleanup intent. No reader can combine source rows with
   target indexes or observe a collision-shortened target.
13. Rekey cancellation removes the unpublished target row prefix and all target
   index prefixes before deleting `KRMS`. Post-cutover cleanup removes the
   retired source row prefix and all retired index prefixes one at a time. The
   prefix ordinal and delete cursor are durable, bounded, and restart-resumable;
   writes resume only after cancellation completes or target publication makes
   the new key authoritative. A foreground snapshot is never force-closed to
   help migration; a long reader may delay checkpoint or cleanup progress
   without changing the published catalog or durable cursor.

## Record-layer index lifecycle

These invariants belong to the Kitwork relational adapter; the kernel continues
to treat their physical keys and values as opaque transactional records.

1. A resumable index build stores its exact source and target definitions,
   hashes, index signatures, nonzero shadow generations, retired prefixes,
   phase, and cursor in one CRC32C-checked `KIBS` value. KIBS v1 remains
   readable and resumable as implicit generation zero. A mismatched declaration
   or malformed state fails closed.
2. Every row chunk and its next cursor commit in the same ordinary KitDB WAL
   transaction. Recovery therefore sees either the previous complete cursor or
   the next complete cursor, never derived entries without their progress.
3. The per-file relational writer gate serializes a chunk with CRUD commit.
   CRUD between chunks maintains target shadows and any source-only active
   generation. Rows ordered before the cursor remain correct through that write
   path; rows after it are read in their latest committed state by a later
   chunk. Write admission refreshes physical layout after a transaction advance
   and rejects an older app-generation schema once another target is accepted.
4. The planner cannot consume an index whose durable state is still in the row
   phase. It must use another published index or a row scan.
5. An index-only schema target and its final row chunk publish the catalog,
   migration audit, `KIGM` active map, monotonic layout epoch, and cleanup state
   in one transaction. A codec target publishes its marker with its final row
   chunk. Shadow-key presence alone is never a publication signal.
6. Read execution validates its prepared layout epoch and opens the kernel
   snapshot while holding the same writer gate used by cutover. It therefore
   binds to one complete layout. Retired-generation and legacy-codec cleanup
   are bounded and resumable; already-open kernel snapshots retain their old
   view.
7. Generation zero is exactly the existing ordered v2 keyspace. New physical
   generations use v3 plus a nonzero fixed-width ID. Primary, unique, row,
   kernel WAL, and main-file formats do not change.
8. Foreground admission commits exactly one metadata-only `KIBS` intent and
   opens no row cursor; its work is independent of table cardinality. The
   Kitwork adapter registers one stateless secondary-index driver with the
   node; accepted DDL, catalog hydration, and compatible dual-writes wake a
   coalesced background task. Every row, cutover, and cleanup continuation is
   node-owned: one dispatch takes the same per-file relational gate, reloads
   `KIBS`, commits at most one bounded chunk, releases its lease, and rejoins
   the weighted queue only after durable progress. Process memory and the queue
   are never index authority. This is not strict zero-pause DDL, a unique-index
   generation protocol, or a field/value backfill protocol.
9. A logical table or ordinary-index rename never changes its physical
   identity. Table rename preserves the catalog struct ID and commits every
   catalog-owned incoming reference in the same WAL transaction. Index rename
   preserves the explicit or legacy physical index ID and atomically remaps any
   active generation signature. A released logical name may be reused only
   with a fresh identity. Pending row or index maintenance, source-owned schema,
   an unresolved dependency, collision, or failed commit leaves the original
   catalog and physical keyspaces authoritative.

## Record-layer planner statistics

These records are rebuildable Kitwork-adapter metadata. They use the kernel's
ordinary transaction, WAL, checkpoint, history, replication, and checksum
ordering; they do not become kernel truth.

1. One struct owns at most one fixed-key `KSTA` value beneath namespace `0x04`.
   Its envelope is versioned, length-bounded to 1 MiB, and protected by CRC32C.
   A malformed value fails closed instead of silently changing a plan.
2. `ANALYZE` reads rows and active indexes through one fixed optimistic record
   snapshot, then writes its statistics/source transaction watermark and
   removes that struct's dirty marker in the same record transaction. If another
   commit advances the database before publication, conflict aborts the
   analysis; no partially current metadata is published.
3. Row and index-entry counts are exact for the analyzed snapshot. Distinct
   counts are estimates from fixed 1,024-register sketches, with at most 64
   active indexes and 8 leading prefixes per index. Analysis memory is bounded
   independently of row cardinality.
4. Once statistics exist, every row mutation of that struct writes one fixed
   dirty marker in the same transaction as rows and indexes. Planning may
   consume statistics only when struct identity and schema hash match and no
   committed or transaction-local dirty marker exists. Commits for unrelated
   structs do not invalidate them. A dirty marker or schema change returns the
   planner to deterministic structural ranking rather than trusting stale
   cardinality.
5. Estimates may break a tie between structurally valid candidates and may be
   reported by `EXPLAIN`; they never weaken predicates, constraints, residual
   evaluation, result bounds, or snapshot correctness. Deleting and rebuilding
   all statistics cannot change query results.

## Resumable import progress

1. One logical import ID maps deterministically to one key beneath namespace
   `0x05`. Its `KIMP` envelope is versioned, bounded to 64 KiB, and protected by
   CRC32C. The complete ID inside the payload detects a key digest mismatch.
2. A progress-bearing COPY binds one stable source label and format, struct ID,
   and ordered field IDs. A later chunk must present exactly the previous
   chunk number, source offset, and SHA-256 checksum. Completion and
   cancellation are mutually exclusive terminal states; either state rejects
   every later chunk under that import ID.
3. Decoded rows, constraints, derived indexes, statistics dirty marker, and
   the next KIMP value are mutations of one ordinary record transaction. COPY
   failure, explicit rollback, cancellation, timeout, disconnect, or optimistic
   conflict publishes neither rows nor progress.
4. A lost acknowledgement may replay a chunk, but its previous watermark no
   longer matches after a successful commit. The importer reads the durable
   status and treats only the exact already-committed chunk as success; it never
   writes that chunk twice under the same import ID.
5. Before resuming, the CSV/JSONL importer reparses the complete committed
   prefix and verifies seed, semantic SHA-256 chain, row count, and exact byte
   offset. A changed or truncated prefix fails before a new COPY begins. This
   proof covers accidental source drift, not a hostile SHA-256 collision.
6. `PRAGMA import_cancel(id)` advances KIMP through one ordinary record
   transaction and is idempotent. `PRAGMA import_forget(id)` accepts only a
   terminal state and removes only KIMP. Neither operation removes or rolls
   back rows from already committed chunks.
7. Chunk row/byte limits bound client memory and one record transaction. The
   PostgreSQL gateway additionally admits a bounded number of COPY streams
   before `BeginCopyIn`, caps active and queued work per host-trusted
   app/database key, and selects queued keys by bounded weighted virtual runtime.
   Waiting consumes the configured COPY lifetime; cancellation removes its
   waiter and every release rebalances the next eligible key. Aggregate metrics
   never expose admission keys. This fairness is local to one listener, not a
   cross-listener fleet authority.
8. KIMP does not make the file one unbounded transaction, does not replace
   backup, and is not a server-side source-file cursor. Process-crash evidence
   before publication, after WAL commit but before acknowledgement, and after
   checkpoint lives in `TestKitDBPostgresResumableImportHardCrashMatrix`.

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

1. `DB.Snapshot` captures one main generation, one copied WAL overlay, one
   committed transaction, and its boundary checksum while holding the database
   state read lock.
2. A snapshot owns an independent read handle; replacing the live in-process
   reader during incremental checkpoint cannot invalidate it.
3. Snapshot point reads and cursors never observe commits made after capture.
4. Range start is inclusive, end is exclusive, prefix intersects both bounds,
   and cursor output remains in strict raw-key order.
5. A range cursor seeks to the first candidate sparse page in every segment; it
   does not scan from the beginning merely to satisfy a later start key.
6. Cursor keys and values returned to callers are copies.
7. `Snapshot.ScanKeys` exposes no row value. Its callback receives one
   read-only key valid only until that callback returns; forward scans may
   reuse one checksummed page buffer per immutable segment. It has the same
   snapshot, ordering, range, tombstone, and overlay semantics as a Cursor.
8. At most 32 snapshots are active per database. `DB.Close` invalidates and
   closes all remaining snapshots.
9. Active snapshots permit commits and incremental checkpoints but prevent
   full compaction, which fails explicitly with `ErrSnapshotsActive`.
10. `GetWithStats`, `Cursor.Stats`, and `ScanKeys` statistics are query-local
    observations. No global or atomic counter is shared across readers, and
    ordinary cursor iteration does not increment a statistic per record.
11. One reported page read means one successfully read, checksummed and decoded
    KitDB main-file page. It does not prove a hardware read beneath the
    operating-system page cache. Cache hit/miss describes only KitDB's LRU;
    sequential cursors report direct reads as cache bypasses.
12. Generation-entry counts include immutable-segment duplicates, tombstones
    and merge lookahead actually consumed before the logical cursor stops. They
    are not inferred from the number of rows returned to the caller.

## Backup anchors

1. An anchor is a standalone compacted format-v3 main file, not a new backup
   container format.
2. Anchor construction uses one bounded read snapshot and therefore includes
   synced commits still present only in the WAL overlay without forcing a
   source checkpoint or WAL rotation.
3. Commits and incremental checkpoints may continue while the anchor is built.
   The owned snapshot prevents full compaction until construction ends.
4. The output preserves the database identity, captured transaction, and exact
   boundary checksum while discarding obsolete physical versions and
   tombstones.
5. Before publication, KitDB requires the physical size to equal the active
   boundary, verifies every active page and logical row, and computes SHA-256
   over all exact bytes.
6. The destination must not exist. Publication uses an exclusive
   same-filesystem link followed by a containing-directory sync where
   supported, so an existing anchor is never replaced.
7. Cancellation, source corruption, and validation failure do not publish the
   destination. Temporary names are never opened as anchors.
8. A returned anchor can be independently reopened at its captured transaction.
   History retention and remote durability remain separate protocols.

## Restore by transaction

1. Restore accepts one verified format-v3 anchor and an explicit target at or
   after that anchor transaction. It never infers or silently substitutes an
   anchor.
2. The history metadata identity must equal the anchor database identity.
   Canonical history names must cover one contiguous range through the target.
3. Replay begins only after the history chain proves the anchor transaction and
   boundary checksum, either in the first used WAL header or at the exact frame
   containing an anchor that lies inside a retained segment.
4. Transactions are applied strictly in order. A gap, duplicate boundary,
   wrong identity, malformed frame, or checksum mismatch fails closed.
5. Every used immutable history file is validated completely, even when the
   requested target occurs before its final frame.
6. Replay coalesces mutations in memory only until the 32 MiB or 262,144-key
   bound, then appends one sorted immutable generation. One source transaction
   may exceed the byte threshold but remains bounded by the WAL format limits.
7. Restored generations preserve the source transaction checksum at each
   materialized boundary. Bounded generation compaction uses the existing
   checksum-verifying merge path.
8. The final staging image has no replay WAL or writer lock. Its exact active
   boundary, every active page and logical row, and SHA-256 are verified before
   publication.
9. The destination must not exist and is published exclusively only after
   complete verification. Cancellation or any pre-publication failure leaves
   no destination.
10. Restore does not mutate the source anchor or history directory. The output
    is independently openable at exactly the requested transaction.

## Checkpoint

1. A checkpoint contains only state visible after a synced commit and is
   serialized against later commits.
2. A normal format-v3 checkpoint below the 32-segment bound appends one sorted
   segment containing one mutation per overlay key, followed by a complete
   bounded manifest.
3. The append path publishes in this order: write segment and manifest, sync,
   write the inactive superblock slot, sync, install the new in-process reader,
   optionally seal retained history, then rotate the WAL.
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

## Retained history

1. Retained history is opt-in for a database without a history directory. A
   valid existing history directory keeps retention enabled on later opens.
2. Retention adds no write, allocation, or sync to the commit hot path. Only a
   checkpoint or stale-WAL recovery publishes a history segment.
3. The checksummed `META` anchor binds history to one database identity and one
   exclusive base transaction/checksum. Enabling history later does not claim
   to retain earlier transactions.
4. Every `.khist` file is an exact complete WAL range. Its canonical filename,
   WAL header, contiguous frame IDs, final frame checksum, and exact file size
   are validated before publication or reuse.
5. Main-file publication precedes history publication. History publication
   precedes WAL rotation. A history seal failure, or a later WAL-rotation
   failure, makes the live handle unavailable. The old WAL remains recovery
   authority, and reopen can validate and reuse an already canonical segment.
6. Recovery that selects a newer main with a stale WAL seals the proven WAL
   range before completing rotation when that range is covered by retention.
7. History staging names are never listed or replayed. Canonical active segment
   names form one contiguous range beginning at `META.base_transaction + 1`.
   Fully retired prefix files may remain as cleanup debris; a segment may not
   straddle the base. Open requires the active range to reach the main
   checkpoint, unless the WAL proves one continuous bridge through it.
8. `WalkHistory` first checkpoints to one fixed transaction boundary. It then
   streams complete transactions strictly in order and excludes later commits.
9. Missing ranges return `ErrHistoryGap`. Invalid metadata, malformed frames,
   or checksum mismatches return `ErrCorrupt`. A consumer advances its durable
   watermark only after the complete walk returns nil.
10. `PINS` is a bounded, checksummed, database-identified file containing at
    most 1,024 named transaction/checksum cursors. A pin is accepted only while
    that exact cursor is still provable from the retained chain.
11. Pin creation, movement, and release write and sync a complete replacement
    before atomically publishing it. An uncertain post-publication sync makes
    the live handle unavailable until reopen resolves the durable pin set.
12. Pruning is explicit, rejects a target beyond the oldest pin, and advances
    only through complete segments. It checksum-validates retired segments and
    proves the first retained header links to the new base.
13. Pruning publishes and syncs replacement `META` before deleting old files.
    A crash may leave an ignored fully retired prefix, never a missing active
    prefix. Cleanup failure is observable; retrying the same published boundary
    removes that debris without advancing `META` again.
14. Segment cardinality is bounded to 65,536, pin cardinality to 1,024, and
    directory reads are batched. Retention policy is process-local and has no
    per-database timer. It may run as typed node maintenance after a successful
    checkpoint, never on the commit path. It computes a whole-segment boundary
    from verified timestamps and bytes, caps that boundary at the oldest
    durable pin, and reuses the explicit prune publication path. A pin-limited
    policy reports pressure instead of deleting protected history.
15. New local WAL frames carry a positive UTC Unix-nanosecond commit timestamp
    covered by the frame checksum. Timestamps are strictly increasing in
    transaction order; replica apply preserves the source timestamp exactly.
    Legacy untimestamped frames remain transaction-replayable but cannot be
    used to invent a point-in-time recovery boundary.
16. Timestamp recovery owns a verified compacted image at the retained base.
    Pruning prepares the next base image before publishing advanced metadata.
    Time resolution verifies the complete base-to-boundary transaction,
    checksum, and timestamp chain and selects only an exact transaction edge.
17. A recovery fork is published exclusively and never overwrites an existing
    path. It preserves the selected rows, schema, transaction, and boundary
    checksum but receives a fresh database identity before becoming writable.

## One-way replicas

1. A replica bootstrap is a verified standalone anchor with the source's
   stable database identity and an associated durable history pin.
2. Bootstrap pins a source boundary before building the anchor. It advances
   that pin only after history proves the anchor transaction/checksum. A
   post-publication failure keeps a conservative older pin.
3. A replica handle refuses `Begin`. Promotion is explicit: close it and reopen
   the file without replica mode.
4. Replica protocol v1 is a bounded transport-neutral API envelope. Replica
   wire v1 is an encoding of that envelope, not a new main-file, WAL, history,
   apply cursor, or target recovery log.
5. Every batch binds one canonical database identity, exact `from`, `to`, and
   fixed source-boundary cursors, a contiguous transaction sequence, and its
   exact WAL-frame byte count. The hard ceilings are 4,096 transactions and
   67,108,920 frame bytes.
6. Source reads refuse a cursor or source boundary that the complete retained
   checksum chain cannot prove. Continuations preserve the first fixed boundary
   and never include commits published after it.
7. Catch-up refuses a target with a different database identity or a cursor
   outside the batch. A target at the batch end is an idempotent no-op; a target
   at a matching interior cursor resumes only the missing suffix.
8. Only one apply or catch-up owns a target handle at a time. Reentrant or
   concurrent attempts fail with `ErrReplicaBusy` rather than interleave.
9. The complete envelope and every transaction's re-encoded frame checksum are
   validated before the first mutation from that apply attempt.
10. Replica commits use the ordinary bounded commit writer, catalog validation,
    WAL sync, overlay publication, listeners, checkpoint, and recovery paths.
11. The target main/WAL transaction and checksum are the only durable apply
    cursor. There is no independently published watermark that can get ahead of
    target data.
12. A source pin moves only after an acknowledgement exactly equals a complete
    batch end and that cursor is re-proved against retained history. Local
    catch-up acknowledges its final batch only after reaching the fixed source
    boundary. Cancellation leaves partial target progress durable and the older
    pin conservative; retry resumes the same batch from the target cursor.
13. `CatchUpReplica` itself produces and applies protocol-v1 batches; there is
    no legacy direct-event write path beside the protocol.
14. Wire v1 reuses canonical WAL frame-v1 bytes. The fixed header is CRC32C
    protected, every frame retains its source CRC32C, and SHA-256 covers the
    complete envelope except the digest field. Decoders enforce count and byte
    limits before body allocation and require exact EOF.
15. A filesystem batch or ACK final name is linked only from a completely
    written, synced, closed staging file and is never overwritten. Directory
    sync follows publication; an uncertain sync is observable and retryable.
16. The filesystem target publishes an ACK only after `ApplyReplicaBatch`
    reaches its exact end. A crash after WAL commit but before ACK is recovered
    by idempotently replaying the batch and regenerating the ACK.
17. The filesystem source advances or confirms its durable pin before removing
    transport files. Cleanup removes and syncs the batch before the ACK; replay
    of an older ACK never moves a newer pin backward.
18. Final filenames are verified against decoded identities and cursors.
    Temporary files are never apply candidates, count toward byte pressure, and
    are removed only by explicit quiescent cleanup. File count, total bytes,
    directory entries, message size, and ACK size are bounded.
19. This transport contract remains one-way and host-operated. It does not
    claim authentication, persistent topology, synchronous primary commit
    acknowledgement, leader election, peer discovery, or multi-writer merge.
    A node controller may repeatedly compose these exact stages, but it cannot
    add another durable cursor or weaken their publication order.

## Ownership

1. At most one KitDB writer process owns a database path.
2. The stable lock sidecar is not replaced during main-file or WAL publication.
3. Reads, transaction preparation, and commit submission on one open handle are
   race-safe.
4. Closing drains the admitted commit queue and waits for the writer before
   closing the WAL and writer lock.
5. Operations on a closed or unavailable handle fail explicitly.

## Node fleet ownership

1. `kitdb/node.Manager` is outside the transaction kernel. It may open, share,
   idle, and close handles but cannot alter WAL, checkpoint, or recovery rules.
2. One lexically canonical absolute path maps to at most one managed handle.
   Concurrent acquisition waits for one publication and reuses that handle.
3. The handle count, aggregate reserved page-cache bytes, and concurrent opens
   are bounded before an open begins. Opening and closing entries continue to
   consume their reservations until fully resolved.
4. Every caller owns an explicit lease. An entry with any active lease is
   ineligible for idle eviction; callers release only after all transactions,
   snapshots, and cursors derived from the handle have finished.
5. Released handles enter a most-recently-used idle list. Capacity pressure and
   `TrimIdle` close the least-recently-used non-warm entry only. A process-local
   warm policy protects the handle and its bounded caches, not the complete
   database payload; manager close and explicit removal still close it.
6. If capacity is full and a leased non-warm entry may later become evictable,
   acquisition waits with context cancellation rather than exceeding a limit
   or interrupting an active tenant. If only warm open handles hold the needed
   capacity, acquisition fails explicitly instead of waiting without a possible
   wake-up.
7. Open options and explicitly requested database policy are stable while a
   handle is managed. Conflicting acquisition fails explicitly; an idle handle
   may close and reopen under new open options, while a deliberate live policy
   change uses the manager's typed policy operation.
8. Manager close stops admission, waits for active leases and in-progress
   opens, closes all idle handles, and does not report completion while it
   still owns a database entry.
9. A registered filesystem replica link owns one unique target, mailbox, and
   source-pin pair within its manager. No mailbox path may also be any link's
   source or target database. Registration validates existing source and target
   files, rejects an existing non-directory mailbox, but neither opens a
   database nor creates the mailbox.
10. All links share one dispatcher and a fixed, bounded worker pool. Link count
    and concurrent cycles are admitted up front; an idle link owns no database
    lease, transaction body, per-link goroutine, or unbounded metric label.
11. One link cycle is exactly one bounded publish, apply, and acknowledge
    composition through the ordinary maintenance scheduler. The source pin,
    target WAL cursor, and mailbox artifacts remain the only authoritative
    restart state; process-local configuration is registered again after
    restart.
12. Failures use capped exponential backoff with deterministic jitter, success
    uses separate active and idle cadence, and explicit pause prevents future
    automatic cycles without interrupting a running durable stage. Manager
   close cancels and drains the shared controller and maintenance pool before
   reporting completion.

The hard-process evidence for items 9-12 is maintained in
[`FAULT_LAB.md`](FAULT_LAB.md). Its child exits without closing the manager or
database handles at every publish/apply/pin/cleanup boundary; a new manager must
then satisfy the recovery oracle using no process-local cursor.

## Node production protection

1. One registered policy owns one canonical source, one canonical backup
   directory, and at most one named publisher within its manager. Registration
   requires an existing regular source, retained history, explicit RPO/RTO
   ages, bounded retention, and a backup directory outside the live source and
   its history tree.
2. Every policy shares one dispatcher and a fixed worker pool bounded before it
   starts. An idle policy owns no database lease, page cache, transaction body,
   per-policy goroutine, or unbounded metric label.
3. A cycle composes `ScheduleBackup`; it does not write an anchor, checkpoint,
   or pin through another implementation. Existing anchors are adopted only
   after complete verification and source identity proof by that maintenance
   path.
4. Backup discovery reads at most the configured store-entry ceiling. Every
   supervisor-named anchor is fully verified before reuse or pruning. A
   malformed, corrupt, or mixed-identity store fails closed; retention removes
   only verified same-identity anchors in the supervisor namespace.
5. A restore drill targets a unique temporary file, verifies the restored main
   image, and compares `kitdb-logical-digest/v1` over sorted key/value bytes with
   the source anchor. No path, key, or value enters cycle or health evidence.
6. `ready` requires backup and restore evidence inside their explicit age
   ceilings. If a publisher is configured, it also requires publication
   evidence for the exact current backup identity, transaction, record/byte
   count, SHA-256, and anchor creation time. Missing, mismatched, expired,
   future-dated, or integrity-unsafe evidence is `unsafe`; a paused policy or
   retryable failure with still-fresh exact evidence is `degraded`.
7. Policy configuration, retry time, and counters are process-local. After
   restart the host registers the policy again; immutable anchors and the
   durable source pin are the only recovery authority, and a fresh restore
   drill is required before returning to `ready`.
8. A `ProductionAnchorPublisher` may report success only after retrieving and
   completely verifying the published object. Its receipt is path-free and
   must exactly match the local verified anchor; an upload acknowledgement or
   partial receipt is not evidence and fails closed.
9. `VerifiedDirectoryPublisher` accepts only a pre-created regular directory,
   uses one deterministic immutable content name, bounds all directory reads,
   cleans only its bounded abandoned restore-staging namespace, keeps at least
   two anchors, verifies before reuse or deletion, and refuses mixed identities,
   backward transactions, forks, malformed names, corruption, and destination
   overlap. It cannot assert that the directory is physically independent from
   the live host.
10. Publisher labels and registry cardinality are bounded. One label has one
    policy owner, and overlapping built-in publisher destinations cannot be
    registered under different labels. Publisher configuration and authority
    remain process-local and host-only.
11. Manager close stops policy admission, cancels and drains the shared policy
    workers with maintenance, and never reports completion while a production
    cycle still owns a lease or publisher call.

## Node database catalog

1. A tenant that creates a database through the node-mode PostgreSQL adapter
   owns one reserved `.data/.kitdb-node.kitdb`. It is an ordinary KitDB
   database using the same main-file, WAL, lock, checkpoint, and recovery
   contracts; it is not exposed as a user database.
2. Each entry is a bounded binary v2 envelope protected by CRC32C; v1 remains
   readable. The catalog accepts at most 4,096 logical databases and rejects unknown keys, versions,
   states, reserved fields, invalid lengths, invalid names, and malformed
   16-byte database identities.
3. An entry persists only logical/storage names, creation time, database
   identity, lifecycle state, and the source capability's storage name. It
   never persists an authentication token. Startup resolves authorization from
   the current source declaration and fails closed when that authority is
   absent or conflicts with a source-owned database path. PostgreSQL and HTTP
   authorization also resolve the current source capability on every new
   session/request; a missing capability hides its managed databases.
4. One app-runtime catalog mutex serializes create, rename, drop, and startup
   reconciliation across sessions. Catalog leases use the bounded node manager,
   reserve zero page-cache bytes, and may be LRU-evicted like any cold handle.
5. Create syncs `creating` before publishing a target path. Empty creation and
   point-in-time fork creation both sync `active` with the target's fresh
   database identity before exposing the logical name. Storage that existed
   before the intent is never adopted implicitly.
6. Startup deletes a `creating` intent with no storage. If a complete target
   exists, it verifies the database and core schema catalog, records its
   identity as `active`, and then exposes it. Invalid or partial storage fails
   closed rather than being guessed or deleted.
7. Drop blocks new sessions in memory, syncs `dropping`, removes the target
   through `node.Manager`, and only then deletes the catalog entry. Startup
   resumes `dropping`; a missing target is an idempotent completed removal. A
   source capability cannot be dropped while any catalog entry references it.
8. An `active` entry checks bounded file presence at startup without opening
   the target. The first managed open must match the catalog database identity,
   so a swapped file is rejected before any row or schema operation.
9. Source-path conflict detection precedes every creating/dropping recovery
   action. Reconciliation cannot delete or replace a newly source-declared
   database even when a stale SQL-managed entry claims the same name.
10. The node catalog is lifecycle authority only for SQL-managed databases.
    User rows, Schema IR, transaction state, backup boundaries, and replica
    cursors remain solely in their owning database files.
11. Rename is one catalog transaction that deletes the old logical key and
    writes the new key while preserving storage name, database identity,
    capability reference, and creation time. It is refused while target
    sessions exist. Authentication is frozen across publication, then all
    authorized maintenance-session views advance in memory. A crash before the
    transaction keeps the old name; a crash after it exposes only the new name.
    Main, WAL, history, lock, and pin paths are never renamed.

The publication and recovery portions of items 5-8 are exercised by the
seven-boundary hard-process create/drop matrix, the three-boundary empty-create
matrix, and the two-boundary rename matrix documented in
[`FAULT_LAB.md`](FAULT_LAB.md). Their children exit
while the catalog mutex, manager, and handles are still live; the parent
recovers through a fresh tenant runtime using no process-local lifecycle state.

`Sync` and file locking are only as strong as the host operating system and
filesystem. CRC32C detects accidental corruption, not malicious rewriting.
Network filesystems remain outside the supported boundary.

## Sequence Reservation Boundary

1. Sequence creation/deletion publishes definition and counter atomically in
   one normal WAL frame. Counter allocation is serialized by the DB owner,
   not by a per-client or Kitwork mutex. Session currval/lastval is not durable.
2. A successful nextval is returned only after its lease high-watermark was WAL
   synced and published. CACHE 1 publishes every value; CACHE N serves later
   values from the in-memory lease without another write. A failed or uncertain
   lease Sync returns no usable value and fences writes. SETVAL/RESTART publish
   directly and invalidate any lease. Aborted row transactions reclaim nothing.
3. CommitIfUnchanged validates its snapshot inside the commit writer, including
   earlier accepted requests in the same group. Only internal nextval/setval
   counter reservations are exempt. User writes, catalog changes, raw writes
   to reserved keys and RESTART remain conflict boundaries.
4. lastDataTx is a process-local conflict watermark, never a second durability
   identity. Open initializes it conservatively to recovered lastTx; snapshots
   do not survive restart. All commits still advance lastTx/history normally.
5. Describe never evaluates sequence functions; read-only SQL cannot reserve
   or reset numbers. Sequence IDs prevent session state leaking across a
   drop/recreate under the same name. One O(1) lease record per active sequence
   and the 4096-value cache ceiling keep CPU work and memory explicitly bounded.
6. Restored/cloned databases can issue numbers already used elsewhere. CYCLE
   and explicit resets can also repeat values. No gapless or cross-branch
   uniqueness guarantee is made.
7. Schema IR v3 defaults bind immutable sequence IDs. The kernel validates the
   final catalog dependency graph on commit and load/replay. Table plus owned
   sequences publish/remove in one frame; failed DDL leaves neither dangling
   references nor orphan ownership. External references block owner deletion.
   Renames retain ownership by stable struct ID and field tag. SMALLINT, INTEGER
   and BIGINT sequences enforce their own signed bounds; a bound field and
   sequence must have the same exact width.
8. INSERT and non-primary UPDATE DEFAULT reserve through the same synced
   allocator, never MAX(id)+1 or a separate ORM counter. Reservations commit
   before the row write-set, so statement/savepoint rollback and conflict retries
   can leave gaps. A recreated name cannot capture an old default binding.
   Read-only rejection precedes reservation; Describe never reserves.
9. Closing/crashing discards the unused tail of every process-local lease.
   Reopen, backup, replica and PITR continue after the persisted high-watermark,
   never inside that tail. CACHE can increase gaps but cannot create duplicate
   allocation unless CYCLE, explicit reset or restore branching already permits it.
