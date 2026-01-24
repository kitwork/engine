# 🚀 Kitwork Engine
> **"Logic as Infrastructure. Nanosecond Latency. Zero-GC Runtime."**

![Go Version](https://img.shields.io/badge/go-1.21%2B-00ADD8?style=flat-square&logo=go)
![Architecture](https://img.shields.io/badge/arch-stack--vm-orange?style=flat-square)
![License](https://img.shields.io/badge/license-MIT-blue?style=flat-square)
![Build Status](https://img.shields.io/badge/build-passing-success?style=flat-square)

**Kitwork Engine** is a high-performance, embedded scripting runtime designed for modern backend systems. It bridges the gap between the raw speed of **Go** and the dynamic flexibility of **JavaScript**, enabling you to hot-swap business logic without recompiling or restarting your infrastructure.

---

## ✨ Why Kitwork?

Unlike traditional embedded engines (Otto, GopherLua) that prioritize compatibility over speed, Kitwork is engineered for **hyperscale throughput**:

*   **⚡ Zero-Allocation Runtime**: Tasks and contexts are aggressively pooled (`sync.Pool`), ensuring **0 bytes/op** garbage collection overhead during hot-path execution.
*   **🏎️ Custom Stack-Based VM**: Optimized specifically for backend I/O orchestration, executing logic in ~40ns.
*   **🛠️ Developer Experience**: Write standard JavaScript (ES6+ inspired) with modern features like Arrow Functions, Destructuring, and Template Literals.

---

## ⚡ Performance Markers

Engineered for speed on standard hardware (*Intel Core i7-11850H*):

| Metric | Result | Context |
| :--- | :--- | :--- |
| **Throughput (Raw)** | **~21,000,000 ops/sec** | Direct Bytecode Execution |
| **Throughput (HTTP)** | **~454,000 req/sec** | Full API Stack + Logic Engine |
| **Latency (Core)** | **~40ns** | Pure Logic Execution Time |
| **Overhead** | **0 bytes** | Zero-GC per request (Pooled) |

---

## 🚀 Key Features

### 1. Modern Syntax Support
Kitwork supports a rich subset of ES6+, making it instantly familiar to developers.

*   **Destructuring Assignment**:
    ```javascript
    const { user, config } = data;
    ```
*   **Arrow Functions**:
    ```javascript
    const add = (a, b) => a + b;
    ```

### 2. Built-in Concurrency (Experimental)
Execute blocking I/O operations in parallel using Go's native goroutines, seamlessly exposed to the scripting layer.

```javascript
// Fetch data from multiple sources concurrently
const { user, stock } = parallel({
    user: () => http().get("/api/user/101"),
    stock: () => db().table("inventory").where("id", 101).get()
});
```

### 3. Integrated "Magic" Service Mesh
Core infrastructure primitives are built-in as zero-overhead intrinsics:
*   **`db()`**: Fluent query builder for PostgreSQL/SQLite.
*   **`http()`**: High-performance HTTP client.
*   **`go()`**: Fire-and-forget background processing.
*   **`defer()`**: Resource cleanup hooks.

---

## 🛠️ Usage Example

Define your logic in `.js` files. The engine hot-loads code into efficient bytecode blueprints.

```javascript
// work/order_processor.js
work("OrderProcessor")
    .router("POST", "/v1/process")
    .handle(() => {
        // 1. Parse Input
        const { userId, sku, amount } = payload();

        // 2. Parallel Data Fetching
        const { user, product } = parallel({
            user: () => db().table("users").where("id", userId).first(),
            product: () => db().table("products").where("sku", sku).first()
        });

        if (!user || !product) {
            return { status: 404, error: "Invalid Order" };
        }

        // 3. Business Logic
        if (product.stock < amount) {
            return { status: 400, error: "Insufficient Stock" };
        }

        // 4. Transaction (Atomic)
        const total = product.price * amount;
        
        defer(() => log("Audit: Order processed for " + userId));

        return { 
            status: 200, 
            orderId: uuid(), 
            total: total 
        };
    });
```

---

## 📦 Getting Started

### 1. Run the Demo Server
Boot the engine with the included example workflows:

```bash
go run cmd/server/main.go
```
The server will start on port `8080`, exposing routes defined in the `demo/` folder.

### 2. Run Benchmarks
Verify the performance claims on your local machine:

```bash
go test -bench=BenchmarkAPI -run=^$ -benchmem ./...
```

---

## 🗺️ Roadmap & Status

*   **Current Version**: v0.9.0 (Beta)
*   **Architecture**:
    *   [x] **Core VM**: Stack-based, Thread-safe.
    *   [x] **Compiler**: Multi-pass AST compilation.
    *   [x] **Features**: Destructuring, Arrow Fns, Parallel.
*   **Upcoming**:
    *   [ ] **JIT Compiler**: Compile hot paths to native Assembly.
    *   [ ] **LSP Server**: Integrated language server for VS Code.

---

**Kitwork Engine** - *Logic as Infrastructure.*
Distributed under the MIT License.