# Kitwork Engine — Executive Summary

## 1. Top 10 Key Findings

1. **Custom Lightweight Virtual Machine**: Kitwork successfully implements a sovereign, hand-written stack-based Go VM running a deliberate JavaScript subset, completely eliminating V8 or CGO runtime dependencies.
2. **Strict Pre-Publication Bytecode Verification**: Every compiled script passes a 4-pass verifier (`runtime.Verify`) before activation, guaranteeing stack availability, branch consistency, and valid jump targets prior to execution.
3. **Zero-Allocation HTTP Hot Paths**: Reusable VM pools (`app.Pool`), FastReset recycling, pre-compiled filesystem route trees (`RouteTree`), and prepared view render plans (`RenderPlan`) achieve zero allocation on hot HTTP request paths.
4. **Hierarchical Ownership Model**: Production runtime enforces a clear 4-tier hierarchy: `Host -> AppRuntime(identity) -> SiteRuntime(domain) -> Generation(version) -> RequestScope(HTTP request)`.
5. **Native Import Bundler**: Built-in AST-level module bundler (`compiler.nativeBundle`) resolves relative imports and wraps files in IIFE module structures without requiring external build tools.
6. **VM Interpreter `.safe()` Execution Gap**: A major discrepancy exists where `.safe()` is designed to catch hard `Invalid` errors at the `value` layer, but the VM interpreter's post-opcode `peek().K == Invalid` check aborts execution before `INVOKE("safe")` can be called.
7. **Cast Method Shadowing Hazard**: `value.Invoke` evaluates scalar cast methods (`int`, `string`, `json`, `len`) before map key lookup, requiring `Proxy` objects (e.g. `env`) to prevent map key shadowing.
8. **App-Owned Background Architecture**: Cron schedulers, SQLite connection pools, and detached background tasks (`kitwork().go()`) are owned by `app.Runtime`, ensuring site generation reloads do not interrupt background work.
9. **Zero-VM Static Asset Delivery**: Request paths serving static assets (`/public/` or `/assets/`) bypass the VM entirely, streaming files directly from disk via zero-copy `io.Copy`.
10. **Preflight Parity Tool**: `kitwork check` validates filesystem route graphs, compiles cron sources, and round-trips compiled bytecode through the verifier without opening network listeners or starting schedulers.

---

## 2. Top 5 Critical Technical Risks

1. **Unreachable `.safe()` Error Rescue in VM**: Scripts calling `fail().safe()` or handling database query failures cannot catch hard errors in JS, resulting in unhandled 500 HTTP responses. ([technical-risks.md#risk-1](file:///C:/Users/huynh/.gemini/antigravity-ide/brain/74f48378-4ea7-4577-bb91-1aefb3f9689a/technical-risks.md))
2. **Potential Lock Inversion During Host Shutdown**: `Engine.Close()` holds the engine lock while calling `Tenant.Close()`, creating potential deadlock conditions under heavy concurrent request drains. ([technical-risks.md#risk-5](file:///C:/Users/huynh/.gemini/antigravity-ide/brain/74f48378-4ea7-4577-bb91-1aefb3f9689a/technical-risks.md))
3. **Escaped Closure Memory Retention**: Escaping lambdas capturing local frame scopes (`frame.captured = true`) pin frame variable maps in memory as long as the lambda reference is alive. ([technical-risks.md#risk-2](file:///C:/Users/huynh/.gemini/antigravity-ide/brain/74f48378-4ea7-4577-bb91-1aefb3f9689a/technical-risks.md))
4. **Cast Method Shadowing on Custom Maps**: Accessing map properties named `"int"` or `"string"` executes scalar cast methods instead of fetching property values. ([technical-risks.md#risk-3](file:///C:/Users/huynh/.gemini/antigravity-ide/brain/74f48378-4ea7-4577-bb91-1aefb3f9689a/technical-risks.md))
5. **Stale Bytecode Risk on Indirect Dependencies**: `FileCache` key generation must guarantee recursive SHA-256 fingerprinting for nested imported module files (`./lib/utils.js`). ([technical-risks.md#risk-4](file:///C:/Users/huynh/.gemini/antigravity-ide/brain/74f48378-4ea7-4577-bb91-1aefb3f9689a/technical-risks.md))

---

## 3. Top 5 Architecture Strengths

1. **Stdlib-Only Sovereign Go Design**: Zero third-party Go module dependencies ensure high security, reproducible builds, and complete stack ownership.
2. **Statically Analyzable JS Subset**: Enforcing arrow-only functions, no `while` loops, and no `try-catch` makes scripts deterministic, gas-boundable, and statically analyzable.
3. **Atomic Generation Publication**: Generation swaps (`site.Generation`) replace route trees, `.env` snapshots, and HTML render plans atomically without dropping in-flight request leases.
4. **Energy Metering & Gas Protection**: Mandatory per-opcode energy accounting (`MaxEnergy`) and context cancellation checks prevent infinite loops and runaway CPU consumption.
5. **Layered Isolation Boundaries**: Clear separation between process host, identity `AppRuntime`, domain `SiteRuntime`, and per-request `RequestScope`.

---

## 4. Top 10 Actionable Next Steps

1. **Fix VM `.safe()` Execution Path**: Defer `Invalid` stack aborts to `COMMIT` or implement a dedicated opcode so JS code can catch hard errors using `.safe()`.
2. **Refactor Engine Shutdown Lock Bounds**: Move `Tenant.Close()` execution outside `Engine.mu` lock inside `Engine.Close()`.
3. **Fix `value.Invoke` Key Shadowing**: Check map property existence prior to built-in scalar cast lookup.
4. **Add P0/P1 Integration Tests**: Implement `TestSafeRescuesVMHardFailure` and `TestConcurrentTenantIsolationSoak`.
5. **Implement Indirect Import Cache Key Fingerprinting**: Expand `FileCache` to recursively hash nested imported module files.
6. **Deprecate Legacy `.result()` and `ctx.render()`**: Remove outdated comments and align API documentation.
7. **Reconcile `ARCHITECTURE.md` Documentation**: Update historical RFC docs to accurately reflect the production filesystem route tree (`router.kitwork.js`).
8. **Add Cron Scheduler Crash Recovery Tests**: Verify background job durability during simulated SIGKILL events.
9. **Expose Prometheus Health Metrics**: Wire `RuntimeHealthSnapshot` to an HTTP `/health` telemetry endpoint.
10. **Publish Kitwork JS Subset Language Specification**: Create `docs/LANGUAGE_SPEC.md` documenting language subset rules and constraints.

---

## 5. Unconfirmed Areas Requiring Empirical Validation

1. **Multi-Node Cluster Placement**: Code references tenant placement and cluster migration, but multi-node clustering remains unimplemented in current engine code.
2. **Capsule Write Permissions (Tier ③ Write Grants)**: Read-only logic capsules are documented in RFCs; write/delete identity capability grants require further production validation.
3. **Extreme Load Lock Contention**: `Engine.mu` read-lock overhead under 100,000+ concurrent requests requires verification via multi-threaded load tests.
