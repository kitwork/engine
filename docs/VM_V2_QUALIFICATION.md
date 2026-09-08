# VM v2 Release Candidate Qualification

This document records the current VM v2 qualification boundary. It is evidence
for a release decision, not a declaration that a release has shipped.

## Current status

The current worktree is **Windows-qualified as a candidate**. It is not yet a
final release because the qualification was run from a dirty worktree and the
same exact commit has not completed the release campaign on Linux.

Frozen compatibility identity:

- bytecode version: `2`;
- Program encoding version: `1`;
- artifact version: `1`;
- compiler schema: `3`;
- instruction-set checksum:
  `10872c964d1c5c284b8ec4fd1acc429e31568151dc03d17a77d1da76774c7e91`;
- compiler fingerprint:
  `f0c6c9255af80f7d2109348a23f49281ca50935b9bb8e41cbcf4970d7fcd4c07`.

## Evidence from 2026-08-30

- Compiler-to-VM fuzz ran for two minutes after corpus preparation: 5,058,186
  executions, 1,437 final interesting inputs, no failure.
- VM determinism fuzz ran for two minutes after corpus preparation: 3,451,932
  executions, 1,319 final interesting inputs, no divergence between fresh and
  reused execution.
- Both pooled-VM soak tests passed, including oversized verified state release.
- The value-pressure campaign passed 48 measured rounds after six warm-up
  rounds. Its pool returned to `active=0` after 216 acquisitions and releases.
  Heap growth was 7,384 bytes, object growth was 28, goroutine growth was zero,
  and measured heap slope was about 192 bytes per round.
- The production retention campaign replaced generations 12 through 262.
  Post-warm-up heap growth was about 0.10 MiB, slope stayed below 0.40 KiB per
  generation, and goroutine growth was zero.
- The complete Windows release campaign passed all 27 steps in 704,828 ms. It
  included full tests, vet, focused race coverage, restart/recovery, concurrent
  cache publication, KitDB race and hard-crash matrices, a canary smoke run,
  and the 128-iteration replica crash soak.

Local evidence files:

- `.artifacts/vm-v2-rc-windows-release.json`;
- `.artifacts/vm-v2-rc-value-pressure.json`;
- `.artifacts/vm-v2-rc-benchmark.txt`;
- `.artifacts/restart-campaign.json`;
- `.artifacts/cache-contention-campaign.json`.

## Performance baseline

The Windows baseline was measured on an Intel Core i7-11850H with Go 1.26.0,
five runs per benchmark. These values are diagnostic rather than portable
performance promises.

- protected `.safe()` success median: 3,781 ns/op, 1,680 B/op, 10 allocs/op;
- typed application-failure rescue median: 5,169 ns/op, 1,849 B/op,
  25 allocs/op;
- arithmetic dispatch: 0 allocations;
- 100 internal function calls: 4 allocations;
- map/filter/reduce callback chain: 20 allocations;
- `FastReset`: 0 allocations;
- pool acquire/release: 0 allocations.

The runtime test suite enforces bounded headroom for the two new paths: at most
12 allocations for protected `.safe()` success and at most 30 for typed
application-failure rescue.

## Promotion gate

Promote this candidate only after all of the following are true for one commit:

1. Separate or commit the intended engine changes so the repository is clean.
2. Run the Windows release campaign with `--require-clean`.
3. Run the same release campaign on Linux for the exact commit.
4. Confirm both reports contain the same compatibility identity above.
5. Complete the controlled deployment canary described in `docs/RELEASE.md`.

Until then, preserve VM v2 opcodes and compiler schema v3. Fix observed
qualification failures; do not add execution features to make the candidate
look more complete.
