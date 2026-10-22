# KitDB Windows Candidate Consolidation

## Status

**Not qualified.** The complete Windows release plan stopped at step 6. Focused
passing reruns do not replace that failure or qualify the remaining steps.
No production behavior was changed as a speculative cancellation workaround.

## Exact Source

- Baseline: `e89bd9824396a2684c7669a029e94d9b526f1910`.
- Frozen overlay: 58 files (37 KitDB, 18 benchmark, 3 timing-helper files).
- Full manifest: 11,847 files; SHA-256
  `89d30cfd78a007f76f2e5a628b5b1fe82e69d5456185626c96a117cf012a8092`.
- Local evidence: `.artifacts/kitdb-candidate-20261020-r1/`.
- Source: an independent local Git clone plus allowlisted uncommitted files.
  Compiler/UI/JIT/host changes from the shared checkout were excluded, not
  reverted. No new commit, branch, dependency, push or CI dispatch was made.

The Git hash is the baseline, **not** the identity of these optimizations.
`source-manifest.json` and `snapshot.json` identify their complete source.
All source hashes and the unignored file set matched after the release gate.
The separate report and this README link were written after freezing and are
not part of that snapshot.

## Review

The focused review checked overlay value ownership, public-versus-borrowed scan
buffers, snapshot admission, callback close/reentrancy, checkpoint scratch-page
ownership, newest tombstone precedence, range fences, CRC error propagation,
primary-suffix ordering and diagnostic build-tag boundaries. Compatibility
versions, archived fixtures, WAL publication order and durability thresholds
were not changed. See the local `AUDIT.md` for the boundaries and residual risks.

Allocation improvements are not peak-RSS guarantees. Earlier slower mixed
workload outcomes remain part of the evidence; this consolidation adds no
universal throughput or latency claim.

## Execution

The unchanged `kitdb-release` plan ran with `GOMAXPROCS=4`, `GOPROXY=off`,
`GOWORK=off` and `GOFLAGS=-mod=readonly`. It intentionally omitted
`--require-clean` and reported a dirty development snapshot, not a clean
committed release. Its overall timeout remained 90 minutes. No keep-awake or
power-plan changes were made.

- Five steps passed: compatibility, authorization, restricted-write campaign,
  standalone authorization crash and SQL authorization canary.
- Step 6, the full kernel/node package suite, failed in relational
  `TestRestrictedWriteCampaignSeeded` with
  `cancel batch committed after termination`.
- The remaining 24 steps were explicitly `not_run`.
- Same-source focused reruns passed 10 repetitions (14.991 seconds) and then
  100 repetitions (127.570 seconds), including ledger/index/reopen checks.
- A separate 20-repeat race diagnostic passed (273.698 seconds), with no data
  race report. Its log and outcome are retained in `restricted-race-20.log`
  and the local `qualification.json`, independently from normal reruns.

The failure observed an unexpected nil completion error after cancellation.
The failing test stopped before its final ledger/reopen oracle. Consequently
the cause, and whether that unexpected result corresponded to a durable write,
remain unresolved. Neither an engine defect nor a test-only race is established.

## Next Boundary

Reproduce the full-suite cancellation outcome with explicit context/commit
barrier and independent ledger evidence before selecting a fix. Preserve this
failed snapshot and avoid retry-until-green qualification, skipped assertions,
arbitrary sleeps or changed durability semantics.

Post-gate storage/SQL soaks were not started. Linux, a clean same-commit release
gate, 24-hour canary, deployment-filesystem drills and hardware power-loss
qualification remain absent. This snapshot must not promote KitDB to stable.
