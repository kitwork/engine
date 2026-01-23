# Kitwork Engine: High-Performance Logical OS

> **"Nano-second Latency. Zero-Allocation Runtime. Logic as Infrastructure."**

**Kitwork Engine** is a high-performance stack-based virtual machine and DSL runtime, engineered in **Golang**. It is designed to eliminate I/O overhead and memory pressure, treating business logic as "infrastructure assets" that operate with near-native efficiency.

---

## ⚡ Performance Metrics

Kitwork Engine has been optimized at the hardware level to achieve record-breaking figures. The following benchmarks were conducted on standard hardware (Intel Core i7-11850H @ 2.50GHz):

| Metric | Result | Notes |
| :--- | :--- | :--- |
| **Throughput (Raw)** | **~21,000,000 ops/sec** | Internal stress test (Direct VM Loop) |
| **Throughput (API)** | **~454,000 req/sec** | Full HTTP Stack + Engine Execution (`BenchmarkAPIRaw`) |
| **Latency (Execution)**| **~40ns** | Pure Logic Execution Time |
| **Latency (E2E)** | **~2.3µs** | End-to-End HTTP Request Processing |
| **Allocations** | **0 bytes/op** | Zero-GC Runtime for Logic Execution |
| **Thread Safety** | **100% Lock-Free Read** | Hot-swappable Logic Blueprints |

> **Note on Benchmarks:** The API benchmark includes the overhead of `net/http/httptest`. The raw engine throughput is significantly higher, limited only by CPU memory bandwidth.

---

## 🏗️ Core Architecture: Blueprint vs Task

Kitwork solves the "Flexible but Fast" dilemma through a strict separation of concerns:

1.  **Work (Blueprint):** An **Immutable** object containing Bytecode, Constants, and Configuration. Created once during the `Build` phase and shared across millions of requests.
2.  **Task (Execution State):** A **Mutable** state container for each request (Params, Response, Context). Tasks are managed by a `sync.Pool`, ensuring **Zero-Allocation** during runtime by reusing memory.
3.  **Execution Context:** A pooled "super-object" pre-loaded with the VM and bounded system functions, allowing the engine to "cold start" a script in nanoseconds.

---

## 🚀 Key Features

-   **JavaScript-like DSL:** Write backend logic using familiar syntax with support for Chaining, Prototypes, and Functional patterns.
-   **Integrated Service Mesh:**
    -   `http()`: Ultra-fast external API calls.
    -   `db()`: Fluent Query Builder with "Magic Lambda Where" clause.
    -   `payload()`: Safe access to input parameters.
    -   `log()`: Context-aware structured logging.
-   **Smart Optimization:**
    -   **Zero-Copy Variable Access:** VM reads directly from system memory without scope copying.
    -   **Opcode Fusion:** Critical paths like `ADD`, `MUL`, `ITER` are optimized for CPU branch prediction.
    -   **Auto-Response:** Automatically detects return types (JSON, HTML) based on execution results.

---

## 🛠️ Usage Example

### 1. Complex Workflow (`demo/advanced_workflow.js`)

```javascript
const w = work("OrderProcessor")
  .router("POST", "/v1/process")
  .version("1.5.0");

let input = payload();
log("🚀 Starting process for user:", input.user_id);

// 1. Database Check (Fluent API)
let user = db().table("user").where("id", input.user_id).get();

if (user.len() == 0) {
    return { status: 404, error: "User not found" };
}

// 2. External Service Call
let fx = http().get("https://api.exchangerate.host/latest");

// 3. Logic & Persistence
let total = input.amount * 25000;
db().table("transactions").insert({ 
    user_id: input.user_id, 
    amount: total 
});

return { order_id: now().text(), total: total };
```

### 2. Integration with Go

```go
e := engine.New()
w, _ := e.Build(scriptContent)

http.HandleFunc("/api", func(rw http.ResponseWriter, r *http.Request) {
    params := map[string]value.Value{ "user_id": value.New(101) }
    
    // Zero-alloc, high-performance execution
    result := e.Trigger(context.Background(), w, params)
    
    json.NewEncoder(rw).Encode(result.Response.Interface())
})
```

---

## 📦 Getting Started

### Run Standard Benchmarks
Verify the performance on your own machine:
```bash
go test -bench=BenchmarkAPI -run=^$ -benchmem
```

### Run Demo Server (Port 8081)
Experience the engine in action:
```bash
go run cmd/server/main.go
```
*Access `http://localhost:8081/deploy` (Loads `raw.js` and other demos)*

---

## 🗺️ Roadmap

- [x] **Core VM (KVM):** Stack-based Bytecode VM with `ITER` support.
- [x] **Zero-Alloc Runtime:** Full `sync.Pool` implementation.
- [x] **Thread-Safety:** Blueprint/Task architecture.
- [x] **Native Connectors:** PostgreSQL (lib/pq) support.
- [ ] **JIT Compilation:** Compile critical hot paths to native machine code.
- [ ] **Binary Serialization:** Save/Load compiled bytecode to disk.

---

**Kitwork Engine** - Bringing hyperscale infrastructure logic to your fingertips. Optimized to the byte. Fast to the nanosecond.
