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
app.web({
  bytecodeCache: true,
  // Optional. Relative paths resolve from app root.
  bytecodeCacheDir: ".kitwork/cache/bytecode",
});
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
- Two-byte operands are unsigned big-endian values.
- Constant indexes and control-flow addresses are 16-bit.
- Code is limited to 65,535 bytes.
- A constant pool contains at most 65,536 values.
- Jump targets address instruction boundaries, never arbitrary bytes.

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

## Frozen VM v2 contract

VM v2 is frozen by `runtime/contract_test.go`. The test fixes:

- `BytecodeVersion == 2`;
- every opcode numeric slot, including `_RESERVED` and `_LIMIT`;
- the complete instruction metadata checksum;
- `ProgramEncodingVersion == 1`.

Version ownership is deliberately separate:

- change `CompilerSchemaVersion` when lowering or compiler semantics change
  without changing the VM instruction contract;
- change `ProgramEncodingVersion` or `BytecodeArtifactVersion` when only a
  storage envelope becomes incompatible;
- increment `BytecodeVersion` only when opcode encoding, stack behavior,
  constant semantics, or execution meaning becomes incompatible.

Updating the VM v2 golden checksum without a deliberate version decision is a
release failure, not routine test maintenance.

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
