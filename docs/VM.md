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

- `runtime/limits.go` is the single source for Program-envelope, bytecode,
  constant-pool, verified-stack, call-depth, default-energy, cleanup, and
  cancellation limits. `runtime.Limits()` returns a detached read-only
  snapshot; there is no API for tenant code to widen structural limits.
- Every instruction has one canonical `InstructionSpec`.
- Operand width, stack effect, and energy come from that spec.
- Cancellation accounting is VM-wide. Nested `ExecuteLambda` calls cannot
  restart the 64-instruction cancellation interval.
- Energy accounting is saturating and cannot wrap around `uint64`.
- Cleanup after energy exhaustion receives one shared bounded reserve.
- The call stack is fixed at 64 frames. That ceiling is enforced independently
  of the exported frame storage, so enlarging a VM's backing slice cannot widen
  execution policy.
- A pooled VM drops stack backing storage larger than 4,096 values and oversized
  variable/defer storage rather than retaining exceptional request memory.
- Reusable stack and defer backing arrays are cleared before their length is
  reset. Exceptional arrays/maps are detached, so pooled VMs do not pin values,
  closures, capabilities, or request contexts from a previous lease.

`TestPooledVMReleasesClosureHeavyPrograms` runs real compiler output containing
closure factories, nested closures, and array callbacks through one VM pool.
After every lease it verifies that no Program, context, host state, stack value,
root variable, function frame, or defer remains reachable directly from the VM.

`TestEngineMemoryRetentionCampaign` is the longer production-path check. It
uses a deliberately large generation-owned closure scope, native HTTP, and at
least 250 replacements. Forced-GC checkpoints measure post-warm-up heap bytes,
heap objects, and goroutines; both the total delta and the linear heap slope are
bounded. Optional `KITWORK_HEAP_PROFILE` output is the evidence source before
changing closure capture or pooling policy.

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

Health also includes process-local VM pool counters. `active` is the current
lease count; `created`, `acquired`, and `released` describe churn. There is no
idle-capacity field because Go's `sync.Pool` may discard entries during GC
without an observable callback.

`core.Engine.Diagnostics()` adds process memory/GC, build, lifecycle policy,
compiler/runtime compatibility metadata, and the detached runtime-limit
snapshot around that health report. The detached JSON and optional private
heap-profile bundle are specified in `docs/DIAGNOSTICS.md`; diagnostics never
traverse or retain VM owners.

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

## Language conformance corpus

`conformance/testdata/corpus.json` indexes executable `.kitwork.js` fixtures
for accepted behavior, structured runtime diagnostics, and deliberately
rejected syntax. Accepted fixtures cross the native compiler, artifact
encode/decode/re-encode boundary, verifier, fresh VM, previously-used VM, and
released/reacquired app VM pool lease. All three executions must retain the
same variables, result or diagnostic, instruction and energy counts, and final
stack/frame state.

Run the corpus directly with:

```text
go test ./conformance -run TestLanguageConformanceCorpus -count=1
```

Language changes are incomplete until the corpus records their accepted and
rejected boundary. Host capabilities stay in route-level contract tests rather
than this pure language corpus.

## Frozen VM compatibility archive

`compatibility/testdata/v2` stores representative Program binaries produced by
the frozen VM v2/compiler v2 tuple. Unlike compiler `.kwbc` cache entries, these
`.kwpb` files contain the Program envelope only and are committed exclusively
as internal test evidence. Normal archive tests never compile their adjacent
sources.

```text
go test ./compatibility -run TestVMV2CompatibilityArchive -count=1
```

Each case freezes source and binary hashes, Program checksum, verifier profile,
result or diagnostic, execution statistics, deterministic re-encoding, and
reuse/pool behavior. `go run ./cmd/vmcompat --update` creates new evidence;
replacing existing evidence additionally requires `--replace` and a reviewed
version decision.

## VM fault gauntlet

`runtime/testdata/faults/manifest.json` defines the malformed Program and
execution-failure corpus in readable, reviewable cases. Decoder cases mutate a
valid Program envelope at test time instead of committing opaque broken binary
fixtures. Verifier cases require exact structured rejection codes. Execution
cases require a deterministic diagnostic fingerprint, then prove that both the
same VM and a released/reacquired pooled VM execute a clean recovery Program
without retaining stack, frames, globals, context, energy, or Program state.

```text
go test ./runtime -run ^TestVMFaultGauntlet -count=1
```

The gauntlet covers framing and size corruption, invalid constants and control
flow, unsupported opcodes, missing or stopped execution, native panic, runtime
errors, cancellation, energy exhaustion, stack overflow, and cross-Program
lambda rejection. Its stack-overflow fixture freezes the exact call-depth
ceiling. A new decoder, verifier, or execution-failure boundary is incomplete
until it has a manifest case and recovery proof here.

## Bytecode inspector

From the Kitwork host workspace, inspect one executable source without running
tenant code or starting app resources:

```text
go run . inspect apps/<identity>/<domain>/router.kitwork.js
go run . inspect apps/<identity>/<domain>/router.kitwork.js --json
```

The command compiles native imports, validates and restores the bytecode
artifact, and then reports Program identity, bounded constant previews, lambda
entry points, every instruction and operand, canonical energy, verifier-proven
stack depth, jump targets, source locations, and unreachable instructions. The
inspector consumes `runtime.InstructionSpec`, the shared decoder, and the shared
stack analyzer; it does not maintain a second opcode contract. Inspector output
can contain source literals and should be reviewed before publication.

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
- map/filter/reduce callback chain: at most 25 allocations;
- 128-item script array growth: at most 24 allocations;
- 64-node script object chain: at most 160 allocations;
- two 16 KiB native buffers: at most 24 allocations;
- 32-record reflected native collection: at most 340 allocations.

These are implementation budgets, not promises to tenant code. Change them only
with a benchmark result and an explanation.

The collection budgets were tightened after allocation profiles showed two
avoidable conversion costs: string-valued keys and method names rebuilt text
buffers, and every `INVOKE` created a closure plus an eager diagnostic label.
Returning canonical string payloads directly and using a dedicated native-method
panic boundary first reduced the fixed array workload from 394 to 137 allocations
and the object workload from 257 to 129. An ownership-aware `INVOKE` fast path then
reduced the array workload to 9 allocations by lending a capped VM-stack view only
to engine-owned standard methods. Extensions and proxies still receive isolated
argument storage because they may retain it after returning. `unshift` explicitly
copies its result storage, so no standard method can retain the VM stack. These
changes do not alter bytecode or language semantics.

The opt-in value-pressure campaign complements these fixed allocation envelopes
with retention evidence at larger sizes. It executes script-created arrays and
objects plus native buffers and reflected collections through `app.Pool`, then
forces GC at bounded checkpoints. Each workload must preserve deterministic
instruction and energy counts, every VM owner must be cleared on release, and
post-warm-up heap, object, and goroutine counts must plateau.

Stack cleanup is checked against the reusable backing array, not only
`len(vm.Stack)`. The VM tracks its actual stack high-water mark, clears exactly
that used region on reset, and exposes the observed peak through
`VMStats.PeakStackDepth`; this avoids both stale references and clearing the full
pooled capacity after every request.

The report contains only scalar measurements and workload names:

```text
KITWORK_VALUE_PRESSURE=1 go test ./runtime \
  -run '^TestVMValuePressureCampaign$' -count=1 -timeout=10m -v
```

The default JSON path is `.artifacts/value-pressure.json`; override it with
`KITWORK_VALUE_PRESSURE_REPORT`. This evidence does not establish a public
collection-size promise. Energy remains the tenant execution budget, while
host-native capabilities remain responsible for bounding their own inputs and
results.

Real Program profiles and handler CPU profiles currently do not justify local
slot opcodes. Name-based locals remain deliberately simple until map lookup is
a measured request bottleneck rather than merely a frequent static opcode.

## Change gate

A VM/compiler change is first checked against the compatibility tuple in
`docs/VM_V2_FREEZE.md`. The canonical local commands are:

```text
go run ./cmd/releasegate --mode verify --report .artifacts/verify.json
go run ./cmd/releasegate --mode release --require-clean --report .artifacts/release.json
```

The full 24 to 72 hour canary and rollout criteria are documented in
`docs/RELEASE.md`.

A VM change is complete only after:

```text
go build ./...
go test ./...
go vet ./...
go test -race ./...
go test ./runtime -run 'TestRuntimeLimits|TestRuntimeStructuralLimitBoundaries|TestRuntimeEnergyBoundary|TestRuntimeCallDepth' -count=1
go test -race ./core -run TestEngineLifecycleGauntlet -count=10
go test ./compiler -run '^$' -fuzz FuzzCompileVerifyExecute -fuzztime=10s
go test ./runtime -run '^$' -fuzz FuzzVMDeterminism -fuzztime=10s
KITWORK_SOAK=1 go test ./runtime -run TestPooledVMSoakAcrossPrograms
KITWORK_SOAK=1 go test ./runtime -run TestPooledVMReleasesOversizedVerifiedWorkload
KITWORK_VALUE_PRESSURE=1 go test ./runtime -run '^TestVMValuePressureCampaign$' -count=1 -timeout=10m -v
KITWORK_RETENTION=1 go test ./core -run TestEngineMemoryRetentionCampaign -count=1 -v
KITWORK_RESTART_CAMPAIGN=1 go test ./core -run '^TestEngineRestartRecoveryCampaign$' -count=1 -timeout=10m -v
KITWORK_CONTENTION_CAMPAIGN=1 go test ./core -run '^TestEngineCacheContentionCampaign$' -count=1 -timeout=10m -v
go run . check
```

Do not add an opcode merely to shorten implementation elsewhere. A new opcode
must represent stable VM semantics, update the verifier and instruction table,
and include malformed-bytecode, energy, diagnostic, and execution tests.

## Fault-injection gate

The release suite must keep these failures contained:

- corrupt or incompatible Program data is rejected before publication;
- corrupt generation cache artifacts are rebuilt from source;
- fresh engine processes sharing a cache preserve healthy artifacts byte for
  byte and deterministically repair truncated, stale, checksum-corrupt, or
  deleted artifacts;
- a restart after an intentional process exit serves the same response without
  retaining temporary cache artifacts;
- barrier-synchronized engine processes converge on one valid artifact during
  cold publication, corrupt-cache recovery, and source revision while warm
  cache reads remain byte-for-byte untouched;
- native function and committer panics become `NATIVE_PANIC`;
- cancellation crosses nested callbacks and still runs frame defers;
- a malformed hot-reload candidate cannot replace the active generation;
- generation retirement waits for accepted requests;
- app shutdown drains accepted detached work before closing resources.
- queue worker restart drains the old poller before replacing runtime state.

The corresponding tests live in `runtime/program_binary_test.go`,
`runtime/failure_boundary_test.go`, `runtime/vm_cancel_test.go`,
`compiler/cache_test.go`, `core/engine_test.go`, `core/restart_campaign_test.go`,
`core/cache_contention_campaign_test.go`, `site/generation_test.go`, and
`app/application_test.go`.
