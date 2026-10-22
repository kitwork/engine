# SQL Cancellation Barrier Follow-up

## Finding

The original campaign allowed its worker to leave the termination barrier on
either `proceed` or `batchCtx.Done()`. That does not establish that `cancel()`
has returned. In the inspected Go 1.26 implementation, cancellation publishes
the parent's Done channel before visiting its children. The worker could call
`sql.Tx.Commit()` while cancellation had not reached the transaction's child
context. This is a synchronization/order race, not necessarily a data race.

The deterministic test holds child cancellation delivery between those phases.
With the old Done-only rule, Commit can return nil and its row survives both an
independent reader and reopening. With a completed-cancellation barrier, Commit
is rejected and both readers observe zero rows. No sleeps, kernel hooks, relaxed
assertions or production behavior changes are used to create this evidence.

This identifies an unsound barrier in the test. It does not reconstruct the
unrecorded WAL/ledger outcome of the earlier failed snapshot, nor promise that
cancellation can undo a commit already admitted to the durability writer.

## Fix

- The campaign waits for `proceed`, closed only after cancellation returns or
  KILL SESSION removal is observed. Its outer test deadline still bounds waits.
- Each outcome records adapter, context errors before/after Commit, commit error
  and monotonically ordered termination/commit markers.
- A missing commit error still fails the test. Oracle mismatches are recorded
  without stopping before the node is closed and independently reopened.
- Fresh native observer connections inspect the totals and discarded rows after
  each canceled/killed batch. Reopen checks repeat the observation and preserve
  existing row/index, role and resource-drain assertions.
- `TestSQLCancellationCompletionBarrier` covers both the in-flight cancellation
  window and the completed cancellation boundary with independent durable data
  oracles.

## Exact Snapshot

Local evidence: `.artifacts/kitdb-candidate-20261020-r2/`.

- Baseline: `e89bd9824396a2684c7669a029e94d9b526f1910`.
- Manifest: 11,849 files, 61 overlaid files; SHA-256
  `0b77907b889c8537c06dba164184bc786e55a898b2931c7e9863437122c6e770`.
- Compared with retained r1, the only source-manifest differences are the
  campaign test, new controlled regression test, benchmark README and the
  earlier consolidation report. All production source is unchanged.
- This follow-up report is written after freezing, not part of that manifest.

## Focused Evidence

Structured `go test -json` events were checked for exact named PASS counts and
absence of failures/skips, not just command exit status:

- Normal: 20 repetitions of both top-level tests, 34.475 seconds.
- Race: 5 repetitions of both top-level tests, 86.188 seconds.
- 50 ordered campaign termination traces, each with a non-nil completion error.
- 300 exact ledger observations across independent connections and reopened DBs.
- 25 controlled in-flight-cancel commits independently persisted/reopened with
  one row, and 25 completed-cancel cases independently persisted/reopened with
  zero rows.
- Full source manifest checks matched before and after the probes.

See `probes-validated.json`, `probe-execution.json`, `source-delta.json` and the
two JSONL event streams for the exact local records. The previous failed gate
and its 110 normal / 20 race passing reruns remain untouched in r1.

## Qualification

The unchanged Windows `kitdb-release` gate passed **30/30 steps** in
2,032.084 seconds (33 minutes 52.084 seconds). No steps failed or remained
unrun. This includes the full KitDB package suite that failed in r1, the host
adapter suite, compatibility, operator build/vet, authorization normal/race,
kernel/search/adapter race coverage, hard-process crash matrices, standalone
commerce, a short key/value canary and the seeded 128-iteration replica crash
soak. The complete plan and actual step results remain in `release.json`.

All 11,849 source hashes and the unignored file set matched after the gate.
The two changed test files in the shared checkout also match the tested r2
bytes. `qualification.json` records a passed Windows development gate and
retains the hashes of the raw gate and focused evidence.

This is still a dirty-source snapshot, not same-clean-commit release evidence.
The original r1 failure remains retained, not relabeled as passing. The short
canary is kernel key/value evidence, not a 24-hour SQL/pgwire qualification.
No commit/push, Linux test, keep-awake change or 24-hour canary was made;
`clean_commit_qualified` and `stable_qualified` remain false.
