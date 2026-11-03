# Release gate

Run from the engine directory. All gates run locally on the current OS; they do
not push commits or schedule CI. `--require-clean` is for a committed candidate;
omit it for development, where the JSON explicitly records a dirty worktree.

```text
go run ./cmd/releasegate --mode verify --report .artifacts/verify.json
go run ./cmd/releasegate --mode release --require-clean --report .artifacts/release.json --timeout 90m
```

`verify` runs the VM compatibility archive, the VM fault gauntlet, the language,
inspector, VM and compiler contracts, diagnostics and lifecycle, then build,
full tests and vet. `release` adds focused race coverage, the compiler and VM
fuzz smokes, and the VM pool, value-pressure, memory-retention,
restart/recovery and cache-contention campaigns. See `docs/RELEASE.md`.

The KitDB gates (`kitdb-verify`, `kitdb-release`, `kitdb-auth`,
`kitdb-lifecycle`, `kitdb-read`) and the crash evidence only they produced went
with KROW on 03/11/2026 (`docs/KITWORK_KITDB.md` câu 13.4 in the root
repository).

## Evidence Contract (Schema 4)

- OS, architecture, Go version, starting Git commit and dirty state are recorded.
  A dirty worktree report is not clean-candidate evidence.
- A step has `passed`, `failed`, `skipped`, `not_run` or `planned` status, in
  addition to the compatibility `success` boolean. Dry-run plans have
  `status=planned`, `dry_run=true`, `success=false`; the command still exits zero
  when the plan is valid. A plan is not a successful execution.
- The gate stops on first failure. Every remaining step is explicitly `not_run`,
  not silently absent from JSON. A clean-tree refusal lists all steps as unrun.
- A step that names required tests streams `go test -json`. Each named required
  test must emit a PASS in its expected package. A missing test, skipped test or
  skipped descendant rejects the step even when `go test` exits zero. A nonzero
  command exit always fails, even if required tests previously passed.
- `required_tests` preserves bounded per-test pass/fail/skip counts. The
  `skipped_tests` count is available for JSON-streamed steps; non-JSON steps
  retain command-exit semantics, not individual test-skip evidence.
- Schema 4 (03/11/2026) drops the KitDB compatibility profiles from the
  compatibility tuple and a step's process-crash evidence.
