# KitDB Storage Format v3 (KitDB v0.7)

All integers are little-endian. Sizes count bytes. Readers reject nonzero
reserved fields and format versions they do not understand. CRC32C uses the
Castagnoli polynomial and detects accidental corruption, not malicious edits.

KitDB v0.7 does not change the v3 bytes introduced by v0.6. Read snapshots and
range cursors are runtime views over one declared generation plus a copied WAL
overlay; they add no on-disk object or recovery candidate.

## Files

For `Open("tenant.kitdb")`, the storage set is:

```text
tenant.kitdb       canonical generation main file
tenant.kitdb.wal   transactions after the snapshot base
tenant.kitdb.lock  stable exclusive-writer lock
```

Incremental checkpoints append to the main file. Compaction and WAL staging
files are created in the same directory, synced, and atomically replaced.
Staging names are never recovery candidates.

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
| 12 | 4 | flags, currently `0` |
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

The payload begins with a 4-byte operation count. Each operation has a 9-byte
header followed by its key and value:

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

## Checkpoint ordering

An incremental format-v3 checkpoint uses this publication order:

1. Truncate bytes beyond the active file boundary and append one sorted
   mutation segment plus its checksummed directory.
2. Append a complete manifest containing every active segment descriptor.
3. Sync the main file so segment and manifest bytes are durable.
4. Write the inactive generation slot with generation `active + 1`.
5. Sync the main file again; the new slot is now the publication point.
6. Install a reader for the new generation and clear the in-memory overlay.
7. Write and sync a WAL-header staging file linked to that main boundary.
8. Close the previous WAL, atomically replace it, sync its directory, and reopen
   it before admitting another commit.

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
there, recovery finishes rotation. If it contains later complete frames, they
are replayed and remain authoritative until the next checkpoint.

Publishing the WAL before the main file is forbidden because it could discard
the only durable copy of committed transactions.
