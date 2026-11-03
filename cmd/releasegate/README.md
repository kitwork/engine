# Release gate

Run from the engine directory. All gates run locally on the current OS; they do
not push commits or schedule CI. `--require-clean` is for a committed candidate;
omit it for development, where the JSON explicitly records a dirty worktree.

```text
go run ./cmd/releasegate --mode kitdb-read --report .artifacts/kitdb-read.json --timeout 15m
go run ./cmd/releasegate --mode kitdb-lifecycle --report .artifacts/kitdb-lifecycle.json --timeout 10m
go run ./cmd/releasegate --mode kitdb-auth --report .artifacts/kitdb-auth.json --timeout 20m
go run ./cmd/releasegate --mode kitdb-verify --report .artifacts/kitdb-verify.json --timeout 45m
go run ./cmd/releasegate --mode kitdb-release --require-clean --report .artifacts/kitdb-release.json --timeout 90m
```

`kitdb-auth` is a focused eight-step development gate, not full KitDB qualification:
account-incarnation/admin protection, local-role lifecycle and portable mapping,
native/pgwire/HTTP KitSQL permissions including INSERT SELECT and ON CONFLICT,
seeded concurrent writes, standalone process-crash recovery, and the managed SQL
authorization canary. Each has a
normal step and a race step. The crash race step instruments the parent **and**
fresh server executable. The full verify profiles include the four normal
steps; release profiles additionally include the four race steps. The canary
requires allowed/revoked/restored adapter progress, automatic checkpoint coverage
and closed/reopened ownership; see `kitdb/kitsql/SQL_CANARY_AUTH.md`.

Existing storage, backup/restore, projection, search, operator and compatibility
gates remain in the broader profiles. See `kitdb/RELEASE_1_0.md` for their scope.

## Prepared Read Regression

`kitdb-read` runs three short required-evidence steps: normal, race, and a
`kitdb_profile` build. It covers primary-to-snapshot handoff after an interleaved
commit, row-generation changes without schema changes, cancellation at both
physical reads and the fence, read-error refusal, real truncated-file reads,
invalid reservation cleanup and allocation envelopes. All fixtures are disposable.
It also covers forward secondary-index buffer ownership across pages, warm and
disabled KitDB page caches, LIMIT/OFFSET/reverse reads, cancellation, old snapshots
with own writes, phantom/range conflicts and concurrent checkpoints. Both cache
policy subcases must pass. A disabled KitDB cache is not a cold OS cache.
Reader fault scripts live only in tests; the truncation test restores the exact
fixture bytes and verifies the owner before cleanup. This is not a power-loss
or physical-disk-failure simulation.

The commit handoff fills the other 31 snapshot slots: fallback must consume its
existing reservation rather than request another. The profile step separately
requires one reservation and zero/one capture for ordinary primary/non-primary
queries. Each root, each scripted handoff point, both allocation cases and both
profile reservation cases must emit PASS. Missing, skipped or failed evidence
rejects a step even if a process otherwise exits zero.

Allocation checks use the same immutable 128-row fixture and SQL on each side:
prepared primary is capped at 160 objects and must save at least eight against
a one-shot explicit snapshot; secondary equality is capped at 460 and may not
exceed its snapshot control by more than four. These are regression envelopes,
not new runtime memory limits or timing promises. There is no ns/op or p99 CI
threshold. The formerly rejected fallback's 55 extra objects fails this check.
The separate 1,024-row ordered secondary fixture is capped at 460/480 objects
for warm/disabled KitDB cache, including room for race instrumentation. The
baseline cursor needs about 435/451 normal objects on the same SQL and fixture.
The experimental 425/444 envelopes were withdrawn with copy elimination after
incomplete Windows mixed-tail qualification; ownership/result/cache checks
remain mandatory. `KITDB_SECONDARY_TAIL` is forced off in qualification plans.

`verify` and `kitdb-verify` include normal/profile steps; release profiles add
race. The focused mode runs all three and never starts a timed benchmark,
canary, listener service or manual diagnostic campaign.

## Lifecycle And WAL Regression

`kitdb-lifecycle` is a short two-step development gate: normal and race execution
of the same required lifecycle, ACK-model and copy/reopen tests. Windows also
requires both real no-delete-share WAL denial subcases: close/reopen without
another checkpoint, and an explicit successful checkpoint after releasing the
blocker. Publication failure must remain visible, subsequent ACKs must survive,
and kernel/node resources must drain. Other platforms omit these Windows-only
requirements, rather than counting a skipped Windows test as evidence.
Windows additionally requires the controlled checkpoint-forensic capture and
copy-only ACK verification. That short control queries Restart Manager for a
known test handle; it never shuts down or restarts a resource user.
The Windows normal/race steps also select `./kitdb` and require native
`open-source` / `replace-target` denial subcases, path-resolution and
unsupported-fallback refusal classification. Both packages have exact PASS
requirements (17 entries per Windows step); missing native-package tests cannot
silently pass. Tagged before-cleanup capture and sync/crash controls are
documented in [`kitdb/FAULT_LAB.md`](../../kitdb/FAULT_LAB.md#windows-wal-publication-diagnostics),
not silently enabled in ordinary lifecycle gates.

`verify` and `kitdb-verify` include the normal step; `release` and `kitdb-release`
also include its race step. Each selected root and both Windows recovery
subcases must emit PASS through the existing JSON evidence observer. Missing or
skipped requirements fail the step even if `go test` exits zero.

Every plan explicitly sets `KITDB_LIFECYCLE_CAMPAIGN`, `KITDB_MIXED_CAMPAIGN`,
`KITDB_THREAD_ATTRIBUTION`, `KITDB_WAL_FORENSIC`, `KITDB_WAL_LOCK_CAPTURE` and
`KITDB_WAL_CHECKPOINT_FORENSIC` to
`0`, including broad package suites. Inherited shell opt-ins cannot silently
start a long diagnostic or access a retained fixture path. The fixed Windows
capture control above remains part of the gate. These flags are recorded in
planned and executed environments. Other
reviewed gate-owned opt-ins, such as the authorization crash matrix, remain
unchanged. Run manual diagnostics directly as documented in
[`kitdb/relational/LIFECYCLE.md`](../../kitdb/relational/LIFECYCLE.md).

This gate is not a production fix for an unexplained Windows denial, a clean
candidate, a no-leak certification, a 24-hour soak or a stable release decision.

## Evidence Contract (Schema 3)

- OS, architecture, Go version, starting Git commit and dirty state are recorded.
  A dirty worktree report is not clean-candidate evidence.
- A step has `passed`, `failed`, `skipped`, `not_run` or `planned` status, in
  addition to the compatibility `success` boolean. Dry-run plans have
  `status=planned`, `dry_run=true`, `success=false`; the command still exits zero
  when the plan is valid. A plan is not a successful execution.
- The gate stops on first failure. Every remaining step is explicitly `not_run`,
  not silently absent from JSON. A clean-tree refusal lists all steps as unrun.
- Mandatory authorization/campaign steps stream `go test -json`. Each named
  required test must emit a PASS in its expected package. A missing test, skipped
  test or skipped descendant rejects the step even when `go test` exits zero.
  A nonzero command exit always fails, even if required tests previously passed.
- `required_tests` preserves bounded per-test pass/fail/skip counts. The
  `skipped_tests` count is available for JSON-streamed steps; legacy non-JSON
  steps retain command-exit semantics, not individual test-skip evidence.
- The standalone crash step deletes its designated previous artifact before
  running. The fresh schema-1 crash JSON must match platform, child race mode,
  SHA256 format and all six successful case names. Its binary hash and case
  results are embedded in the enclosing step, so later runs cannot silently
  replace the report's copy. Missing, oversized, trailing or malformed evidence
  fails. Test events and a sidecar are local evidence, not cryptographic signing.

The mandatory crash opt-ins are supplied by the plan, not the caller's shell.
Normal and race crash evidence have separate paths. The general operator suite
explicitly disables the opt-in to avoid accidentally repeating it through an
inherited environment. See `kitdb/managed/PROCESS_CRASH.md` for exact fault
boundaries and unknown-commit reconciliation limits.

Windows execution needs atomic database renames and loopback listeners. A
restricted sandbox denying these operations is not a successful workload run.
Process termination evidence does not qualify physical power loss, dishonest
fsync, other operating systems or distributed exactly-once behavior.
