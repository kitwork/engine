# VM v2 Compatibility Freeze

Kitwork VM v2 has an explicit, mechanically enforced compatibility boundary.
This freeze does not mean the engine can no longer improve. It means a change
cannot silently reinterpret already compiled code or move a numeric opcode.

## Frozen tuple

- bytecode version: `2`;
- Program binary envelope: `1`;
- bytecode artifact envelope: `1`;
- compiler schema: `2`;
- instruction-set checksum:
  `10872c964d1c5c284b8ec4fd1acc429e31568151dc03d17a77d1da76774c7e91`;
- compiler fingerprint:
  `aa8250eda7fb056436c15a279e12f37a3e0b25d5597f3ad8059a6bfc120627e4`.

`runtime.TestVMV2Contract` freezes every opcode number, including the retired
`_RESERVED` slot, and the complete instruction metadata checksum.
`compiler.TestCompilerV2GoldenContract` freezes representative arithmetic,
closure/callback, bounded-loop, and switch/fallthrough Programs and validates
their artifact round trips. Compiler schema v2 added `switch`, `case`,
`default`, and scoped `break` by lowering them to existing VM v2 instructions;
it did not add or renumber an opcode.
`compiler.TestCompilerV2ArtifactDeterminismAcrossColdProcesses` compiles the
same corpus in six fresh OS processes and requires identical Program checksums,
artifact hashes, source fingerprints, cache keys, and encoded sizes. This
catches nondeterministic emitter behavior that one warm process can hide.
`compatibility.TestVMV2CompatibilityArchive` does not compile source. It loads
committed VM v2 Program binaries, verifies their source and binary hashes,
decodes and verifies them, executes their recorded semantics, and requires a
byte-identical re-encode. A second test repeats execution across reused and
pooled VMs. Together these tests prove that today's runtime still understands
Programs produced by the frozen compiler tuple.

## What is compatible

A change may stay within the current tuple when all existing verified Programs
keep their meaning, rejected malformed Programs remain rejected, and the
compiler goldens remain byte-for-byte deterministic. Internal allocation,
pooling, diagnostics, and dispatch improvements may change without a version
bump when those conditions hold.

## What requires a version decision

- Moving, removing, or reusing an opcode slot requires a bytecode version bump.
- Changing operand width, stack effect, energy, or verifier interpretation
  requires a bytecode version decision.
- Changing the Program binary framing requires a Program encoding version bump.
- Changing the artifact framing requires an artifact version bump.
- Intentionally changing compiler output requires a compiler schema bump, even
  when the resulting Program has equivalent behavior.

The smallest owning boundary should be bumped. A change must add compatibility
and rejection tests before the frozen values are updated. Reserved numeric
slots are never recycled inside VM v2.

Runtime resource ceilings are operational policy rather than opcode semantics,
so changing one does not automatically require a VM version bump. It does
require a reviewed `runtime.Limits()` change, exact boundary tests, fault
gauntlet evidence where applicable, and an updated release report. A limit must
never drift through a second hard-coded value outside `runtime/limits.go`.

## Required gate

Run the canonical verification before merging a VM or compiler change:

```text
go run ./cmd/releasegate --mode verify --report .artifacts/verify.json
```

Before naming a release candidate, run the release campaign on both Windows and
Linux:

```text
go run ./cmd/releasegate --mode release --require-clean --report .artifacts/release.json
```

The release gate begins with the immutable VM v2 compatibility archive, then
runs the manifest-driven VM fault gauntlet, language conformance and inspector
contracts, VM and compiler contracts, build, tests, vet, focused race coverage,
compiler-to-VM fuzzing, determinism fuzzing, the pooled-VM soak, and the memory
retention campaign. The complete release and canary procedure lives in
`docs/RELEASE.md`.
