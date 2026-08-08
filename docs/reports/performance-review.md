# Kitwork Engine — Performance Review & Hotspot Analysis

This document analyzes the critical execution paths of the Kitwork Engine, identifying empirical bottlenecks, memory allocation patterns, lock contention points, and micro-optimization opportunities.

---

## 1. Hotspot & Performance Breakdown

```mermaid
graph TD
    Request[HTTP Request] --> Router[Route Matching (O(1) Tree Lookup)]
    Router --> VMLease[VM Acquisition (sync.Pool - Zero Alloc)]
    VMLease --> Interpreter[Bytecode Dispatch Loop (Direct Spec Table Lookup)]
    Interpreter --> ValAlloc[Value Operations (24-byte Tagged Union)]
    Interpreter --> NativeBridge[Native Reflection (Cached Method Descriptors)]
    Render[Render Engine] --> ZeroCopy[Zero-Copy I/O (io.Copy Stream)]
```

### Empirical Performance Gates
- **Zero-Allocation HTTP Serving**: `work/bench_handler_test.go` enforces strict allocation budgets (`TestHandlerCorpusAllocationBudgets`). `BenchmarkServeHandlerEngine` removes `httptest` overhead to measure true engine allocation.
- **Zero-VM Static Serving**: Files requested from tenant `/public/` or `/assets/` bypass VM compilation and bytecode execution entirely, streaming bytes via `io.Copy`.

---

## 2. Analysis of Critical Execution Paths

### 2.1 Lexer & Parser Performance
- **Optimizations Implemented**:
  - `Table[256]` character lookup table replaces runtime character range checking (`isAlpha`, `isDigit`) with O(1) byte classification.
  - `lexerPool` (`sync.Pool`) recycles `Lexer` instances to eliminate GC overhead during compilation.
  - Keyword values are pre-instantiated in static maps (`valKeywords`) during `init()`.
- **Potential Bottleneck**: String conversion via `unsafe.Pointer(&b)` during string token reading avoids heap copying but relies on unsafe pointer operations.

### 2.2 Bytecode Interpreter Dispatch Loop
- **Optimizations Implemented**:
  - Opcode table (`instructionTable[256]`) stores pre-computed opcode metadata (`InstructionSpec`: width, energy, stack delta).
  - Pre-verified bytecode eliminates bounds checking and operand validation inside the interpreter `for` loop.
  - Cancellation check is throttled to once every 64 opcodes (`instructions & 63 == 0`), avoiding atomic context checks on every opcode step.
- **Potential Bottleneck**: `switch op` inside [interpreter.go:L172](file:///d:/project/kitwork/engine/runtime/interpreter.go#L172) compiles to a jump table in Go. While fast, computed `goto` (indirect branch) cannot be used in Go stdlib.

### 2.3 Value Allocation & Memory Footprint
- **24-Byte Structure**:
  ```go
  type Value struct {
      N        float64     // 8 bytes (numeric scalar)
      V        any         // 16 bytes (interface header: type pointer + data pointer)
      K        Kind        // 1 byte (Kind tag)
      IsError  bool        // 1 byte (Error flag)
      ErrorVal any         // 16 bytes (optional attached error) -> expanded struct
  }
  ```
- **Performance Trade-offs**:
  - Storing numeric scalars in `N` avoids heap allocation for numbers.
  - Complex structures (maps, arrays, lambdas) use interface pointer `V`. Slice operations on `[]value.Value` require pointer unwrapping.

### 2.4 Function Calls & VM Frame Allocation
- **Optimizations Implemented**:
  - `vm.Frames` is a fixed-size pre-allocated array (e.g. 64 frames). Function calls bump `vm.FrameIdx++` without heap allocating `Frame` objects.
  - Recycled frames reuse their `f.Vars` map by calling Go 1.21 `clear(f.Vars)`.
- **Potential Bottleneck**: When `frame.captured == true` (escaped closure), frame recycling must allocate a fresh map (`make(map[string]value.Value)`), creating heap allocation on closure instantiation.

### 2.5 Native Bridge Reflection
- **Optimizations Implemented**:
  - Reflection metadata (type descriptors, method indices) is cached globally to minimize `reflect.ValueOf` introspection.
- **Potential Bottleneck**: Invoking arbitrary Go struct methods via `reflect.Call` still incurs interfaceboxing and reflect stack copy costs compared to direct `NativeFunc` function pointers.

### 2.6 Router Tree Matching
- **Optimizations Implemented**:
  - Route graphs are prepared and compiled once during `site.Generation` publication.
  - Route tree resolution uses segment-based trie traversal without regex matching on static routes.

---

## 3. Allocation & Contention Audit Findings

| Path | Area | Finding | Impact | Recommendation |
|---|---|---|---|---|
| **VM Pooling** | Memory | `ResetForPool` clears frames and stacks. Oversized stack slices (>1024 items) are resliced to default capacity. | Prevents VM pool memory bloat. | Proven effective in `retention_soak_test.go`. |
| **Engine Lock** | Concurrency | `Engine.mu` read lock is acquired per HTTP request to fetch `cachedTenant`. | Low contention under read lock. Hot reload write lock holds `Engine.mu` during generation swap. | Minimize work done inside `Engine.mu` write lock during hot reload. |
| **Database Conversion** | Database | DB row scanning constructs `map[string]value.Value` per SQL row. | Allocation scales with query result row count. | Use pooled slice buffers for batch database row reading. |
| **String Concatenation** | Operator `ADD` | `left.Add(right)` on strings performs standard string allocation. | Heap allocation on repeated string concatenation. | Recommend template string literals or array `join()`. |
