# Search on-disk format family

This document is the executable format contract for `.ks` immutable segment
files. All integers are little-endian. Absolute offsets are unsigned 64-bit
file offsets. Writers publish through a same-directory temporary file, `Sync`,
close, and a platform publication primitive to a previously unused final path.
Unix uses same-directory rename. Windows uses `MoveFileExW` with
`MOVEFILE_WRITE_THROUGH`. Unix additionally syncs the containing directory
after publishing each immutable artifact and manifest. Publication errors are
reported; if a manifest name is already visible when directory sync fails, the
writer adopts that generation so cleanup cannot remove files it references.
The API reports this state as `ErrDurabilityUncertain` together with the
published generation rather than representing it as an unpublished mutation.

`.kitwork-search.writer.lock` is an operational lock file, not part of any
search format. One writer holds its OS lock for the complete writer lifetime;
readers do not acquire it. The file may remain after a clean exit or crash and
is ignored by readers and garbage collection. Lock ownership is the live OS
handle, not the file's existence.

## Segment V3

Segment V3 is the current writer format. Readers continue to accept Segment V1
and Segment V2. V3 keeps the same file shape as V2 but lets posting blocks
optionally carry phrase positions for exact phrase search.

### Header

Every file starts with a fixed 256-byte header.

| Offset | Bytes | Value |
| ---: | ---: | --- |
| 0 | 8 | ASCII magic `KWSEG001` |
| 8 | 4 | Format version, currently `2` |
| 12 | 4 | Header size, currently `256` |
| 16 | 8 | Exact file size |
| 24 | 32 | SHA-256 schema fingerprint |
| 56 | 4 | Local document count |
| 60 | 2 | Field count |
| 62 | 2 | Reserved flags |
| 64 | 168 | Seven 24-byte section descriptors |
| 232 | 4 | Header CRC32C with this field zeroed |
| 236 | 20 | Reserved |

Each section descriptor contains:

| Relative offset | Bytes | Value |
| ---: | ---: | --- |
| 0 | 8 | Absolute section offset |
| 8 | 8 | Section byte length |
| 16 | 4 | CRC32C of complete section bytes |
| 20 | 4 | Reserved |

Sections must be ordered, non-overlapping, contained by the recorded file size,
and cover the file from byte 256 through its exact end. Empty sections are
allowed and have a CRC32C value of zero.

### Sections

The descriptor and physical order is fixed:

1. Field statistics
2. Postings
3. Dictionary blocks
4. Dictionary sparse index
5. Field norms
6. Stored identifier offsets
7. Stored identifier data

#### Field statistics

One unsigned 64-bit total token count per field. BM25 average field length is
`total_tokens / document_count`.

#### Postings

A term posting list is a sequence of independent blocks. The dictionary stores
the absolute offset, total byte length, and document frequency of each list.

Each block contains at most 128 documents and starts with a 32-byte header:

| Relative offset | Bytes | Value |
| ---: | ---: | --- |
| 0 | 2 | Document count |
| 2 | 2 | Flags, bit 0 means phrase positions are present |
| 4 | 4 | First local document ID |
| 8 | 4 | Last local document ID |
| 12 | 4 | Maximum term frequency |
| 16 | 4 | Minimum exact field norm in the block |
| 20 | 4 | Payload byte length |
| 24 | 4 | Payload CRC32C |
| 28 | 4 | Header CRC32C with this field zeroed |

The payload stores unsigned varint pairs `(document_gap, term_frequency)`.
When the positions flag is set, each posting appends its term positions as
strictly increasing varint gaps after the frequency. The first gap in every
block is zero because its absolute document ID is in the block header. Later
gaps must be positive. The independently protected header lets a query safely
derive a BM25 upper bound and skip the payload without decoding it. Full
verification additionally proves that `maximum_tf`, `minimum_norm`, and any
stored positions match the payload and norm section.

#### Dictionary blocks

Terms are sorted by `(field_id, term_bytes)`. Blocks contain at most 64 terms
and never cross a field boundary. A block begins with a 16-bit entry count and
16 reserved bits.

Each entry has a 32-byte fixed prefix followed by term suffix bytes:

| Relative offset | Bytes | Value |
| ---: | ---: | --- |
| 0 | 2 | Common byte prefix length with previous term |
| 2 | 2 | Term suffix byte length |
| 4 | 4 | Document frequency |
| 8 | 8 | Absolute postings offset |
| 16 | 8 | Postings byte length |
| 24 | 4 | Maximum term frequency across the complete list |
| 28 | 4 | Minimum exact field norm across the complete list |

The first term has a zero common prefix. Reconstructed terms must be valid UTF-8
and strictly increasing. The dictionary block CRC protects term-level score
bounds before the query planner uses them. The planner sums these bounds to
reject complete segments against an index-wide Top-K threshold, then uses the
lead term's per-block bounds for finer pruning inside a competitive segment.

#### Dictionary sparse index

The reader loads this small section on open. It starts with `KDI1`, followed by
a 32-bit block count. Each variable-size entry contains:

| Relative offset | Bytes | Value |
| ---: | ---: | --- |
| 0 | 2 | Field ID |
| 2 | 2 | First term byte length |
| 4 | 8 | Absolute dictionary block offset |
| 12 | 4 | Dictionary block byte length |
| 16 | 4 | Dictionary block CRC32C |
| 20 | variable | Complete first term bytes |

Lookup binary-searches this sparse index, reads one dictionary block, verifies
its CRC32C, and scans no more than 64 prefix-compressed entries.

#### Field norms

Norms are field-major exact unsigned 32-bit token counts. The section length is
`field_count * document_count * 4`. A reader loads one field lazily when that
field is first searched.

#### Stored identifiers

The offset section contains `document_count + 1` unsigned 64-bit offsets into
the stored data section. The first offset is zero, values are monotonic, and the
last offset equals stored data length. Identifier bytes are valid UTF-8 because
the builder rejects invalid input.

### Integrity model

`OpenSegment` validates:

- header magic, version, size, and CRC32C;
- schema fingerprint and field count;
- section bounds and ordering;
- field statistics and sparse dictionary checksums;
- dictionary block coverage;
- exact norm and stored-offset lengths.

Request-time lookup verifies every dictionary block, every posting header it
uses for pruning, and every posting payload it decodes. `Segment.Verify`
additionally streams every section checksum, decodes every dictionary and
posting list, proves block/list score bounds against postings and field norms,
validates contiguous coverage, checks stored offsets, and recomputes field
token totals from norms.

Malformed input must return an error wrapping `ErrCorruptSegment`. Decoders may
not panic, loop indefinitely, or allocate from untrusted counts before checking
the enclosing section bounds.

## Manifest V2

An index snapshot is one immutable manifest named
`manifest-<20-digit-generation>.km`. A writer publishes every new segment
before publishing its manifest. Each artifact is file-synced, published, and
directory-synced before it can be referenced. The manifest follows the same
ordering and is the sole commit point. Readers ignore temporary files and
unreferenced segments, scan exact manifest names, and open the highest
generation. A corrupt highest generation is reported; readers do not silently
fall back to older data.

Every manifest starts with a fixed 96-byte header:

| Offset | Bytes | Value |
| ---: | ---: | --- |
| 0 | 8 | ASCII magic `KWMAN001` |
| 8 | 4 | Format version, currently `2` |
| 12 | 4 | Header size, currently `96` |
| 16 | 8 | Exact manifest file size |
| 24 | 32 | SHA-256 schema fingerprint |
| 56 | 8 | Positive generation number |
| 64 | 4 | Segment count, at most 256 |
| 68 | 4 | Reserved flags, must be zero |
| 72 | 4 | CRC32C of the complete entry body |
| 76 | 4 | Header CRC32C with this field zeroed |
| 80 | 16 | Reserved, must be zero |

The V2 body contains one variable-size entry per segment, in stable ordinal
order:

| Relative offset | Bytes | Value |
| ---: | ---: | --- |
| 0 | 2 | Segment filename byte length, at most 255 |
| 2 | 2 | Identifier filename byte length, at most 255 |
| 4 | 2 | Deletion filename byte length, at most 255 |
| 6 | 2 | Reserved flags, must be zero |
| 8 | 4 | Positive physical segment document count |
| 12 | 4 | Deleted document count, at most physical count |
| 16 | 8 | Exact `.ks` segment file size |
| 24 | 8 | Exact `.ki` identifier sidecar size, or zero |
| 32 | 8 | Exact `.kd` deletion sidecar size, or zero |
| 40 | 8 | Reserved, must be zero |
| 48 | variable | Segment, identifier, then deletion filenames |

Names may contain only ASCII letters, digits, `.`, `_`, and `-`; path
separators are rejected. Every artifact name must be unique within a manifest.
An identifier sidecar is optional only for backwards compatibility. A deletion
sidecar is required exactly when deleted count is non-zero.

Readers also accept Manifest V1. Its entry is the former 16-byte fixed prefix
followed by one `.ks` name: name length at offset 0, zero flags at offset 2,
document count at offset 4, and segment bytes at offset 8. Writers always emit
V2. A V1 segment remains searchable and can be compacted into a V2 segment;
identity mutation uses a bounded legacy scan until that compaction happens.

On open, every referenced artifact must match the manifest schema, document
count, and exact byte size. Physical ordinals still use immutable segment order.
Live document counts and field-token totals subtract deletion metadata before
index-wide BM25 statistics are computed.

Manifest files are capped at 64 MiB. A committed manifest may contain zero
segments, which represents a valid empty index. The writer keeps old manifests
and artifacts until generation-aware garbage collection first removes
manifests below an explicit drain boundary, syncs the
directory, and then removes artifacts unreachable from retained manifests. A
zero boundary retains all manifests and only collects true orphans.

## Identifier index V1

Each new segment has an immutable `.ki` sidecar. It is not on the search path;
the writer opens and fully verifies it before the first identity mutation.

The fixed 96-byte header is:

| Offset | Bytes | Value |
| ---: | ---: | --- |
| 0 | 8 | ASCII magic `KWID0001` |
| 8 | 4 | Format version, currently `1` |
| 12 | 4 | Header size, currently `96` |
| 16 | 8 | Exact file size |
| 24 | 32 | SHA-256 schema fingerprint |
| 56 | 4 | Segment physical document count |
| 60 | 4 | Entry size, currently `20` |
| 64 | 4 | Body CRC32C |
| 68 | 4 | Header CRC32C with this field zeroed |
| 72 | 24 | Reserved, must be zero |

The body has exactly one fixed-width entry per document:

| Relative offset | Bytes | Value |
| ---: | ---: | --- |
| 0 | 16 | First 128 bits of SHA-256(identifier bytes) |
| 16 | 4 | Local document ID |

Entries are strictly sorted by `(hash, local_document_id)`. Binary lookup finds
the hash range, then reads the actual identifier from `.ks` before accepting a
match. Hash collisions therefore affect lookup work, not correctness. Full
verification proves body CRC, ordering, document bounds, hash correctness, and
the one-entry-per-document permutation.

## Deletion index V1

An immutable `.kd` sidecar represents all deleted documents for one segment in
one manifest generation. Updating the deletion set writes a new sidecar; it
never modifies the sidecar retained by an older snapshot.

The fixed 96-byte header is:

| Offset | Bytes | Value |
| ---: | ---: | --- |
| 0 | 8 | ASCII magic `KWDEL001` |
| 8 | 4 | Format version, currently `1` |
| 12 | 4 | Header size, currently `96` |
| 16 | 8 | Exact file size |
| 24 | 32 | SHA-256 schema fingerprint |
| 56 | 4 | Segment physical document count |
| 60 | 4 | Deleted document count |
| 64 | 2 | Field count |
| 66 | 6 | Reserved, must be zero |
| 72 | 4 | Body CRC32C |
| 76 | 4 | Header CRC32C with this field zeroed |
| 80 | 16 | Reserved, must be zero |

The body is the concatenation of:

1. one unsigned 64-bit deleted token total per field;
2. strictly increasing unsigned 32-bit deleted local document IDs;
3. a `ceil(physical_document_count / 8)` membership bitset.

The reader proves that IDs and bitset agree. `Index.Verify` additionally sums
the selected field norms and proves every deleted token total. Search skips the
bitset members while scanning postings and intersects tombstone IDs with each
query term to derive exact live document frequency. Compaction remaps only live
documents and therefore removes the `.kd` sidecar from its output segment.

## Compatibility

All formats are experimental. Until a stable module release:

- readers accept Segment V1. Its posting blocks have the former 24-byte header
  `(count, reserved, first_doc, last_doc, max_tf, payload_length, payload_crc)`,
  and dictionary entries have the former 24-byte fixed prefix without score
  bounds. V1 queries remain exact but deliberately disable Block-Max pruning;
- writers and compaction always emit Segment V2;
- incompatible layout changes increment the segment format version;
- analyzer behavior changes require a new analyzer identifier;
- schema field order, name, analyzer identifier, and boost change its hash;
- readers reject unknown versions instead of guessing;
- a search index remains derived data and must always be rebuildable.
