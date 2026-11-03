# Windows WAL Ownership Investigation, 2026-10-22

## Conclusion

**The historical WAL-publication failure is not resolved.** Its blocking
process/handle and root cause remain unknown. One hundred executions of the
exact previously failing test passed without a storage patch. A new bounded
kernel probe also completed 512 commit/checkpoint pairs without reproducing
the spontaneous failure. Those results do not retroactively explain it.

This iteration adds controlled Windows contract tests, retained kernel
checkpoint diagnostics, and a required capture/recovery control in the lifecycle
gate. It makes failure investigation and recovery evidence stronger, but adds
no production WAL retry, sleep, timeout increase, format change, or replacement
primitive. No claim of stability or release qualification is made.

## Code Audit And Reproduction

`rotateWALLocked` runs under `commitMu`. `prepareWAL` writes and syncs the
staging header and closes its file before returning. Rotation then removes the
active WAL from the DB under `mu`, closes that handle, and calls
`MoveFileExW(REPLACE_EXISTING | WRITE_THROUGH)` through `storageReplace`.
Publication refusal reopens the old WAL; retained-history mode subsequently
marks the owner unavailable. There is no obvious extra WAL handle in the small
`fullCheckpointFixture` path, but a code audit is not proof of runtime ownership.

The original `TestCheckpointContextDeadlineAndConcurrentOwner` was run 100
times, unchanged, on disposable TempDir databases with a three-minute test
timeout. All 100 passed. The command did not contain a production retry loop;
its purpose was a bounded reproduction attempt, not replacing old evidence.

Eight relevant files are byte-identical between the failed projection snapshot
and this investigation's frozen source: `wal.go`, `replace_file_windows.go`,
`storage_io.go`, `checkpoint.go`, `checkpoint_admission.go`, the admission test,
`db.go`, and `commit_writer.go`. Their raw hashes are retained in
`storage-comparison.json`.

## Controlled File-Sharing Results

`TestWindowsWALPublicationSharing` opens a known handle on either staging or
the existing destination, keeping read/write sharing constant. Observed on
this Windows host:

| Known held file | Delete sharing | Publication result |
| --- | --- | --- |
| Existing WAL destination | Off | Denied, errno 5 |
| Existing WAL destination | On | Denied, errno 5 |
| Staging source | Off | Denied, errno 32 |
| Staging source | On | Succeeded |

Every refusal preserves the old destination and new staging headers exactly.
After closing the known handle, a separate explicit control publishes the
staging file successfully. No retry was added inside `replaceFile` or rotation.
The open-target/delete-sharing case permits either success with correct handle
identity or a refusal with both files unchanged, rather than asserting a
stronger cross-version Win32 guarantee than this primitive provides.

Thus delete sharing alone is not evidence that this legacy replacement call
must succeed. Equally, errno 5 from the old failure does not identify its owner,
prove the same scenario, or implicate antivirus. The documented Win32 flags and
the separate extended rename semantics are described by Microsoft in
[MoveFileExW](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-movefileexw)
and [FILE_RENAME_INFORMATION](https://learn.microsoft.com/en-us/windows-hardware/drivers/ddi/ntifs/ns-ntifs-_file_rename_information).
No alternative replacement API was introduced here: its compatibility and
durability contract would need separate qualification.

`TestWindowsWALCheckpointDenialRecovery` covers incremental and full-compaction
publication with retained history both off and on. It holds a real no-delete-share
WAL reader, requires the refusal to remain visible, checks that the main
checkpoint boundary is published while the old WAL is restored, and then closes
and independently reopens without retrying checkpoint. Every acknowledged value
survives, the recovered WAL base matches the exact transaction, verification
passes, and a later commit succeeds. History-enabled refusal must fail closed
until reopen; ordinary mode keeps its acknowledged value readable.

## Retained Kernel Checkpoint Probe

`TestWALCheckpointForensicCampaign` is Windows-only and explicitly opt-in. It
uses native key/value calls, not SQL or an application database. A fresh absolute
fixture root and an outside report prefix are required; existing roots/files
are refused. Eight rounds each execute 64 commits and checkpoints, crossing
incremental/compaction boundaries. Originals and verification copies remain on
disk, and no failed checkpoint is retried.

On failure it records the last ACK, completed checkpoint count, operation,
raw error/errno, and best-effort Restart Manager resource users before closing
the owner. **This observation is after Checkpoint returns**: the recovery path
may already have reopened our WAL and removed staging. Seeing our PID does not
prove our handle caused the original denial; an empty result cannot exclude a
transient user or kernel filter. No shutdown/restart API is invoked.

After owner close, the probe creates a digest-verified copy, opens only the copy
for recovery, runs Verify and checks the independent ACK model, then hashes the
original again. All eight actual rounds completed: 512 ACKs/checkpoints, eight
verified copies, and eight unchanged originals. No spontaneous failure/owner
observation was produced in this run.

The always-on `TestWALCheckpointForensicCapture` validates the evidence path by
holding a known no-delete-share handle at checkpoint 33. It requires pre-close
error capture, the known resource user, closed owners, a verified recovery copy
and unchanged originals. This controlled test is now a required Windows
lifecycle-gate entry. The separate long probe remains excluded from gates;
all six manual diagnostic opt-ins are explicitly disabled there.

## Validation And Provenance

Frozen source: commit `4d36264cbd102a162f1b54908df29f53b422080e` plus 14 recorded
overlay files, carrying the previous uncommitted projection candidate separately
from the new test/gate work. All 12 Go overlay files match the shared tree.
One post-freeze README clarification distinguishes the fixed gate's Restart
Manager query from inherited long-running diagnostic opt-ins; both document
hashes are recorded without mutating the frozen source.

- Original unchanged test: 100/100 root executions passed.
- `go test ./kitdb/... ./cmd/releasegate -count=1 -timeout=12m`: 905 root tests,
  14 packages passed; 18 skip events remain, including opt-in campaigns.
- Selected root/relational race tests, three repetitions: 27 root executions
  passed, no skips.
- `go vet ./kitdb/... ./cmd/releasegate` and `go build ./...`: passed.
- Newly built lifecycle gate: normal and race steps passed all 12 required
  entries per step, no skips. This is dirty development-source evidence.
- Opt-in retained checkpoint probe: passed eight rounds and copy verification.

Development failures are retained too. The first control build incorrectly
called Stat on the `commitWAL` interface; the test now checks the restored file
concretely. The next control run failed because its test assumed an open
delete-sharing destination must be replaceable with `MoveFileExW`. The corrected
contract validates both permitted platform outcomes and preserved bytes; no
production implementation was changed to make that test pass.

Local evidence: `.artifacts/kitdb-wal-owner-20261022/`, including the original
100-repeat log, both development failures, frozen source manifest, suite/race
JSONL, gate report, eight round reports and retained fixture/copy pairs,
assessment, and final hash index. The earlier failed projection suite and its
SHA-256 remain unchanged; this is a new investigation, not a relabeled success.

## Remaining Work

The root-cause acceptance criterion is still unmet. A future spontaneous failure
needs contemporaneous ownership/share-mode evidence; post-return resource users
have explicit limits. Keep the retained probe available for a bounded run on
the affected filesystem. Do not silently install tracing tools, change security
software, add retry policy, or promote a candidate based on later green tests.

This was Windows development validation, not Linux execution, a 24-hour soak,
SQL/pgwire performance qualification, physical power-loss testing or production
readiness. No commit, push, keep-awake request or power-plan change was made.
