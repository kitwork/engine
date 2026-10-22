# Windows Lifecycle Diagnostics: Publication Failure Retained

## Outcome

The initial repeated lifecycle campaign **FAILED**; it did not establish an
eight-window resource plateau. A separately recorded diagnostic follow-up
completed all windows without changing the engine, but still requires process
thread review. Neither the later diagnostic nor the earlier 30/30 clean Windows
gate overrides the initial failure.

The baseline was candidate `3ae270052df8a3a347f811599a24f677d0bf8b00`, with
a recorded, uncommitted test-only overlay. Windows/amd64, Go 1.26.0,
GOMAXPROCS=4. Neither kernel/SQL implementation nor dependencies changed.

## Fixed Workload

One process was to execute two warmup windows and eight measured windows,
alternating balanced/noisy native SQL across 16 databases. Every window requested
30 seconds, with existing query/checkpoint limits unchanged. Checkpoint, exact
ACK/sync accounting, independent row/index reopen, manager closure and kernel
resource drain were required. This is diagnostics, not a latency benchmark.

Full per-tenant windows stream to JSONL; only fixed, preallocated summaries
remain in RAM. Process handles, OS threads, goroutines, post-GC heap and RSS
are sampled between windows. Heap/goroutine profiles supplement observations.
Process memory still includes test logging, JSON/profile caches and Go runtime
residency; it must not be treated as KitDB-only retained memory.

## Observed Failure

The two warmups and first measured window completed: 48 independent database
lifetimes and 49,417 acknowledged transactions passed the reopen/sync models.
All three ended at 254 process handles, 24 OS threads and two goroutines.
The initial sample had 146 handles and eight OS threads. These observations
are consistent with an initialization effect, but do not prove attribution or
absence of a leak: only one measured window completed.

The fourth window stopped after 8,714.6185 ms with one maintenance failure:

```text
database 9 checkpoint: kitdb: publish rotated WAL: rename ... tenant_09.kitdb.wal:
Access is denied.
```

Six windows were never started. The failing window recorded 6,193 acknowledged
transactions but did not reach independent reopen or post-cleanup sampling.
Do not retroactively claim those ACKs were verified, lost or corrupted. The
original blocker was not captured; an antivirus, foreign process or internal
handle cannot be identified from this errno alone. Original `TempDir` fixtures
were cleaned by the test runner and were not retained for later inspection.

## Follow-Up Coverage

`TestLifecycleWindowsWALReplaceDenial` holds a real Windows WAL reader without
delete sharing. It requires the actual native replacement denial, exactly one
visible maintenance failure, continued writes after releasing that reader,
kernel/manager drain, and independent row/index reopen. Separate cases cover
closing/reopening without another checkpoint and explicitly checkpointing again.
It adds no automatic production retry or fault-injection hook.

The initial focused normal/race suites plus ten normal and three race repeats
passed 30 controlled denial/recovery scenarios. This establishes the bounded
recovery contract for this controlled blocker, NOT the outcome of the original
failed database or completion of the repeated mixed campaign.

The final fixture-retention overlay then passed all ten required focused root
tests in both normal and race modes, plus `go vet`. Its unchanged Windows
recovery test adds four scenarios, for 34 controlled scenarios in total.

The final test harness also:

- Keeps post-close stats and the complete failure cause on error paths.
- Separates resource release from healthy zero-failure counters; a released
  manager with a failed checkpoint does not become a successful window.
- Tests cleanup after an early profile/setup failure.
- Supports `KITDB_LIFECYCLE_ROOT` for retaining failed fixtures. It atomically
  creates a new directory and refuses any existing root, including an empty
  one. Successful window fixtures are removed; failed ones remain.
- Captures failure resource samples and profiles after owner cleanup.

## Reproduction

Ordinary focused regression tests are short; the large campaign is opt-in:

```powershell
go test ./kitdb/relational '-run=^TestLifecycle|^TestMixedCampaignSmoke$' -count=1 -timeout=5m
go test -race ./kitdb/relational '-run=^TestLifecycle|^TestMixedCampaignSmoke$' -count=1 -timeout=5m

# Use a fresh parent and an isolated, source-identified development snapshot.
New-Item -ItemType Directory .artifacts/kitdb-lifecycle-new
$parent = (Resolve-Path .artifacts/kitdb-lifecycle-new).Path
$env:KITDB_LIFECYCLE_CAMPAIGN = '1'
$env:KITDB_LIFECYCLE_SECONDS = '30'
$env:KITDB_LIFECYCLE_REPORT = Join-Path $parent 'lifecycle.json'
$env:KITDB_LIFECYCLE_ROOT = Join-Path $parent 'fixtures'
go test ./kitdb/relational '-run=^TestKitDBLifecycleCampaign$' -count=1 -timeout=12m -v
```

The root/report must be new. Raw windows use `.windows.jsonl`; profiles use
`.warmup`, `.final` or `.failure` suffixes. Do not shorten the workload, widen
timeouts, ignore maintenance failures or rerun until green as qualification.

## Evidence And Next Boundary

Ignored local `.artifacts/kitdb-lifecycle-20261020/` retains the original
binary/source-overlay hashes, raw windows, failed assessment and separate
recovery regression logs. Older clean-candidate reports are unchanged.

Before another qualification attempt, capture the actual blocking owner or
preserve an affected DB for offline verification. A Windows replacement retry
policy, if introduced, requires its own boundedness, permanent-denial,
cancellation and durability review; it is not silently added here.

No new commit/push, Linux run, 24-hour canary, stable promotion or production
handle-leak exemption is part of this follow-up.

## Retained-Fixture Diagnostic Follow-Up

Exactly one new diagnostic ran using the tested failure-retention harness,
the same candidate plus its recorded test-only overlay, and unchanged workload
limits. It is retained separately in
`.artifacts/kitdb-lifecycle-20261020/diagnostic-02/`. No production code, retry
policy, timeout or durability rule was changed.

It completed two warmups and eight measured 30-second windows over 16 databases:
160 independently verified database lifetimes, 231,076 acknowledged transactions
(184,783 in measured windows). Every row/index reopen oracle, ACK/sync count,
kernel drain and manager close passed. The requested workload was 300 seconds;
windows including admitted-operation drain totalled 301.702 seconds. The test
including setup/reopen took 325.40 seconds. Successful fixture trees were
removed; the explicitly created failure-retention root was empty at completion.

Measured-window observations:

- Handles: exactly 247 in all eight samples (initial process sample: 145).
- Goroutines: exactly two in all eight samples and both stack dumps. Only the
  test/profiling goroutine and the test runner remained, not KitDB workers.
- OS threads: 23 for seven measured samples, then 25 in the final sample.
  `process_growth_review_required` therefore remains **true**. Handle/thread
  snapshots are sequential observations, not an atomic attribution of owners.
- Post-GC heap: 611,912..636,856 bytes; final 628,216 bytes. The last-four-sample
  delta was -6,848 bytes, not a monotonically increasing tail.
- RSS: 71,745,536..72,552,448 bytes; final 71,872,512 bytes. This is retained
  process residency, not a KitDB allocation limit or peak-memory guarantee.

The sampled warmup/final heap-profile difference had no retained sampled bytes.
Heap profiling is sampled: this does not rule out changes smaller than its
resolution, and the exact process counters above are not replaced by that
profile result.

The fixture location changed from the original **C: TempDir** to the retained
**D: workspace**. No WAL publication denial occurred in this follow-up, but it
is NOT a same-filesystem reproduction or evidence that the original denial is
fixed. Its blocking owner and failed-window reopen outcome remain unknown.
The original `assessment-failed.json`, binary, source overlay and raw evidence
remain unchanged. This diagnostic supports a bounded observation of flat
handles/goroutines, not a stable label or an unrestricted no-leak claim.

## Thread And Idle Control Experiment

The next test-only experiment ran two fresh, sequential processes from the same
binary: an observation-only control with no DB workload, then native SQL over
16 databases. Each had ten fixed 30-second windows and post-work idle samples at
0/1/5/15/30/60 seconds. The control kept the memory sampler, timers and GC but
did not mimic concurrent DB I/O; it is not a normalized latency comparison.

New observations bracket process sampling with a second native-thread snapshot
and handle count, plus Go thread-profile record counts. Those profile records
are not a current OS-thread count or a handle-ownership inventory.

The control went from 7 to 9 native threads at its last work-window sample,
while Go profile records stayed at 8, handles at 152 and goroutines at two.
These counters persisted through 60 seconds idle. No DB calls, DB fixtures or
ACKs existed in that process.

The DB arm verified 160 database lifetimes and 232,968 ACKs after independent
row/index reopen, with exact sync accounting and complete kernel/manager drain.
Its late native count similarly went from 23 to 25 with Go profile records
unchanged at 24, handles unchanged at 248 and goroutines at two, persisting
through idle. Both native snapshots agreed. An earlier +6 handles coincided
with +1 native thread and +1 Go profile record; no object-type enumeration
proves ownership of those handles.

Thus the two-native-thread pattern also occurs without DB work. It does not
by itself establish a KitDB-specific leak, nor does this identify the exact
native creator or retrospectively explain the older process. No raw review
flag is waived, and the original WAL denial remains unresolved.

All 13 required focused roots passed normal/race and `go vet`. Source/binary
hashes, 34 raw process observations, all ten DB windows and no-overlap execution
were checked. Raw evidence and interpretation are retained separately in
`.artifacts/kitdb-thread-attribution-20261020/`; no engine API or storage format
was changed.

## Same-C:-Volume WAL Diagnostic, 2026-10-21

A single diagnostic returned to the original C: temp parent, using a fresh
exclusive fixture root, the same native SQL workload (2 warmups + 8 measured
windows, 30 seconds each, 16 databases) and unchanged operation/test timeouts.
Its explicitly hashed test-only overlay adds a first-worker-error Windows
resource-user observation and retains failed fixtures for copy-only recovery
and independent ACK-model verification. It does not modify production WAL
publication or introduce automatic retry.

All 10 windows passed: **160 independently reopened database lifetimes,
166,858 ACKs**, exact row/index/sync checks and complete ownership drain.
The test took 349.80 seconds. No WAL denial recurred, so no actual-failure
copy verification or blocking-owner attribution occurred in this campaign.
Sixteen focused roots passed normal/race, and vet passed; those tests cover
the file-user probe, controlled denial, ACK reconstruction and copy verifier.

Measured handles stayed at 252 and goroutines at two. Native threads changed
24 to 26 at the last two measured observations, consistent with the earlier
non-DB-specific pattern but without proving a creator. The old process-growth
review flags remain unchanged. The new wrapper samples after close, before
fixture removal; identical SQL workload does not mean identical full process
history, wrapper observations or external file users.

The original failure remains unresolved, not fixed by a later same-volume
success. Earlier raw evidence, clean candidates and shared staging were
verified unchanged. Full source hashes, raw results, limits and assessment:
`.artifacts/kitdb-wal-forensics-20261021/REPORT.md` and `assessment.json`.
