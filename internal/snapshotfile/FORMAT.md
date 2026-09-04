# Experimental Projection Containers

These are rebuildable sidecar formats, not canonical KitDB storage, WAL,
backups, or a 1.0 compatibility promise. All integers below are little-endian.
CRC32C detects accidental corruption; it does not authenticate hostile data.

## KSNAP001 (Legacy, Still Readable)

The existing 40-byte header points to one checksummed JSON directory at EOF.
Each entry is a single contiguous section. Publication syncs a temporary file,
then replaces the destination. Search snapshots still use this writer.

## KSNAP002 (Columnar Generations)

```text
0       root slot A (4096 bytes)
4096    root slot B (4096 bytes)
8192    immutable bytes: entry data, historical directories, abandoned tails
...     new/rebuilt chunk data, followed by a complete new JSON directory
```

Each root slot is independently checksummed:

| Offset | Size | Content |
| --- | ---: | --- |
| 0 | 8 | `KSNAP002` |
| 8 | 8 | Nonzero generation number |
| 16 | 8 | Directory file offset |
| 24 | 8 | Directory length, at most 8 MiB |
| 32 | 8 | Published end = directory offset + length |
| 40 | 4 | Directory CRC32C |
| 44 | 4 | Reserved, zero |
| 48 | 16 | Random nonzero container identity |
| 64 | 4028 | Reserved, zero |
| 4092 | 4 | CRC32C of bytes 0..4091 |

Creation initializes both slots to zero and publishes generation 1 into slot B.
The first eight file bytes therefore need not contain the magic yet. Readers
check both slots, validate directory bounds/checksums/entries, and select the
highest usable generation. An invalid newest root/directory permits fallback
to the previous valid one. Two valid headers with different identities or
duplicate usable generation numbers are rejected. The container identity is
distinct from the source database identity in projection metadata.

The directory retains `Metadata` and `Entries`. A v2 entry has a name, zero
legacy `Offset`, total logical `Length`, and `Extents: [{Offset, Length}, ...]`.
Extents concatenate in logical order, which need not be physical file order.
Empty sections have no extents. Extents must lie after the two roots and before
the selected directory, have positive lengths summing to the logical length,
and cannot overlap within that generation, even across entries. Unreferenced
gaps are permitted. The directory is bounded to 16384 entries and 65536 total
extents. KCOL additionally enforces its own chunk count/coverage budgets.

`Reader.Section` exposes a bounded `io.SectionReader`. A fragmented section
uses binary-searched extent offsets over the same file handle; it is not
materialized or extracted into another file. Logical KCOL group/chunk offsets
remain unchanged as an interface. Opening verifies roots and directory, not
every payload byte. The columnar layer validates payload format/checksums.

## Append Protocol

The owner must serialize publishers. KitDB uses its existing per-Engine build
admission and canonical process lock. This container is not independently a
multi-process database or a synchronization service.

1. Check that the destination is the same regular file and exact base generation
   held by the source reader. Reject stale bases and generation overflow.
2. Append new bytes at physical EOF, not at the last published end. Reuse only
   ranges from the bound base reader. References do not read/write payload;
   the caller must validate the referenced KCOL bytes first.
3. Append a complete directory for the new logical generation, then sync the
   file. Old data and directories are never overwritten.
4. Check cancellation, then write the other root slot and sync again. A credible
   but unusable newer header still advances the next generation number. Always
   overwrite the slot opposite the selected valid root, not a parity guess.
5. After root writing starts, cancellation is not treated as rollback. A write,
   sync or close error here can mean publication is uncertain; reopen and verify
   rather than retrying the same writer or claiming the old root is guaranteed.

An append failure before root writing leaves only unreachable tail bytes.
Close does not truncate them: an existing reader might still hold a later
generation whose root was subsequently damaged. Reclamation uses drained
whole-file compaction. This also avoids overwriting old reader-visible bytes
when appending after fallback to an older root.

Readers hold immutable directory/extent maps and file handles. Append does not
change their payloads. KitDB currently still drains query readers briefly at
publication; this is not a zero-pause claim. Data copying/rebuilding happens
outside that lock.

## Compaction and Upgrade

Creating/replacing a container uses a same-directory `.projection-*` temporary
file. Both data/directory and its root are synced before rename. Replacement
must drain all readers first, including the builder's base handle on Windows.
Unix also syncs the parent directory. Windows has no portable directory sync
here; a lost sidecar publication requires a canonical-source rebuild.

On explicit columnar refresh, compact if obsolete bytes exceed the larger of
live container bytes and 1 MiB. Live bytes mean the two root pages, current
directory and currently referenced payloads. Obsolete bytes include previous
directories, replaced chunks and incomplete tails; some are still needed by
old readers until those readers drain. Compaction verifies/copies surviving
chunks and rebuilds dirty ones into a new container identity. V1 upgrades use
the same replacement path. Canonical KROW/WAL formats do not change.

This is a threshold policy, not a hard filesystem quota or bounded-duration
compaction. An append may cross the threshold and the next explicit refresh
will compact. Old/new files coexist during compaction, so free space is still
required. Directory metadata is rewritten per publication. A no-change refresh
below the threshold verifies the old KCOL chunks but writes zero bytes.

## Verification Evidence

Tests cover cross-extent reads, pinned old readers, stale bases, bad references,
overlapping/out-of-bounds extents, corrupt/truncated directories, torn roots,
identity mismatch, canceled/failed writes, and compaction. Child processes exit
without cleanup after entry append, directory sync, root write, root sync,
and around replacement rename. A bounded directory-decoder fuzz target is
retained. These are process-crash and format tests, not a proof against physical
power loss, dishonest fsync, unsupported filesystems or malicious corruption.
Unpublished replacement temp files can survive a crash; automatic orphan-file
cleanup is not implemented. Never delete unrelated files to make a test pass.
