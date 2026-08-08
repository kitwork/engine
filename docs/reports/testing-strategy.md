# Kitwork Engine — Testing Strategy & Quality Assurance Plan

This document audits the current test suite of the Kitwork Engine and defines a comprehensive strategy for expanding test coverage across unit, integration, concurrency, fuzzing, property-based, and benchmark suites.

---

## 1. Existing Test Coverage Audit

```mermaid
graph TD
    CurrentTests[Current Test Corpus] --> Unit[Unit Tests: compiler/, runtime/, value/]
    CurrentTests --> Contract[Contract Tests: contract_test.go, STABILITY.md invariants]
    CurrentTests --> Fuzz[Fuzz Seeds: FuzzCompileVerifyExecute, FuzzVMDeterminism]
    CurrentTests --> Bench[Alloc Gates: TestHandlerCorpusAllocationBudgets]
    
    MissingCoverage[Missing / Weak Coverage] --> SafeVM[VM .safe() Hard Failure Rescue]
    MissingCoverage --> NestedCache[FileCache Indirect Dependency Graph Hash]
    MissingCoverage --> CronFail[Scheduler Recovery on Process Crash]
    MissingCoverage --> NativeLeak[Native Bridge Memory & Goroutine Leaks]
```

### Current Test Highlights
1. **Contract Tests ([contract_test.go](file:///d:/project/kitwork/engine/runtime/contract_test.go))**: Enforces bytecode freeze (`BytecodeVersion == 2`, opcode numbers, instruction table checksum).
2. **Determinism & Retention Tests ([determinism_test.go](file:///d:/project/kitwork/engine/runtime/determinism_test.go), [retention_test.go](file:///d:/project/kitwork/engine/runtime/retention_test.go))**: Asserts that reused/pooled VMs maintain byte-for-byte execution parity and zero memory retention.
3. **Allocation Budgets ([bench_handler_test.go](file:///d:/project/kitwork/engine/work/bench_handler_test.go))**: Enforces zero-allocation gates on hot HTTP request handler paths.
4. **Fuzzing Targets ([compiler_fuzz_test.go](file:///d:/project/kitwork/engine/compiler/compiler_fuzz_test.go))**: Fuzzes the compiler -> verifier -> VM execution pipeline.

---

## 2. High-Priority Missing Test Plan

The table below prioritizes crucial test additions to close coverage gaps.

| Priority | Test Name & Category | Component | Input Case | Expected Result | Target File to Add/Modify |
|---|---|---|---|---|---|
| **P0** | `TestSafeRescuesVMHardFailure` (VM Integration) | `runtime/` & `work/` | Script executing `fail("boom").safe()` or invalid DB query `.safe()`. | `.safe()` catches `Invalid` error and returns `{ ok: false, error: "boom" }` without HTTP 500 abort. | [engine/work/safe_rescue_test.go](file:///d:/project/kitwork/engine/work/safe_rescue_test.go) |
| **P0** | `TestFileCacheIndirectImportInvalidation` (Cache) | `compiler/` & `site/` | Modify nested imported dependency (`./lib/db.js`) without touching main `router.kitwork.js`. | Cache key changes, triggering source re-compilation instead of stale bytecode cache hit. | [engine/compiler/cache_test.go](file:///d:/project/kitwork/engine/compiler/cache_test.go) |
| **P1** | `TestConcurrentTenantIsolationSoak` (Concurrency) | `core/` & `app/` | 100 concurrent goroutines executing requests across 10 distinct tenant identities. | Zero cross-tenant data leakage in VM pool, database connections, or request scope. | [engine/core/engine_soak_test.go](file:///d:/project/kitwork/engine/core/engine_soak_test.go) |
| **P1** | `TestCronSchedulerCrashRecovery` (Resilience) | `work/` | Interrupt scheduler goroutine mid-job, then trigger restart. | Orphaned jobs marked failed; scheduled jobs resume without duplicate execution. | [engine/work/cron_persist_test.go](file:///d:/project/kitwork/engine/work/cron_persist_test.go) |
| **P1** | `TestInvalidBytecodeVerifierRejection` (Security) | `runtime/` | Hand-crafted corrupted bytecode with out-of-bounds jump offsets and invalid stack depths. | `runtime.Verify` rejects program with specific `VerifyError` before publication. | [engine/runtime/verify_test.go](file:///d:/project/kitwork/engine/runtime/verify_test.go) |
| **P2** | `TestParserErrorDiagnosticLocations` (Diagnostics) | `compiler/` | Syntax errors across multiline template literals and native import wrappers. | Structured diagnostic reports exact file, line, and column of syntax error. | [engine/compiler/diagnostic_test.go](file:///d:/project/kitwork/engine/compiler/diagnostic_test.go) |
| **P2** | `TestNativeBridgePanicRecovery` (Bridge) | `runtime/` & `value/` | Native Go capability function panicking with custom error struct. | Panic caught at native boundary, converted to `NATIVE_PANIC` diagnostic without crashing process. | [engine/runtime/failure_boundary_test.go](file:///d:/project/kitwork/engine/runtime/failure_boundary_test.go) |
| **P2** | `TestQueueWorkerHeartbeatTimeout` (Worker) | `work/` | Worker node stops sending heartbeats while holding active jobs. | Stale jobs reclaimed by active queue worker after timeout expiration. | [engine/work/queue_test.go](file:///d:/project/kitwork/engine/work/queue.go) |

---

## 3. Recommended Automated Test Commands & CI Pipeline

To enforce quality standards prior to merging code changes, the following verification commands must pass cleanly:

```bash
# 1. Standard build, test, and vet
go build ./...
go test ./...
go vet ./...

# 2. Concurrency race detection
go test -race ./...

# 3. Static engine preflight check
go run . check

# 4. Handler allocation budget gates
go test ./work -run TestHandlerCorpusAllocationBudgets
go test ./work -run '^$' -bench '^BenchmarkServeHandlerCorpus$' -benchmem

# 5. Pipeline Fuzzing (10s minimum)
go test ./compiler -run '^$' -fuzz FuzzCompileVerifyExecute -fuzztime=10s
go test ./runtime -run '^$' -fuzz FuzzVMDeterminism -fuzztime=10s

# 6. Opt-in Soak Testing
KITWORK_SOAK=1 go test ./runtime -run TestPooledVMSoakAcrossPrograms
KITWORK_SOAK=1 go test ./core -run TestEngineHotReloadSoak
```
