# Kitwork Bytecode Contract

Kitwork bytecode is an internal, version-sensitive instruction format for the
hand-written stack VM. It is never accepted as executable merely because it is
a byte slice. Production code follows this path:

```text
source
  -> lexer / parser / compiler
  -> bytecode + constants
  -> runtime.NewProgram (copy + verify + fingerprint)
  -> generation publication
  -> VM execution
```

`compiler.CompileSource` and `compiler.CompileFile` both return a
`compiler.Bytecode` that owns a verified `*runtime.Program`.
Route preparation, cron and queue compilation, and `kitwork check` use
`CompileFile`, so verification happens before activation rather than once per
request.

The VM accepts only `*runtime.Program`; it cannot be constructed or reset with
loose instruction and constant slices.

## Program boundary

`runtime.NewProgramWithDebug` is the compiler publication boundary.
`runtime.NewProgram` remains the compatibility constructor for callers that
still provide a byte-offset line map:

- it copies code, constants, lambda templates, and debug entries before
  validation;
- it accepts only immutable scalar constants and unbound lambda templates;
  mutable collections and host objects must enter through globals, builtins, or
  runtime results;
- it rejects malformed bytecode and invalid debug ranges;
- it assigns `BytecodeVersion`;
- it fingerprints version, code, constants, and debug metadata with SHA-256;
- it exposes only copied snapshots for diagnostics and serialization.

## Local cache artifacts

`Program.MarshalBinary` and `runtime.UnmarshalProgram` define the verified
local-cache representation. The envelope carries:

- `ProgramEncodingVersion` for storage framing;
- `BytecodeVersion` for opcode and constant semantics;
- the Program checksum;
- bytecode, immutable constants, and compressed debug entries.

Decoding is not a shortcut around publication. It enforces size bounds,
rejects incompatible versions and trailing or truncated data, reconstructs
owned values, runs the complete verifier, and compares the resulting checksum
before returning an executable Program.

The compiler adds a second `compiler.Bytecode` envelope. Its fingerprint
combines `CompilerSchemaVersion`, bytecode and storage versions, and the
instruction-table checksum. Its source fingerprint hashes every bundled source
name and byte in deterministic order. `Bytecode.CacheKey()` combines both, so
source or engine changes select a different cache identity.

Artifacts intentionally do not serialize dependency paths. The opt-in
`compiler.FileCache` therefore performs current source parsing and native
import discovery before lookup, keys the artifact by the exact bundled source
fingerprint, and attaches the current dependency list after a hit. A miss,
stale artifact, or corrupt artifact compiles from source and replaces the local
file. Cache I/O failure never prevents compilation.

The engine does not enable a process-global disk cache implicitly. A host opts
in from its web manifest:

```javascript
app.website({
  bytecodeCache: true,
  // Optional. Relative paths resolve from app root.
  bytecodeCacheDir: ".kitwork/cache/bytecode",
}).run(8080);
```

When enabled without a directory, the default is
`<root>/.kitwork/cache/bytecode`. Every prepared `site.Generation` owns its
`FileCache` handle and uses it for root and nested router compilation.
Generation replacement never mutates the active Program. Cron and queue
programs remain app-owned and compile through their existing app lifecycle.
The files on disk are reconstructible host storage and may outlive a
generation.

Closures carry an opaque `value.ProgramRef`, not instruction, constant, and
debug-table slices. Detached execution resolves that reference and resets a
pooled VM onto the same owning program. A VM rejects a lambda whose owner does
not match its currently loaded program.

Lambda templates also carry diagnostic metadata: an inferred function name
and declaration file, line, and column. That metadata is copied into closures
and detached snapshots and participates in the Program checksum. It does not
affect instruction semantics. Anonymous callbacks use `<anonymous>`; synthetic
native module wrappers use `<module>`.

## Debug table

Compiler tokens retain a normalized source name and byte position. For file
compilation, the entry is named by its base name and imported modules are named
relative to the entry directory. The compiler converts lexer byte positions
into one-based line and byte-column locations.

The compiler emits a `DebugEntry` only when the active source location changes.
Each entry starts a range at an instruction byte offset. Program interns file
names and stores compact file IDs; lookup uses binary search. Operands inherit
the location of their instruction.

`Program.DebugEntries()` returns a detached compressed snapshot and
`Program.SourceAt(ip)` resolves one location. `Program.SourceMap()` expands
lines only when a legacy caller explicitly requests it.

`FastReset` changes request state and the loaded `Program`; it never verifies
again. Verification is paid once when the compiler publishes the program.

## Encoding

- One opcode is one unsigned byte.
- Operands immediately follow the opcode.
- Two- and three-byte operands are unsigned big-endian values.
- Constant indexes and control-flow addresses are 16-bit; the wide forms
  `PUSH_WIDE`, `LOAD_WIDE`, `STORE_WIDE`, `JUMP_WIDE`, `TRUE_WIDE`, `FALSE_WIDE`
  and `ITER_WIDE` carry them in 24 bits (bytecode 3).
- Code is limited to 2 MiB (`runtime.MaxBytecodeSize`, 2,097,152 bytes).
- A constant pool contains at most 1,048,576 values (`runtime.MaxConstants`).
- The Program envelope is limited to 64 MiB (`runtime.MaxProgramBinarySize`).
- Jump targets address instruction boundaries, never arbitrary bytes.

### Wide forms (bytecode 3, 14/10/2026)

Bytecode 2 limited a program to 65,535 bytes because a jump's address was two
bytes. apptop.vn's largest route compiled to 65,038 of them, so any growth of its
`_core` would have stopped the domain from compiling. Bytecode 3 keeps every
bytecode 2 instruction as it was (same slot, operand width, stack effect and
energy; `runtime.TestVMContract` hashes those 33 slots back to bytecode 2's
checksum) and adds seven wide forms after the reserved slot (33 to 39). Each is its
instruction with a three-byte operand: the same stack effect, the same energy, the
same execution. The decoder gives a wide instruction the meaning of its instruction
(`decodedInstruction.op` is `PUSH` for a `PUSH_WIDE`, `encoded` is what was
written), so the verifier, the inspector and the call-site reader treat both alike;
the profile and the inspector show the encoded name.

The compiler writes the narrow form unless it cannot:

- `PUSH`, `LOAD` and `STORE` of a constant index above 65,535 are wide
  (`Compiler.emitIndex`);
- every jump is written through `emitJump`, `emitJumpTo` and `patchJump`. When a
  two-byte jump is given an address past 65,535, compiling the `Program` starts
  again with every jump wide (names were resolved once, so the second pass emits
  the same program); a jump whose address fits stays narrow in a program that never
  needs one wide.

The verifier keeps one encoding per program: a wide constant index of 65,535 or
less is `INVALID_OPERAND`, and so is a wide jump in a program of 65,535 bytes or
less. So a program that fits two-byte operands holds no wide form and is the
same bytes it was in bytecode 2; only its envelope's version and checksum moved.
Over the 1,114 programs under apps and docs/examples that compiled on 14/10/2026,
every instruction stream, constant pool and debug table is unchanged. A program
past `MaxBytecodeSize` is a compile error at the place it crosses the limit
(`router.kitwork.js:12:5: chương trình dịch ra … byte bytecode, quá giới hạn
2097152 byte của một chương trình …`), not a verifier failure.

The limits leave about 32 times apptop.vn's route in bytes (65,038 bytes, envelope
993,905 bytes, 13,808 constants) and 27 times its code: a route that bundles its
`_core` N times writes its jumps and its constants past 65,535 wide, about 16% more
bytes, so 16 copies compile to 1,209,855 bytes (218,739 constants, envelope
16,334,935 bytes), 27 copies to 2,086,126 bytes (envelope 27,624,664 bytes), and 28
copies are past the limit (measured 15/10/2026). A program at 2 MiB references at
most about 540,000 constants, so the constant limit is not met first, and its
envelope at apptop.vn's ratio (about 15 bytes per byte of code, mostly the debug
table's file names) is about 32 MB, inside 64 MiB. `runtime/limits.go` fails to
compile if a limit grows past what three bytes address.

`runtime.InstructionSpec` is the single source of truth for:

- instruction name;
- operand widths;
- fixed or dynamic stack effect;
- energy cost.

The VM's single dispatch loop, verifier, energy table, and playground
disassembler all consume that contract.

## Stack model

Most instructions have a fixed `(StackIn, StackOut)` contract. `CALL` and
`INVOKE` derive their input count from the encoded argument count:

```text
CALL(n)   consumes function + n arguments, produces one result
INVOKE(n) consumes target + n arguments + method name, produces one result
```

`ITER` has two effects:

```text
continue: collection + index -> collection + next-index + item
exhausted: collection + index -> empty, then jump
```

Three meanings changed on 14/10/2026 (compiler schema 9, the owner's "đồng ý hết")
without an opcode, operand, stack effect or energy change:

- **Truth.** `TRUE`, `FALSE`, `AND`, `OR`, `NOT` and the array callbacks read
  `value.Truthy`, which is JavaScript's: a number (a time and a duration too) is
  false only at 0, -0 and NaN, so a negative one is true (it was `N > 0`).
- **`SET` of an array** (`array_item.go`): a whole-number key from 0 to the length
  replaces or appends; past the length the gap is padded with null, one unit of
  energy per null charged before the array grows (`ENERGY_LIMIT` when the run
  cannot pay), and more than `runtime.MaxArrayGap` (1,048,576) nulls is the
  `INVALID_ARGUMENT` failure; any other key (negative, fractional, NaN, a string,
  a boolean, null) is the `UNSUPPORTED` failure. It appended the item before.
- **`INVOKE` of `#typeof`** with no argument (`runtime.TypeofMethod`) is answered
  by the VM before any key, method or proxy of the target, with JavaScript's typeof
  (`value.TypeOf`; null is `"undefined"`). `typeof x` compiles to
  `x; PUSH "#typeof"; INVOKE 0`; a source cannot write `#` after a dot, so nothing
  else reaches it.

`RETURN` and `HALT` are terminal and may retain zero or one value. More than one
value at a terminal is invalid bytecode. `COMMIT` observes the top value without
changing stack depth. On success the slot retains that value; a committer panic
or invalid completion callback replaces the slot with a runtime diagnostic.

Every frame owns a LIFO defer list. All terminal and failure exits unwind that
list exactly once. Cleanup errors preserve an existing primary failure through
`Diagnostic.Suppressed`; cleanup after energy exhaustion uses one bounded
reserve shared by the complete unwind.

## Verification

`runtime.Verify(code, constants)` performs four passes without executing host
logic:

1. Decode every instruction and reject unknown, reserved, unsupported, or
   truncated opcodes.
2. Validate opcode-specific operands, constant indexes and types, jump targets,
   collection kinds, and comparison modes.
3. Validate every lambda constant address against decoded instruction
   boundaries.
4. Walk control flow from the program entry and every lambda entry, proving
   stack availability, consistent join depths, bounded stack growth, and
   balanced terminals.

Failures are returned as `runtime.VerifyError` with a stable `VerifyCode`, byte
offset, opcode, and detail. Invalid bytecode is rejected before a generation can
replace the last valid one.

Execution failures use the parallel `runtime.Diagnostic` contract. Each
diagnostic has a stable `DiagnosticCode`, byte offset, source file, line,
column, function, and an inner-to-outer call stack. Its formatted text remains
available through the invalid `value.Value`, so existing route and playground
error handling does not need to change.

## Historical slots

Opcode numeric values are append-only. Removed instructions keep their slots
reserved so a future opcode can never reinterpret old bytecode.

- The former `POPFINSOFT` slot is reserved.
- `LAMBDA` and `YIELD` retain historical numeric values but are unsupported and
  rejected. Lambdas are encoded as constants containing an entry address; the
  compiler emits `JUMP` over the function body and `PUSH` for the closure
  prototype.

## Frozen VM contract

The VM contract is frozen by `runtime/contract_test.go` (`TestVMContract`; it was
`TestVMV2Contract` until bytecode 3). The test fixes:

- `BytecodeVersion == 3` (2 until 14/10/2026);
- every opcode numeric slot, including `_RESERVED`, the seven wide forms and
  `_LIMIT`;
- the complete instruction metadata checksum, and that the slots bytecode 2 had
  still hash, with version 2, to bytecode 2's checksum;
- `ProgramEncodingVersion == 1`.

Version ownership is deliberately separate:

- change `CompilerSchemaVersion` when lowering or compiler semantics change
  without changing the VM instruction contract;
- change `ProgramEncodingVersion` or `BytecodeArtifactVersion` when only a
  storage envelope becomes incompatible;
- increment `BytecodeVersion` only when opcode encoding, stack behavior,
  constant semantics, or execution meaning becomes incompatible.

Updating the VM golden checksum without a deliberate version decision is a
release failure, not routine test maintenance. Bytecode 3 is such a decision: a
runtime of bytecode 2 cannot run a wide instruction, so the version moved, and
`UnmarshalProgram` refuses a bytecode 2 envelope (a cached artifact is also refused
by its compiler fingerprint and compiled again).

## Change rule

Adding or changing an instruction requires all of the following in one change:

1. Update `InstructionSpec`.
2. Implement the opcode once in `runtime.execute`.
3. Extend verifier operand and control-flow rules.
4. Add valid, malformed, and energy regression tests.
5. Run build, test, vet, race, and `kitwork check`.

No feature may bypass `runtime.NewProgram`. `Compiler.ByteCodeResult` is also a
verified publication path and returns an error when the generated program
violates the contract.
