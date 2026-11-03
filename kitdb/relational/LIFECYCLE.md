# KitDB Lifecycle And WAL Regression

## Short Gate

From the engine repository:

```text
go run ./cmd/releasegate --mode kitdb-lifecycle --report .artifacts/kitdb-lifecycle.json --timeout 10m
```

This runs normal and race tests with a five-minute timeout per step. The
existing gate records Git/platform identity and required JSON PASS evidence.
A dirty source report is development evidence, not a committed-candidate gate.
No network service, fixed drive, username or retained user data is required.
Tests create their own temporary fixtures.

The portable contract covers node/kernel release predicates, repeated owner
open/close with exact row/index reopen, failure cleanup and fixture retention,
balanced/noisy short native SQL workloads, ACK-model reconstruction and an
independent copy-only verification. The copy verifier rejects incorrect ACK
counts, incomplete accounting, existing destinations and non-regular files;
it checks hashes before opening the copy and again on the original afterward.
It is a quiescent test-fixture utility, not a live backup API.

On Windows, the gate also opens a real WAL handle without delete sharing and
requires replacement to fail visibly with the Windows error. It releases the
blocker, acknowledges another write and checks both explicit-checkpoint retry
and close/reopen without retry. The maintenance failure counter must stay one;
resource release is not the same as an error-free maintenance history.

The Windows roots and both recovery subcases are required separately. The
checkpoint forensic control additionally requires pre-cleanup capture of a real
denial, closed owners, an ACK-verified recovery copy and unchanged originals. Missing
or skipped cases fail the gate. Non-Windows plans select portable tests only;
a plan constructed for another platform is not evidence it has run there.

## Manual Diagnostics

The gate disables all six diagnostic opt-ins below, including for broad test
suites. These tests are deliberately not acceptance criteria based on handle,
native-thread, heap or RSS thresholds. Process counters do not identify KitDB
resource owners. Launch a diagnostic directly, not by setting flags around
`releasegate`.

| Opt-In | Direct Test | Scope |
| --- | --- | --- |
| `KITDB_LIFECYCLE_CAMPAIGN=1` | `TestKitDBLifecycleCampaign` | Two warmups plus eight windows; `KITDB_LIFECYCLE_SECONDS` bounds each at 1..30 seconds |
| `KITDB_MIXED_CAMPAIGN=1` | `TestKitDBMixedCampaign` | Bounded native SQL mixed workload; separate from a latency SLA |
| `KITDB_THREAD_ATTRIBUTION=1` | `TestKitDBThreadAttribution` | Control/database diagnostics, ten 30-second windows and idle observations; mode/root/report required |
| `KITDB_WAL_FORENSIC=1` | `TestKitDBWALForensicCampaign` | Ten 30-second windows, first worker-error resource-user observation, retained failed fixtures and copy-only verification |
| `KITDB_WAL_LOCK_CAPTURE=1` | `TestLifecycleWindowsLockCapture` | Windows-only known-resource-user probe for the diagnostic Restart Manager wrapper |
| `KITDB_WAL_CHECKPOINT_FORENSIC=1` | `TestWALCheckpointForensicCampaign` | Windows-only kernel key/value fixture: eight rounds of 64 commit/checkpoint pairs, retained originals and ACK-verified recovery copies |

For example, to validate the Windows resource-user probe without a long run:

```powershell
$env:KITDB_WAL_LOCK_CAPTURE = '1'
go test ./kitdb/relational -run '^TestLifecycleWindowsLockCapture$' -count=1 -timeout=1m
Remove-Item Env:KITDB_WAL_LOCK_CAPTURE
```

The forensic diagnostic accepts `KITDB_WAL_ROOT` as a **fresh absolute directory
path** on the chosen volume and `KITDB_WAL_REPORT` as a **fresh absolute file
path outside that fixture root**. It does not choose C:, a username, or a
fallback directory. Use a fresh root under the original filesystem when
investigating a volume-specific failure, and record that choice. The runner
creates the root exclusively; do not pre-create it. Select the exact test and
use `-count=1 -timeout=12m`. No automatic rerun, keep-awake or promotion occurs.

Restart Manager observes applications using registered resources; it does not
prove which share flags or kernel filter caused replacement denial. No
shutdown/restart API is called. An empty list cannot exclude transient users.
After failure, no original DB is opened for recovery. Copying requires complete
owner release and verification refuses incomplete ACK evidence. A successful
later run does not verify ACKs from an earlier deleted fixture.

### Kernel Checkpoint Probe

The checkpoint probe targets the small kernel fixture rather than running SQL
or the mixed-tenant campaign. It uses the same fresh absolute root/report rules
above. Select it exactly with `-count=1 -timeout=4m`; it retains both successful
and failed fixtures and never retries a failed checkpoint. It writes one
`<report>.round-NN.json` per round and, on failure, a
`<report>.failure.json` before owner cleanup. Failure evidence includes the raw
error/errno, the last acknowledged transaction, completed checkpoints and
best-effort Restart Manager users. No recovery opens the original files.

The observation occurs **after Checkpoint returns**: WAL restoration may have
reopened our own handle and removed staging. Our PID is not proof of the
original blocker, and an empty result does not exclude transient users. The
always-on `TestWALCheckpointForensicCapture` proves this capture/copy pipeline
using an explicitly known no-delete-share handle; it is not a reproduction of
an unexplained external failure.

The kernel tests `TestWindowsWALPublicationSharing` and
`TestWindowsWALCheckpointDenialRecovery` separately cover staging/target share
modes and incremental/compacted checkpoints with and without retained history.
An open destination may be refused by legacy `MoveFileExW` even with delete
sharing. Such a refusal must leave both headers unchanged; the closed-handle
control runs separately. History-enabled refusal fails closed until reopen;
the old WAL and published main boundary must reconcile with every ACK intact.

WAL publication now uses `FileRenameInfoEx` with replace/POSIX semantics on
supported Windows filesystems, keeping the staging handle through a post-rename
flush. `TestWindowsWALCheckpointSharedReader` requires compatible delete-sharing
readers to retain their old file identity without blocking checkpoint, across
incremental/compacted and history on/off paths. Readers without delete sharing
must still refuse publication visibly. Unsupported API/filesystem errors alone
fall back to the legacy primitive; access/sharing errors are not retried.
Post-rename sync/close errors mark durability uncertain and fail the owner
closed rather than restoring old offsets. The tagged Windows WAL fault tests
and the existing SQL checkpoint/storage matrices qualify these boundaries;
see [the fault lab](../FAULT_LAB.md).

## Qualification Limits

The Windows WAL replacement denial observed in the earlier lifecycle campaign
remains unexplained. A later same-C:-volume run passed, but that is neither a
production fix nor retrospective evidence for the failed window. Historical
reports in `.artifacts/` must not be overwritten or relabeled when new tests
pass. See `benchmarks/dbcompare/LIFECYCLE_2026-10-20.md` for the recorded history.

This contract uses native relational SQL. It is distinct from kernel key/value
canaries, SQL/pgwire adapter qualification, physical power-loss testing and
cross-platform stable-release review. No production retry policy, storage
format or public database API is changed by these tests.
