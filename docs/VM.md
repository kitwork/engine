# Kitwork VM

The Kitwork VM is a single-owner, stack-based Go interpreter for verified
Kitwork bytecode. It is not JavaScript embedded in Go and it does not execute
unverified instruction slices.

## Execution path

```text
source
  -> compiler
  -> verified runtime.Program
  -> app.Pool.Acquire
  -> VM.FastReset
  -> VM.Run / VM.ExecuteLambda
  -> runtime.execute
  -> VM.ResetForPool
```

`runtime.execute` is the only opcode dispatch loop. Root programs, nested
calls, array callbacks, deferred cleanup, commit continuations, scheduled work,
and detached work must return to that loop rather than implement a parallel
interpreter.

The dispatch loop trusts `Program`: opcode identity, operand widths, constant
indexes, jump targets, lambda entries, and stack transitions were already
verified before immutable publication. Structural rejection belongs to
`runtime.Verify`; repeating it for every executed instruction would weaken the
verified boundary and tax every request.

## State ownership

- `Program` is immutable, copied, verified, versioned, and fingerprinted before
  publication.
- A VM lease belongs to one request or detached execution at a time.
- `FastReset` changes the Program and accepts caller-owned globals while
  preserving the request context.
- Production request paths copy host values through `PrepareHostState` and use
  `FastResetPrepared`; only those VM-owned cleared containers are reusable.
- `ResetForPool` additionally removes context, builtins, globals, hooks, energy
  policy, and tenant-owned references before reuse by another app.
- Cleared global maps and builtin slices may be retained privately by the VM
  pool within fixed bounds. They are not exposed while pooled and are copied
  from the next owner's host environment before use.
- Captured frame maps are detached, never cleared. Closures retain their old
  lexical scope while the VM receives reusable clean storage.
- Inactive frames used by a previous Program are sanitized on reset.

## Bounded execution

- Every instruction has one canonical `InstructionSpec`.
- Operand width, stack effect, and energy come from that spec.
- Cancellation accounting is VM-wide. Nested `ExecuteLambda` calls cannot
  restart the 64-instruction cancellation interval.
- Energy accounting is saturating and cannot wrap around `uint64`.
- Cleanup after energy exhaustion receives one shared bounded reserve.
- The call stack is fixed at 64 frames.
- A pooled VM drops stack backing storage larger than 4,096 values and oversized
  variable/defer storage rather than retaining exceptional request memory.
- Reusable stack and defer backing arrays are cleared before their length is
  reset. Exceptional arrays/maps are detached, so pooled VMs do not pin values,
  closures, capabilities, or request contexts from a previous lease.

## Internal calls

Script-to-script lambda calls bind arguments directly from the verified VM
stack. This avoids allocating an argument slice that no host can retain.

Array callback methods use fixed arity paths for their one, two, or three
arguments. Scalar callback indexes are constructed as `value.Number` directly,
without interface boxing. Calls into host functions still receive an owned
argument slice because host code may retain it.

`unique(callback)` uses scalar value equality and object identity keys. Tenant
objects and arrays cannot become unhashable Go map keys or panic the
interpreter.

## Observability

`VM.Stats()` exposes a post-execution snapshot:

- instructions executed;
- energy consumed;
- current stack depth and retained capacity;
- current and peak frame depth.

The snapshot is not concurrent telemetry. A VM is single-owner and stats should
be read after execution.

`core.Engine.Health()` aggregates those snapshots across request and detached
execution boundaries. It reports executions, successes, failures, unique
Program owners, total instructions and energy, per-execution high-water marks,
peak frame depth, and structured diagnostic counts. The report records no
request, route, argument, URL, app identity, or tenant data and can be
serialized while the engine is live. Unique Programs are tracked by fixed-size
checksums, never by Program pointers, so telemetry cannot retain retired
generations.

`Program.Profile()` exposes a detached static profile computed during the same
pass that verifies bytecode:

- bytecode bytes, constants, instructions, and entry points;
- maximum verified stack depth across root and lambda control-flow graphs;
- opcode counts and their encoded energy.

Encoded energy counts each stored instruction once. It is useful for comparing
Program structure but does not predict loops, branches, callbacks, or request
frequency.

From a Kitwork host workspace, profile every executable router, cron, and queue
entrypoint with:

```text
go run . profile
go run . profile --json
```

The profiler compiles through the native bundler but never executes tenant
code, opens listeners, or starts app resources. Helper modules are counted only
when bundled into an executable Program. Its output also records the bytecode,
program encoding, artifact, and compiler schema versions plus compiler and
instruction fingerprints. Every counted Program has passed the complete
artifact encode, decode, verification, and deterministic re-encode gate.

## Determinism contract

Given the same immutable `Program`, globals, context state, and energy limit,
execution must produce the same:

- result or structured diagnostic;
- top-level variables;
- instruction and energy counts;
- final stack/frame depth and peak frame depth.

This must remain true for a fresh VM, a VM reused with `FastReset`, and a VM
leased through `app.Pool`. The determinism harness deliberately dirties the VM
with another Program, globals, closures, callbacks, builtins, hooks, and policy
before reuse. Compiler-accepted fuzz inputs exercise the same fresh-versus-dirty
comparison under a finite energy limit.

Program checksum and execution metadata are sufficient for regression
fingerprints. Do not serialize raw production globals for replay: globals can
contain credentials, request capabilities, proxies, and host functions.

## Performance contract

Run the VM benchmarks with:

```text
go test ./runtime -run '^$' -bench '^BenchmarkVM' -benchmem -count=5
```

The benchmark suite covers arithmetic dispatch, script function calls, array
callbacks, normal reset, exceptional-state allocation/release, and pool
acquire/release. Each execution benchmark reports its verified bytecode size
and instructions per operation.

Production-like request coverage lives in `work/bench_handler_test.go`:

```text
go test ./work -run '^$' -bench '^BenchmarkServeHandler(Corpus|Engine)$' -benchmem
```

The corpus covers plain text, JSON computation with callbacks, native imports,
guarded rendering, collections, and SQLite callback queries through the real
router, VM pool, capability, response, and render paths. Allocation gates keep
the pure handler workloads from silently regressing. `Corpus` includes
`httptest.ResponseRecorder` and request cloning; `Engine` reuses a discard
writer and request so its allocation count isolates engine-owned lifecycle
work.

Allocation regression tests enforce these workload-level budgets:

- arithmetic dispatch: zero allocations;
- 100 internal lambda calls: at most 6 allocations;
- map/filter/reduce callback chain: at most 25 allocations.

These are implementation budgets, not promises to tenant code. Change them only
with a benchmark result and an explanation.

Real Program profiles and handler CPU profiles currently do not justify local
slot opcodes. Name-based locals remain deliberately simple until map lookup is
a measured request bottleneck rather than merely a frequent static opcode.

## Change gate

A VM change is complete only after:

```text
go build ./...
go test ./...
go vet ./...
go test -race ./...
go test ./compiler -run '^$' -fuzz FuzzCompileVerifyExecute -fuzztime=10s
go test ./runtime -run '^$' -fuzz FuzzVMDeterminism -fuzztime=10s
KITWORK_SOAK=1 go test ./runtime -run TestPooledVMSoakAcrossPrograms
KITWORK_SOAK=1 go test ./runtime -run TestPooledVMReleasesOversizedVerifiedWorkload
go run . check
```

Do not add an opcode merely to shorten implementation elsewhere. A new opcode
must represent stable VM semantics, update the verifier and instruction table,
and include malformed-bytecode, energy, diagnostic, and execution tests.

## Fault-injection gate

The release suite must keep these failures contained:

- corrupt or incompatible Program data is rejected before publication;
- corrupt generation cache artifacts are rebuilt from source;
- native function and committer panics become `NATIVE_PANIC`;
- cancellation crosses nested callbacks and still runs frame defers;
- a malformed hot-reload candidate cannot replace the active generation;
- generation retirement waits for accepted requests;
- app shutdown drains accepted detached work before closing resources.
- queue worker restart drains the old poller before replacing runtime state.

The corresponding tests live in `runtime/program_binary_test.go`,
`runtime/failure_boundary_test.go`, `runtime/vm_cancel_test.go`,
`compiler/cache_test.go`, `core/engine_test.go`,
`site/generation_test.go`, and `app/application_test.go`.
