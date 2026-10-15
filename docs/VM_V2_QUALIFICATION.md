# VM v2 Release Candidate Qualification

This document records the current VM v2 qualification boundary. It is evidence
for a release decision, not a declaration that a release has shipped.

## Current status

The current worktree is **Windows-qualified as a candidate**. It is not yet a
final release because the qualification was run from a dirty worktree and the
same exact commit has not completed the release campaign on Linux.

Frozen compatibility identity:

- bytecode version: `3`;
- Program encoding version: `1`;
- artifact version: `1`;
- compiler schema: `9`;
- instruction-set checksum:
  `f98baae0b65716b886b0786a06a9316ae7baf4ff7ab93b8cd87149a344df1678`;
- compiler fingerprint:
  `a7a9f8ffe16e9318e7356a4944f3d92a057da79e0b61b530e136038986c9e2b6`.

The bytecode version went from 2 to 3 on 14/10/2026 by the owner's decision ("đồng ý
hết"): seven wide forms (a three-byte constant index or jump address) and a program of up
to 2 MiB of code (it was 65,535 bytes; apptop.vn's largest route compiled to 65,038), with
1,048,576 constants and a 64 MiB envelope. Bytecode 2's instructions keep their slots,
operands, stack effects and energy, and a program that fits two-byte operands compiles to
the same bytes; the compatibility archive moved to `testdata/v3`. The compiler schema stays
8. The campaign must run again for the bytecode 3 identity; the evidence below was gathered
on bytecode 2.

The compiler schema went from 8 to 9 on 14/10/2026 (afternoon) by the owner's decision
("đồng ý hết"): `x++` used as a value is the old value and `++o.x` the new one, `continue`
works in both loops, and `typeof` compiles to an `INVOKE` the VM answers. At run time,
without an opcode change, a negative number is true and `a[i] = v` past the end pads with
null (a key that is no index fails). No opcode, operand or bytecode version changed. The
campaign must run again for the schema 9 identity.

The compiler schema went from 7 to 8 on 14/10/2026 with the review of schemas 5 to 7:
a template that starts with two values or is one value joins text, a return inside
for…of compiles, an operator, a member read and an array literal record their own
place, and a write to a const, `continue`, `typeof`, `instanceof`, `delete`, `in`,
for…in, the comma operator, `{ a = 1 }` outside a pattern and code nested past 1000
levels are compile errors. At run time, without an opcode change, `a[i] = v` sets
item `i` of an array. No opcode, operand or bytecode version changed. The campaign
must run again for the schema 8 identity.

The compiler schema went from 6 to 7 on 14/10/2026 by the owner's decision
(`safe(() => …)`, "đồng ý hết, làm luôn đi"): `x.safe()` and its lowering are gone
(a compile error that says to write `safe(() => x)`), `safe` is a global function
the VM calls, `safe(<not a function>)` is a compile error, and columns count
characters. At run time, without an opcode change: a failure's code is one of a
closed set, no value carries a failure attached to its data, an HTTP request that
got no answer and a failed SQLite inspector read are failures, `.then`/`.catch`
and the global `Error` are gone, a diagnostic's message no longer holds its place
and its text is written only when read. No opcode, operand or bytecode version
changed. The campaign must run again for the schema 7 identity and these run-time
changes.

The compiler schema went from 5 to 6 on 14/10/2026 by the owner's decision:
`const` and `let` live in their block, as in JavaScript. The compiler resolves
names and gives each variable its own slot, so an inner declaration no longer
overwrites an outer or a parent function's variable, a closure made in a loop
keeps its iteration, and declaring a name twice in one block is a compile error.
No opcode, operand or bytecode version changed and no instruction changed
meaning. The campaign must run again for the schema 6 identity.

The compiler schema went from 4 to 5 on 14/10/2026 by the owner's decision:
`&&` and `||` short-circuit as in JavaScript, `??`, `?.` and wider destructuring
joined the grammar, and CALL/INVOKE record the place of the call. Existing
source changes behaviour only where the right side of `&&`/`||` has an effect or
can fail; no opcode, operand or bytecode version changed. The campaign must run
again for the schema 5 identity.

The compiler schema went from 3 to 4 on 13/10/2026 by the owner's decision:
reserved words became property names, which changed what some already-compiling
source means, so cached artifacts must be rebuilt. No opcode, operand or bytecode
version changed. The evidence below was gathered at schema 3 (fingerprint
`f0c6c9255af80f7d2109348a23f49281ca50935b9bb8e41cbcf4970d7fcd4c07`); the release
campaign must run again for the schema 4 identity.

The same day, also by the owner's decision, an object's own key began to win over
the builtin method of the same name at run time (`value.Get` and the `INVOKE` fast
path). That changes the meaning of existing Programs without touching compiler
output, so neither the schema nor any opcode records it. The campaign must cover
both changes.

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

- `safe(() => …)` success median (14/10/2026, Intel Core i7-11850H, Go 1.26.0, five
  runs): 1,338 ns/op, 2,240 B/op, 8 allocs/op; the protected `.safe()` it replaced
  measured 3,781 ns/op, 1,680 B/op, 10 allocs/op in the 2026-08-30 baseline;
- typed application-failure rescue by `safe` median (same run): 2,308 ns/op,
  2,820 B/op, 21 allocs/op, the diagnostic's text never written (31 allocs when it
  was written at once); `.safe()` measured 5,169 ns/op, 1,849 B/op, 25 allocs/op;
- arithmetic dispatch: 0 allocations;
- 100 internal function calls: 4 allocations;
- map/filter/reduce callback chain: 20 allocations;
- `FastReset`: 0 allocations;
- pool acquire/release: 0 allocations.

The runtime test suite enforces bounded headroom for the two paths: at most 12
allocations for `safe(() => …)` success and at most 30 for typed
application-failure rescue.

## Promotion gate

Promote this candidate only after all of the following are true for one commit:

1. Separate or commit the intended engine changes so the repository is clean.
2. Run the Windows release campaign with `--require-clean`.
3. Run the same release campaign on Linux for the exact commit.
4. Confirm both reports contain the same compatibility identity above.
5. Complete the controlled deployment canary described in `docs/RELEASE.md`.

Until then, preserve VM v2 opcodes and compiler schema v9. Fix observed
qualification failures; do not add execution features to make the candidate
look more complete.
