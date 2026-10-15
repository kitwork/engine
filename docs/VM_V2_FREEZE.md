# VM v2 Compatibility Freeze

Kitwork VM v2 has an explicit, mechanically enforced compatibility boundary.
This freeze does not mean the engine can no longer improve. It means a change
cannot silently reinterpret already compiled code or move a numeric opcode.

## Frozen tuple

- bytecode version: `3` (`2` until 14/10/2026, see "Bytecode 3" below);
- Program binary envelope: `1`;
- bytecode artifact envelope: `1`;
- compiler schema: `9` (`8` until the afternoon of 14/10/2026, see "Schema 9" below);
- instruction-set checksum:
  `f98baae0b65716b886b0786a06a9316ae7baf4ff7ab93b8cd87149a344df1678`
  (bytecode 2: `10872c964d1c5c284b8ec4fd1acc429e31568151dc03d17a77d1da76774c7e91`);
- compiler fingerprint:
  `a7a9f8ffe16e9318e7356a4944f3d92a057da79e0b61b530e136038986c9e2b6`
  (schema 8 on bytecode 3: `5fbf4f10a82a5a6d4a7f3c1f2a5c727ccef765a7947729a5b2430e0a8964db55`;
  schema 8 on bytecode 2: `be7e49394fb709e1b0adeba3731263930c05631b609763361a95ccd89a276ef9`).

`runtime.TestVMContract` (`TestVMV2Contract` until bytecode 3) freezes every opcode
number, including the retired `_RESERVED` slot and the seven wide forms, the
complete instruction metadata checksum, and that the slots bytecode 2 had still hash,
with version 2, to bytecode 2's checksum.
`compiler.TestCompilerV9GoldenContract` (`TestCompilerV8GoldenContract` until schema 9) freezes representative arithmetic,
closure/callback, bounded-loop, switch/fallthrough, `safe(() => …)` (a success
and a failure), reserved words as keys, short-circuit/null-test, destructuring,
block-scope, closure-per-iteration, template-text and update/continue/typeof Programs and validates their artifact round
trips. Compiler
schema v2 added `switch`, `case`, `default`, and scoped `break`; schema v3 added protected `.safe()` evaluation;
schema v4 (13/10/2026) made reserved words property names, which changed what
some already-compiling source means (`o.undefined` names the key `"undefined"`,
no longer `"null"`; `{ true: x }` emits its key as a string constant), so every cached
artifact is rebuilt once. Schema v5 (14/10/2026) made `&&` and `||` run their
right side only when the left does not decide (DUP/TRUE/FALSE/JUMP; AND/OR stay
for a constant or a name on the right, and a condition branches on them), added
`??`, `?.` and renaming/default/nested destructuring, and moved the debug place
of CALL/INVOKE to the call (the callee's start, the dot) from its last argument:
of 863 compiling sources under apps, docs/examples, native and the engine's own
samples, 415 changed code (the `&&`/`||` right side) and 412 only their debug
table, and with those two changes undone all 863 compile byte-identically to
schema 4. All four lower to the existing VM v2 instruction set and none added or
renumbered an opcode. Schema 6 (14/10/2026) gives `const` and `let` the block
that declares them: the compiler resolves every name before it emits code and
names each variable's slot (`name#N` for a block's, `name@N` for a function's own
declaration whose name a function around may hold, `#N` for the object a loop
makes per iteration when a closure reaches its variables, `compiler/scope.go`),
so an inner declaration no longer overwrites an outer variable or a parent
function's, and a closure made in a loop keeps its iteration. Declaring a name
twice in one block is a compile error. LOAD, STORE and every other instruction
keep their meaning; `runtime.SlotName` reads a slot back for messages and
`ExecuteLambdaScope`. Of the 863 compiling sources, 465 changed only constants
(slot names, and the indices after them), 398 are byte-identical, and every one
keeps its instruction stream up to slot names: no app makes a closure in a loop
that reads a variable of the iteration, so none gains an instruction. Schema 7
(14/10/2026) removes `x.safe()`: its lowering (the receiver compiled into a hidden
evaluator lambda named `\x00kitwork.safe`, then `INVOKE "safe"` on it, which the VM
recognised) is gone, and `x.safe()` with no argument is a compile error that says to
write `safe(() => x)`. `safe` is a global function the VM calls like any other
(`CALL`; `runtime/safe.go`), so the new spelling needs no lowering at all;
`safe(<a call, a literal, an object, an array, arithmetic>)` and `safe` with other
than one argument are compile errors when the bundle does not declare `safe` itself.
Columns of the debug table and of compile errors count characters, not bytes, so the
table of a line with Vietnamese text before a place moved. A schema-6 artifact still
holds the evaluator lambda, which nothing runs any more: the bump retires it
(`compiler.TestSchema7RetiresSchema6Artifacts`). No opcode or operand changed. Schema 8
(14/10/2026, the review of schemas 5 to 7) changes the lowering of a template that
starts with two values or is one value (it starts from "" and joins text: `${a}${b}`
of 1 and 2 is "12", `${5}` is "5"; they added and stayed a number) and of a return
inside for…of (it drops the loop's iterable and index first; the verifier refused it),
and records the place of an operator, a member read and an array literal (its opening
bracket), so the debug table moved. A write to a const, `continue`, `typeof`,
`instanceof`, `delete`, `in`, for…in, the comma operator, `{ a = 1 }` outside a pattern
and code nested past 1000 levels are compile errors; `let y;` and a block standing alone
compile. Of the 865 compiling sources, none changed its instructions or constants and
604 changed only the debug table. A schema-7 artifact holds a template compiled as a
sum: the bump retires it. No opcode or operand changed. Schema 9 (14/10/2026, the
owner's decision, "đồng ý hết") lowers `x++` used as a value to read, update, `POP` (the
old value; `++o.x` updates, drops the object `SET` leaves and reads again), compiles
`continue` to a `JUMP` to the counting for's update or for…of's `ITER`, and `typeof x` to
`x; PUSH "#typeof"; INVOKE 0`, which the VM answers (`runtime.TypeofMethod`); a `SET` of
`o.x = v` or `o[k] = v` records its dot or bracket. `x++` or `++x` as a statement or a for's
update compiles as before. Of the 1,114 compiling sources under apps and docs/examples,
none changed its instructions or constants and 233 changed only the debug table (the
places of `SET`); with those two lines undone all 1,114 compile identically to schema 8,
debug table included. A schema-8 artifact gives `x++` the new value: the bump retires it.
No opcode or operand changed. Schema 3 artifacts carry the fingerprint
`f0c6c9255af80f7d2109348a23f49281ca50935b9bb8e41cbcf4970d7fcd4c07`;
`compiler.TestFileCacheRefusesArtifactsOfSchema3` proves the cache refuses and
replaces them.
`compiler.TestCompilerV4ArtifactDeterminismAcrossColdProcesses` compiles the
same corpus in six fresh OS processes and requires identical Program checksums,
artifact hashes, source fingerprints, cache keys, and encoded sizes. This
catches nondeterministic emitter behavior that one warm process can hide.
`compatibility.TestVMCompatibilityArchive` does not compile source. It loads the
committed bytecode 3 Program binaries of `compatibility/testdata/v3` (compiler schema 8,
the hand-authored cases of `testdata/v2`, whose bytecode 2 binaries the current runtime
refuses by their version and keeps as evidence), verifies their source and binary hashes,
decodes and verifies them, executes their recorded semantics, and requires a
byte-identical re-encode. A second test repeats execution across reused and
pooled VMs. Together these tests prove that today's runtime still understands
Programs produced before the current compiler schema.

### Bytecode 3 (14/10/2026)

The owner's decision of 14/10/2026 ("đồng ý hết") raised the size of one program. A
jump's address was two bytes, so a program could hold 65,535 bytes of code, and
apptop.vn's largest route (`app/{host[string]}/router.kitwork.js`) compiled to 65,038.
Bytecode 3 appends seven wide forms after `_RESERVED` (`PUSH_WIDE` 33, `LOAD_WIDE`,
`STORE_WIDE`, `JUMP_WIDE`, `TRUE_WIDE`, `FALSE_WIDE`, `ITER_WIDE` 39; `_LIMIT` is 40):
each is its instruction with a three-byte operand, the same stack effect and energy.
The compiler writes them only where the narrow form cannot do (a constant index past
65,535; every jump of a program once one jump's address passes 65,535), and the verifier
refuses them elsewhere, so a program that fits is the same bytes as in bytecode 2. The
limits are 2 MiB of code, 1,048,576 constants and a 64 MiB envelope (`docs/BYTECODE.md`,
"Wide forms"). No bytecode 2 opcode, operand, stack effect, energy or meaning changed,
and the compiler schema stays 8: its fingerprint already hashes the bytecode version and
the instruction-set checksum, so every cached artifact is compiled again. Of the 1,114
sources under apps and docs/examples that compiled before, every one compiles to the same
instructions, constants and debug table; only the version and the checksums moved. The
qualification campaign must run again for the bytecode 3 identity.

## What is compatible

A change may stay within the current tuple when all existing verified Programs
keep their meaning, rejected malformed Programs remain rejected, and the
compiler goldens remain byte-for-byte deterministic. Internal allocation,
pooling, diagnostics, and dispatch improvements may change without a version
bump when those conditions hold.

One recorded exception: on 13/10/2026, by the owner's decision, an object's own
key wins over the builtin method of the same name at run time (`{ text: "hi" }.text`
is `"hi"`, `{ delete: 3 }.delete` is `3`; an object without the key keeps the
builtin). Existing Programs that read such a key change meaning. The change lives
in `value.Get` and the `INVOKE` fast path, not in compiler output, so the schema
bump to 4 does not cover it; it is accepted inside VM v2 with no opcode or schema
change, and the qualification campaign must run again for it as well as for schema 4.
Two owner decisions the same evening refine it, also at run time only. A call
reaches an own key only when the key holds a function (`value.CallsOwn`, used by
the `INVOKE` fast path and `value.Invoke`): `{ greet: f }.greet()` calls `f`, while
an own key holding data (anything `request.json()`, `JSON.parse` or a row can
carry) leaves `body.pick(…)`, `has`, `keys`, `text`, `json` and the rest to the
builtin, so data cannot replace a builtin call. And on a plain object (no error
attached: `ErrorVal` nil, `IsError` false) an own `error`, `length` or `isError`
key is read as data, so `{ ok: false, error: "x" }.error` is `"x"` where it was
`null`; a value carrying an error kept the engine's answers until 14/10/2026, when
no value carries one any more and the own key always wins. Through a bracket,
`o["pick"](…)` is still compiled as `GET` then `CALL`, which drops the object, so
data called that way is never the data; reaching the builtin there would need a
lowering change and a schema bump. `x.safe()` with no argument was the safe
evaluator whatever `x` owned, since the compiler lowered it; since schema 7 it is a
compile error, and `o["safe"]()` or `o.safe(arg)` reach an own function `safe`.
On 14/10/2026 the owner decided the two points this left open, again at run time
only ("vâng làm cả hai đi"). Calling a string never prints: `vm.call` printed the
arguments of a `CALL` whose value was the string `log` or `PRINT` to the server's
standard output, so `body["x"](secret)` with `body.x` = `"log"` wrote a secret into
the log; the branch is gone, and no real logging used it (`console.log` is a native
function, `log.print` a capability method, a bare `log(…)` names nothing). And
calling a value that is not a function is a runtime error, as JavaScript's "x is not
a function": a `CALL` of a non-function, and `value.Invoke` of a name that is
neither a function the value owns, nor a builtin of its kind, nor a Go method of a
host value (`INVOKE` and templates), give a `RUNTIME_ERROR` diagnostic whose cause
is `NOT_A_FUNCTION` and whose message names the call as the source wrote it and the
kind of value it is (`o.x không phải hàm (là số): không gọi được`; never a string's text
or, since the safe run of the same day, a number), the diagnostic's place being where the
call starts (printed first: `router.kitwork.js:12:5: …`). The name is read back from the verified bytecode on the first such failure
of a Program (`runtime/callee.go`); a call that succeeds pays only a kind check.
Programs that called data change meaning: `{ x: 3 }.x()` gave `3` and `o["x"]()`
gave `null`, both now fail; a method of `null` stays `null`, and a builtin called
past own data of its name stays the builtin. An array method's callback that is
not a function is the same error, naming the argument (`items.forEach(…): đối số 1
không phải hàm …`), and a function of the engine as the callback is now called, as
in JavaScript (`[1, 0, 2].some(Boolean)` gave `null`); `env.X()` other than
`env.require` is the error (the proxy gave `null`). No opcode, operand, verifier rule or compiler output
changes (the 1140 sources under apps and docs/examples compile to byte-identical
artifacts before and after), so `CompilerSchemaVersion` stays 4, and the
qualification campaign must run again for this as well.
On 14/10/2026 the owner approved the design of `safe(() => …)` (`docs/KITWORK_LANG.md`,
"Lỗi: safe(() => …)"). Besides schema 7 above, it changes run-time meaning without an
opcode, operand or verifier change, so it is accepted inside VM v2 and the campaign must
run again for it: a failure's code is one of a closed set (`value.ClosedCode`; `fail` is
always `FAIL`, `KITDB_TRANSACTION_CONFLICT` is `CONFLICT`, `TYPE_ERROR` is `TYPE`); a value
no longer carries a failure attached to its data (`database.entity()` failing ends the run
with `DATABASE`, `createMany` stopping midway with `PARTIAL` and its progress), an HTTP
request that got no answer fails its `COMMIT` or read (`NETWORK`, `TIMEOUT`, `DENIED`)
through `CommitResult.Failure`, and a failed SQLite inspector read is a failure; `.then`,
`.catch` and the global `Error` are gone; a host failure while the request is cancelled is
`EXECUTION_CANCELLED`; a predicate lambda of the query builder that fails is the query's
failure; a diagnostic's message no longer holds its place (its `Error()` writes
`file:line:column: message`), and its text is formatted only when read. The review of
14/10/2026 adds, in the same way: `a[i] = v` sets item `i` of an array (`SET` appended
`v` whatever `i` was; an index past the end still appends), and `safe(f)` of a host
function keeps a failure nothing catches as `safe(() => f())` does. Schema 9's day adds, in
the same way: a negative number, time or duration is true (`value.Truthy`, which `TRUE`,
`FALSE`, `AND`, `OR`, `NOT` and the array callbacks read; only 0, -0 and NaN are false); a
`SET` of an array past its end pads with null, one unit of energy each and at most
`runtime.MaxArrayGap` (`INVALID_ARGUMENT` past it), and a key that is no index is the
`UNSUPPORTED` failure; `INVOKE` of `#typeof` with no argument is answered by the VM before the
value. The 55 sites of apps and docs/examples answer every route as before.

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

The release gate begins with the immutable VM compatibility archive, then
runs the manifest-driven VM fault gauntlet, language conformance and inspector
contracts, VM and compiler contracts, build, tests, vet, focused race coverage,
compiler-to-VM fuzzing, determinism fuzzing, the pooled-VM soak, and the memory
retention campaign. The complete release and canary procedure lives in
`docs/RELEASE.md`.
