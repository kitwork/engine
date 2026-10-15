# Release gate

Run from the engine directory. All gates run locally on the current OS; they do
not push commits or schedule CI. `--require-clean` is for a committed candidate;
omit it for development, where the JSON explicitly records a dirty worktree.

```text
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
