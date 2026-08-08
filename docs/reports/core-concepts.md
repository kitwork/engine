# Kitwork Engine — Core Concepts & Glossary

This glossary defines the authoritative concepts, data structures, and types that exist in the Kitwork Engine codebase.

---

## Glossary of Core Concepts

### 1. Program
- **Definition**: The immutable, verified publication unit containing executable bytecode instructions, scalar constant pool, lambda prototypes, source maps, and cryptographic checksum fingerprints.
- **Package**: `engine/runtime`
- **File**: [engine/runtime/program.go](file:///d:/project/kitwork/engine/runtime/program.go)
- **Type/Function**: `type Program struct`, `func NewProgramWithDebug(...)`

### 2. Module
- **Definition**: An isolated JavaScript source file bundled by `compiler.nativeBundle`. Modules are converted to self-executing IIFE constructs (`const __kw_mod_N = (() => { ... return { exports }; })();`) with export objects stored in module scopes.
- **Package**: `engine/compiler`
- **File**: [engine/compiler/bundler.go](file:///d:/project/kitwork/engine/compiler/bundler.go)
- **Type/Function**: `func nativeBundle(...)`

### 3. Runtime
- **Definition**: The multi-tiered execution environment hierarchy:
  - `app.Runtime`: Identity-scoped runtime owning databases, schedulers, and background task trackers.
  - `site.Runtime`: Domain-scoped runtime managing generation transitions and persistent caches.
  - `runtime.VM`: Stack-based virtual machine executing bytecode opcodes.
- **Packages**: `engine/app`, `engine/site`, `engine/runtime`
- **Files**: [engine/app/application.go](file:///d:/project/kitwork/engine/app/application.go), [engine/site/runtime.go](file:///d:/project/kitwork/engine/site/runtime.go), [engine/runtime/vm.go](file:///d:/project/kitwork/engine/runtime/vm.go)
- **Types**: `app.Runtime`, `site.Runtime`, `runtime.VM`

### 4. Worker
- **Definition**: Background task execution workers owned by `app.Runtime`. Includes `work.QueueWorker` (polls background queue stores) and `work.CronScheduler` worker goroutines.
- **Package**: `engine/work`
- **Files**: [engine/work/queue.go](file:///d:/project/kitwork/engine/work/queue.go), [engine/work/cron.go](file:///d:/project/kitwork/engine/work/cron.go)
- **Types**: `type QueueWorker struct`, `type CronScheduler struct`

### 5. Context
- **Definition**:
  - `work.Context`: The script-facing `$ / ctx` object passed to handlers, exposing `ctx.request`, `ctx.response`, `ctx.db`, `ctx.view()`, `ctx.json()`.
  - `request.Scope`: The host request boundary carrying cancellation context, authentication principal, and permissions.
- **Packages**: `engine/work`, `engine/request`
- **Files**: [engine/work/context.go](file:///d:/project/kitwork/engine/work/context.go), [engine/request/scope.go](file:///d:/project/kitwork/engine/request/scope.go)
- **Types**: `work.Context`, `request.Scope`

### 6. Value
- **Definition**: The fundamental 24-byte tagged union / NaN-boxed data representation. Stores primitive scalars (number `N`, string/map/array/lambda `V`), kind tags (`K`), error markers (`IsError`), and attached error payloads (`ErrorVal`).
- **Package**: `engine/value`
- **File**: [engine/value/value.go](file:///d:/project/kitwork/engine/value/value.go)
- **Type**: `type Value struct`

### 7. Opcode
- **Definition**: Single-byte VM instruction identifiers (e.g. `PUSH`, `LOAD`, `STORE`, `CALL`, `INVOKE`, `ITER`, `COMMIT`, `HALT`).
- **Package**: `engine/runtime`
- **File**: [engine/runtime/opcode.go](file:///d:/project/kitwork/engine/runtime/opcode.go)
- **Type**: `type Opcode byte`

### 8. Blueprint
- **Definition**: The prepared rendering blueprint (`work.RenderPlan` & `site.Presentation`) defining pre-parsed layout hierarchies, default slot structures, and JIT CSS/icon configurations.
- **Packages**: `engine/work`, `engine/site`
- **Files**: [engine/work/render_plan.go](file:///d:/project/kitwork/engine/work/render_plan.go), [engine/site/presentation.go](file:///d:/project/kitwork/engine/site/presentation.go)
- **Types**: `work.RenderPlan`, `site.Presentation`

### 9. Native Function
- **Definition**: A Go-native function wrapped into a `value.Value` (via `value.NativeFunc` or `value.GoFunc`) that can be directly invoked by the VM interpreter via `INVOKE` or `CALL`.
- **Package**: `engine/value`
- **File**: [engine/value/kind.go](file:///d:/project/kitwork/engine/value/kind.go)
- **Type**: `type NativeFunc func(args []Value) Value`

### 10. Handle
- **Definition**: Resource access handles:
  - `site.Lease`: A reference count handle on active `site.Generation` protecting it from eviction during request processing.
  - `compiler.FileCache`: Handle for local bytecode artifact loading and verification.
- **Packages**: `engine/site`, `engine/compiler`
- **Files**: [engine/site/generation.go](file:///d:/project/kitwork/engine/site/generation.go), [engine/compiler/cache.go](file:///d:/project/kitwork/engine/compiler/cache.go)
- **Types**: `site.Lease`, `compiler.FileCache`

### 11. Result
- **Definition**:
  - `value.CommitResult`: Output structure returned by `Committer.Commit()` during opcode `COMMIT` processing.
  - `value.SafeResult`: Wrapped result object returned by `.safe()` containing `.value`, `.error`, `.code`, `.ok`.
- **Package**: `engine/value`
- **Files**: [engine/value/commit.go](file:///d:/project/kitwork/engine/value/commit.go), [engine/value/result.go](file:///d:/project/kitwork/engine/value/result.go)
- **Types**: `value.CommitResult`, `value.SafeResult`

### 12. Safe
- **Definition**: Inline error handling method `.safe()` on `value.Value` that reshapes success, inline errors, and invalid values into a standard `SafeResult` (`{ ok, value, error, code }`).
- **Package**: `engine/value`
- **File**: [engine/value/result.go](file:///d:/project/kitwork/engine/value/result.go)
- **Function**: `func (v Value) Safe(args ...Value) Value`

### 13. Must
- **Definition**: Strict assertion helper functions in generator APIs (e.g. `id.Generator.Must(length)`) that generate values or panic on internal failure.
- **Package**: `engine/id`
- **File**: [engine/id/id.go](file:///d:/project/kitwork/engine/id/id.go)
- **Function**: `func (g *Generator) Must(length int) string`

### 14. Tenant
- **Definition**: The execution facade (`work.Tenant`) representing an active site folder. Adapts `app.Runtime` and `site.Generation` resources for HTTP request serving.
- **Package**: `engine/work`
- **File**: [engine/work/tenant.go](file:///d:/project/kitwork/engine/work/tenant.go)
- **Type**: `type Tenant struct`

### 15. Scheduler
- **Definition**: App-owned cron scheduler (`work.CronScheduler`) that manages recurring background jobs according to cron expressions.
- **Package**: `engine/work`
- **File**: [engine/work/cron.go](file:///d:/project/kitwork/engine/work/cron.go)
- **Type**: `type CronScheduler struct`

### 16. Job
- **Definition**: Background work items in queue and scheduler subsystems (`work.QueueJob`, `work.CronJob`).
- **Package**: `engine/work`
- **Files**: [engine/work/queue.go](file:///d:/project/kitwork/engine/work/queue.go), [engine/work/cron.go](file:///d:/project/kitwork/engine/work/cron.go)
- **Types**: `type QueueJob struct`, `type CronJob struct`

### 17. State
- **Definition**: Persistent or generation state managers (`persist.Store`, `site.Generation` state, SQLite database state).
- **Packages**: `engine/utilities/persist`, `engine/site`
- **Files**: [engine/utilities/persist/persist.go](file:///d:/project/kitwork/engine/utilities/persist/persist.go), [engine/site/generation.go](file:///d:/project/kitwork/engine/site/generation.go)
- **Types**: `persist.Store`, `site.Generation`
