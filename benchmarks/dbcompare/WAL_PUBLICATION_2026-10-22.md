# Windows WAL Publication Fix, 2026-10-22

## Decision

Keep the handle-based WAL publication change as a tested development candidate.
It fixes a reproducible compatibility defect: a reader opened with read, write
and delete sharing blocked checkpoint when the previous `MoveFileExW` path
replaced the WAL. The same regression body failed all four cases before the
change and passed all 40 cases in ten repetitions afterward.

**This does not identify the owner of the earlier spontaneous failure.** The
historical log remains unchanged. We have fixed a demonstrable failure class,
not proved that every `Access is denied` has the same cause or been eliminated.
Neither the candidate nor KitDB is declared stable here.

## Reproduction

`TestWindowsWALCheckpointSharedReader` opens a real read handle on the active
WAL with `FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE`, then calls
Checkpoint. It covers incremental and full-compaction checkpoints, each with
retained history off/on. The old implementation fails with publication denial
(and the expected unavailable owner in history mode) in all four cases.

After the fix, the test requires the new WAL to start at the exact acknowledged
checkpoint boundary. It closes/reopens the DB and commits another value while
the original reader remains open. That reader must still see its original WAL
bytes, not the replacement or later commit. No expected-success condition was
relaxed to turn this regression green.

`before_shared_reader_test.go` is a retained copy matching the SHA-256 recorded
before the negative test. A Go AST/formatter comparison verifies that its test
function is identical to the final test function, despite separate new path and
fallback tests being added to the file. Baseline storage hashes also match the
captured pre-edit content after exact CRLF-to-LF rendering; this is recorded
explicitly, not treated as raw byte equality.

## Implementation

Only WAL creation/recovery and rotation use the new publication helper. Main
snapshot and history-control replacement keep their existing primitive. There
is no storage-format, public configuration, VM, dependency or retry-policy change.

On supported Windows filesystems the helper opens the synced staging file with
write/delete access and write-through, then uses `SetFileInformationByHandle`
with `FileRenameInfoEx` and replace/POSIX flags. It keeps that file identity
pinned through a post-rename Sync and handle close. Microsoft documents the
extended rename flags and preservation of existing reader handles in
[FILE_RENAME_INFORMATION](https://learn.microsoft.com/en-us/windows-hardware/drivers/ddi/ntifs/ns-ntifs-_file_rename_information),
with the user-mode structure/class in
[FILE_RENAME_INFO](https://learn.microsoft.com/en-us/windows/win32/api/winbase/ns-winbase-file_rename_info)
and [FILE_INFO_BY_HANDLE_CLASS](https://learn.microsoft.com/en-us/windows/win32/api/minwinbase/ne-minwinbase-file_info_by_handle_class).

Unsupported rename API/filesystem errors alone (1, 50, 87, 120, 127) close the
source handle and use the legacy replacement path. Access denied and sharing
violation (5, 32) return directly: no sleep, retry or attempt to bypass a reader
that disallows delete sharing. Deterministic classification tests require one
rename attempt, correct bytes and a closed source handle. They simulate status
codes; they are not execution evidence on every older filesystem/Windows build.

The helper returns **whether publication occurred**, independently of error.
A pre-publication refusal restores the old WAL as before. After successful
rename, any Sync/close error reports `ErrDurabilityUncertain` and makes the
owner unavailable. It must not reopen the new file using old WAL offsets.
Non-Windows publication retains its existing rename and directory-sync flow.

This adds a source handle and post-rename Sync per modern Windows publication.
No checkpoint-latency benchmark or performance win is claimed. Filesystem and
flush guarantees still matter; these tests are not physical power-loss evidence.

## Verification

All commands ran sequentially on one frozen Windows/amd64 development source,
based on `4d36264cbd102a162f1b54908df29f53b422080e` plus 21 recorded overlay files.
The prior projection candidate is carried separately. Unrelated compiler/UI
changes in the shared worktree are excluded from the measured source; this is
not a qualification of that entire evolving worktree.

- Full `go test ./kitdb/... ./cmd/releasegate`: 908 root tests across 14 packages
  passed; 18 skip events remain, including manual campaigns.
- Shared-reader regression: four negative cases before; ten successful root
  repetitions / 40 successful cases after, no skips.
- Selected kernel/relational race tests, three repetitions: 36 root executions
  passed, no skips. These include the shared-reader, path/ABI, fallback,
  no-delete-share denial/recovery and forensic-capture controls.
- Tagged Windows and existing SQL fault matrices: 10 root tests across two
  packages passed. The sole skip is the direct child-only helper; parents
  actually launch hard-exit children. Existing SQL storage-I/O and exact
  checkpoint-publication matrices both pass.
- Tagged publication fault/race tests: three root tests passed, no skips.
  Sync failure after rename must fail closed and recover exact ACKs; hard-exit
  children stop after rename and after Sync, with history off/on.
- `go vet ./kitdb/... ./cmd/releasegate` and `go build ./...`: passed on the
  frozen candidate. The short lifecycle gate passes all 12 required entries
  in each of its normal/race steps, with no skips.
- Retained native kernel probe: eight rounds / 512 commit-checkpoint pairs,
  eight ACK-verified recovery copies and unchanged originals.

The path test includes a Unicode/surrogate-pair filename and a path over 260
characters, with an explicit FILE_RENAME_INFO layout check. Existing controls
still require a genuine no-delete-share handle to cause visible failure and
preserve acknowledged data on close/reopen. The generic legacy replacement
sharing test remains unchanged: it is not mislabeled as the new WAL primitive.

Early development logs remain too: one compile error for a Win32 constant not
exported by Go's syscall package, and a test assertion that expected Stat on a
closed Windows handle to return os.ErrClosed. The latter now uses zero-length
Write to check the Go file's closed state without changing any bytes. These
failed logs were not overwritten by the successful checks.

## Evidence And Limits

Local evidence: `.artifacts/kitdb-wal-publish-20261022/`. It includes the negative
test and original hashes, reconstructed/hash-verified baseline probe, frozen
source manifest, AST equivalence result, all raw suite/race/fault logs, gate
report, retained fixtures/copies, assessment and final hash index. Historical
projection and ownership-investigation raw evidence remains separately sealed.

Readers that forbid delete sharing, incompatible staging users, ACLs, read-only
attributes, unsupported filesystems and transient kernel filters can still deny
publication. The earlier failure's exact blocker is unknown. This patch must
not be advertised as resolving all Windows publication errors or as proof that
antivirus caused the old one.

No Linux run, physical power cut, 24-hour soak, cross-database performance
comparison, commit, push, keep-awake request or power-plan change occurred.
