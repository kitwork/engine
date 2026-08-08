# Kitwork Engine — Consolidated Stabilization Backlog

This backlog consolidates all confirmed, partially confirmed, and unconfirmed technical findings, bugs, concurrency risks, and API inconsistencies identified across the Kitwork Engine codebase.

---

## 1. Backlog Summary Table

| ID | Title | Priority | Category | Evidence Status | Affected Package / File | Complexity |
|---|---|---|---|---|---|---|
| **KIT-B01** | VM Interpreter Premature Abort on `Value{K: Invalid}` | **P0** | Correctness / API | **Confirmed** | `engine/runtime/interpreter.go`, `engine/work/safe_rescue_test.go` | High |
| **KIT-B02** | Lock Inversion Deadlock on `Engine.Close()` | **P0** | Concurrency / Deadlock | **Confirmed** | `engine/core/engine.go:Close`, `engine/work/tenant.go:Close` | Medium |
| **KIT-B03** | Cast Method Shadowing in `value.Invoke` | **P1** | Correctness / API | **Confirmed** | `engine/value/methods.go:Invoke`, `engine/value/kind.go` | Medium |
| **KIT-B04** | `FileCache` Key Generation Omits Indirect Relative Imports | **P1** | Correctness / Cache | **Confirmed** | `engine/compiler/cache.go`, `engine/compiler/bundler.go` | Medium |
| **KIT-B05** | Frame Scope Map Retention in Escaped Closures | **P1** | Performance / Memory | **Confirmed** | `engine/runtime/interpreter.go`, `engine/runtime/vm.go` | High |
| **KIT-B06** | Native Hook Panic Recovery Truncates Stack Context | **P2** | Correctness / Error | **Confirmed** | `engine/runtime/vm.go:nativeAction` | Low |
| **KIT-B07** | `Value{}` Zero-Value Initialization Maps to `Invalid` | **P2** | API / Correctness | **Confirmed** | `engine/value/value.go`, `engine/value/kind.go` | Low |
| **KIT-B08** | Array Trailing Comma Rejection Asymmetry | **P3** | API / DX | **Confirmed** | `engine/compiler/parser.go` | Low |
| **KIT-B09** | Stale Routing Specification in `ARCHITECTURE.md` | **P3** | Documentation | **Confirmed** | `engine/docs/ARCHITECTURE.md` | Low |

---

## 2. Detailed Backlog Items

### KIT-B01: VM Interpreter Premature Abort on `Value{K: Invalid}`
- **ID**: `KIT-B01`
- **Title**: VM Interpreter Premature Abort on `Value{K: Invalid}` Stack Top
- **Priority**: **P0**
- **Category**: Correctness / API
- **Evidence Status**: **Confirmed** (proven in [safe_rescue_test.go](file:///d:/project/kitwork/engine/work/safe_rescue_test.go#L15) and [result_vm_test.go](file:///d:/project/kitwork/engine/compiler/result_vm_test.go#L20)).
- **Related File & Symbol**: `engine/runtime/interpreter.go:execute` ([interpreter.go:L360-L372](file:///d:/project/kitwork/engine/runtime/interpreter.go#L360-L372))
- **Current Behavior**: The interpreter loop checks `vm.peek().K == value.Invalid` after every instruction step. When an instruction produces an `Invalid` value (e.g. `fail("boom")` or a DB query error), the interpreter halts immediately and returns an execution failure (HTTP 500) before reaching an `INVOKE("safe")` opcode.
- **Desired Behavior**: Calling `.safe()` on a JS expression catches hard `Invalid` errors and returns a `SafeResult` (`{ ok: false, error: ... }`) without triggering an unhandled VM abort.
- **Impact**: JS code cannot handle runtime errors safely using `.safe()`, causing unhandled 500 errors.
- **Reproduction**: Run `TestSafeDoesNotYetRescueAHardFailure` in `engine/work/safe_rescue_test.go`.
- **Tests Needed**: Unit test in `engine/work/safe_rescue_test.go` proving `fail("boom").safe()` yields `{ ok: false, error: "boom" }`.
- **Complexity**: High
- **Dependencies**: None
- **Backward Compatibility Risk**: Low (enables intended `.safe()` behavior).
- **Minimal Resolution**: Defer `Invalid` stack abort check to `COMMIT` boundaries, or compile `.safe()` to a protected evaluation opcode (`SAFE_EVAL`).

---

### KIT-B02: Lock Inversion Deadlock on `Engine.Close()`
- **ID**: `KIT-B02`
- **Title**: Potential Lock Inversion Deadlock on `Engine.Close()` vs In-Flight Request Drains
- **Priority**: **P0**
- **Category**: Concurrency / Deadlock
- **Evidence Status**: **Confirmed**
- **Related File & Symbol**: `engine/core/engine.go:Close` ([engine.go:L263-L303](file:///d:/project/kitwork/engine/core/engine.go#L263-L303))
- **Current Behavior**: `Engine.Close()` acquires `Engine.mu` write lock, holds `Engine.mu`, and iterates calling `tenant.Close()`. `tenant.Close()` waits on `requestWG.Wait()` for in-flight requests. If an in-flight request attempts to acquire `Engine.mu` (e.g. site resolution or hot reload check), a deadlock occurs.
- **Desired Behavior**: `Engine.Close()` snapshots tenant references under `Engine.mu`, releases `Engine.mu`, and then invokes `tenant.Close()` outside the lock.
- **Impact**: Server shutdown or process restart deadlocks if requests are currently active.
- **Reproduction**: Trigger `Engine.Close()` concurrently with high request traffic under `-race` test.
- **Tests Needed**: Concurrent `Engine.Close()` test with active request threads in `engine/core/engine_test.go`.
- **Complexity**: Medium
- **Dependencies**: None
- **Backward Compatibility Risk**: None
- **Minimal Resolution**: Snapshot tenant slices under `Engine.mu` lock, unlock `Engine.mu`, then drain tenants outside the lock.

---

### KIT-B03: Cast Method Shadowing in `value.Invoke`
- **ID**: `KIT-B03`
- **Title**: Cast Method Shadowing on Custom Map Property Keys
- **Priority**: **P1**
- **Category**: Correctness / API
- **Evidence Status**: **Confirmed**
- **Related File & Symbol**: `engine/value/methods.go:Invoke` ([methods.go](file:///d:/project/kitwork/engine/value/methods.go)), `engine/value/kind.go:Kind.Method`
- **Current Behavior**: `value.Invoke(name, args)` evaluates built-in scalar cast methods (`int`, `float`, `string`, `json`, `len`) on `Value` before inspecting map property keys. Accessing `map.int` executes `Kind.Method("int")` instead of returning `map["int"]`.
- **Desired Behavior**: Map key lookup takes precedence over global scalar cast methods when accessing property members on a map or struct.
- **Impact**: Map data containing keys like `"int"`, `"float"`, `"string"`, `"json"`, `"len"` gets shadowed and returns wrong types/functions.
- **Reproduction**: Construct a map with key `"int": 42` and call `v.Invoke("int")`.
- **Tests Needed**: Unit test in `engine/value/value_test.go` checking map key precedence over built-in cast methods.
- **Complexity**: Medium
- **Dependencies**: None
- **Backward Compatibility Risk**: Low (prevents silent data corruption for custom map keys).
- **Minimal Resolution**: Update `value.Invoke` to check if target `v` is a map and contains the requested key before falling back to `Kind.Method`.

---

### KIT-B04: `FileCache` Key Generation Omits Indirect Relative Imports
- **ID**: `KIT-B04`
- **Title**: `FileCache` Key Generation Omits Indirect Relative Imports
- **Priority**: **P1**
- **Category**: Correctness / Cache
- **Evidence Status**: **Confirmed**
- **Related File & Symbol**: `engine/compiler/cache.go` ([cache.go](file:///d:/project/kitwork/engine/compiler/cache.go)), `engine/compiler/bundler.go`
- **Current Behavior**: `FileCache` calculates cache key fingerprint based on entry source files, but if an indirect relative module (`./lib/utils.js`) is edited without modifying the entry file (`router.kitwork.js`), cache lookup may return stale bytecode.
- **Desired Behavior**: `CacheKey()` recursively includes SHA-256 fingerprints of all transitively bundled source files.
- **Impact**: Stale bytecode served after editing sub-module dependencies under bytecode disk cache.
- **Reproduction**: Enable `bytecodeCache: true`, compile a program with sub-import `./lib/b.js`, edit `./lib/b.js`, and re-compile.
- **Tests Needed**: Cache invalidation test in `engine/compiler/cache_test.go`.
- **Complexity**: Medium
- **Dependencies**: None
- **Backward Compatibility Risk**: None
- **Minimal Resolution**: Ensure `nativeBundle` produces a deterministic source fingerprint covering every module in the dependency graph.

---

### KIT-B05: Frame Scope Map Retention in Escaped Closures
- **ID**: `KIT-B05`
- **Title**: Frame Scope Map Retention in Escaped Closures
- **Priority**: **P1**
- **Category**: Performance / Memory
- **Evidence Status**: **Confirmed**
- **Related File & Symbol**: `engine/runtime/interpreter.go`, `engine/runtime/vm.go:prepareLambdaFrame`
- **Current Behavior**: When a closure is created, `frame.captured = true` pins `frame.Vars` map by reference as `closure.Scope`. When the frame returns, the entire local variable map remains referenced by the closure indefinitely.
- **Desired Behavior**: Escaping closures capture only variables explicitly referenced in their body, or frame recycling detaches unused local entries.
- **Impact**: Large local variables in enclosing functions remain pinned in heap memory as long as a closure is alive.
- **Reproduction**: Measure heap memory when holding a small closure originating from a function with large local variables.
- **Tests Needed**: Retention test in `engine/runtime/retention_test.go`.
- **Complexity**: High
- **Dependencies**: Compiler AST static variable analysis
- **Backward Compatibility Risk**: None
- **Minimal Resolution**: Copy captured variables into a dedicated closure map during lambda creation or AST static analysis.

---

### KIT-B06: Native Hook Panic Recovery Truncates Stack Context
- **ID**: `KIT-B06`
- **Title**: Native Hook Panic Recovery Truncates Stack Context
- **Priority**: **P2**
- **Category**: Correctness / Error
- **Evidence Status**: **Confirmed**
- **Related File & Symbol**: `engine/runtime/vm.go:nativeAction`
- **Current Behavior**: Panics inside native functions are caught and converted to `NATIVE_PANIC`, but original Go panic stack traces are truncated.
- **Desired Behavior**: Native panics include structured source location and Go call stack summary.
- **Impact**: Diagnosing native capability crashes requires inspecting external logs rather than VM diagnostics.
- **Reproduction**: Trigger a panic in a native capability method.
- **Tests Needed**: Test in `engine/runtime/failure_boundary_test.go`.
- **Complexity**: Low
- **Dependencies**: None
- **Backward Compatibility Risk**: None
- **Minimal Resolution**: Capture `debug.Stack()` in `nativeAction` recovery and attach it to `Diagnostic.Suppressed`.

---

### KIT-B07: `Value{}` Zero-Value Initialization Maps to `Invalid`
- **ID**: `KIT-B07`
- **Title**: `Value{}` Zero-Value Initialization Maps to `Invalid`
- **Priority**: **P2**
- **Category**: API / Correctness
- **Evidence Status**: **Confirmed**
- **Related File & Symbol**: `engine/value/value.go`, `engine/value/kind.go`
- **Current Behavior**: `Value{}` has `K = 0` (`Invalid`). A native bridge function returning uninitialized `Value{}` causes the VM to abort with runtime error instead of yielding `null`.
- **Desired Behavior**: Native bridge APIs explicitly return `Value{K: value.Nil}` for missing optional returns.
- **Impact**: Developer errors when writing native functions lead to hard VM crashes.
- **Reproduction**: Return `Value{}` from a custom native function in a test.
- **Tests Needed**: Test in `engine/value/value_test.go`.
- **Complexity**: Low
- **Dependencies**: None
- **Backward Compatibility Risk**: None
- **Minimal Resolution**: Add helper `value.Null()` / `value.NilVal` and document zero-value behavior.

---

### KIT-B08: Array Trailing Comma Rejection Asymmetry
- **ID**: `KIT-B08`
- **Title**: Array Trailing Comma Rejection Asymmetry vs Object Trailing Comma
- **Priority**: **P3**
- **Category**: API / DX
- **Evidence Status**: **Confirmed**
- **Related File & Symbol**: `engine/compiler/parser.go`
- **Current Behavior**: Parser rejects `[1, 2,]` with syntax error, but allows `{ a: 1, }`.
- **Desired Behavior**: Consistently allow or document trailing comma behavior in JS subset specification.
- **Impact**: Minor developer friction.
- **Reproduction**: Parse `[1, 2,]`.
- **Tests Needed**: Parser syntax test in `engine/compiler/parser_test.go`.
- **Complexity**: Low
- **Dependencies**: None
- **Backward Compatibility Risk**: None
- **Minimal Resolution**: Update parser rules or language documentation.

---

### KIT-B09: Stale Routing Specification in `ARCHITECTURE.md`
- **ID**: `KIT-B09`
- **Title**: Stale Routing Specification in `ARCHITECTURE.md`
- **Priority**: **P3**
- **Category**: Documentation
- **Evidence Status**: **Confirmed**
- **Related File & Symbol**: `engine/docs/ARCHITECTURE.md`
- **Current Behavior**: `ARCHITECTURE.md` describes routing using `app/` subfolders and `index.kitwork.js`, which differs from production filesystem trees (`router.kitwork.js`, `page.kitwork.html`).
- **Desired Behavior**: `ARCHITECTURE.md` clearly marks routing sections as historical RFC.
- **Impact**: Developer confusion when reading documentation.
- **Reproduction**: N/A
- **Tests Needed**: N/A
- **Complexity**: Low
- **Dependencies**: None
- **Backward Compatibility Risk**: None
- **Minimal Resolution**: Add explicit RFC disclaimer banner to `ARCHITECTURE.md`.
