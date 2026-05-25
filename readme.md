# 🚀 Kitwork Engine
> **The Industrial-Grade Logic Operating System for Sovereign Execution.**

![Go](https://img.shields.io/badge/go-1.25+-black?style=flat-square&logo=go)
![Speed](https://img.shields.io/badge/latency-70ns-black?style=flat-square)
![Throughput](https://img.shields.io/badge/ops-14M--s-black?style=flat-square)
![Status](https://img.shields.io/badge/status-production--ready-blue?style=flat-square)

Kitwork is a ultra-high-performance, stack-based bytecode engine designed to replace the modern cloud stack with **Living Logic**. It prioritizes Developer Experience (DX) and native-level security without compromising on hardware-level execution speeds.

---

## ⚡ Performance Dashboard
We don't just build; we engineer. Kitwork is optimized for **Nanosecond-Precision High-Frequency Logic**.

| Category | Indicator | Benchmark | Comparison |
| :--- | :--- | :--- | :--- |
| **🚀 VM CORE** | Instruction Clock | **~14,100,000 ops/s** | Raw Bytecode Velocity |
| | Logic Latency | **70ns** | Pure Execution Precision |
| **🗄️ DATABASE** | Query Construction | **230ns** | **20x Faster** than GORM |
| | Memory Overhead | **0 B/op** | **Zero-GC** Architecture |
| **⚙️ SYSTEM** | Cold Boot Time | **< 10ms** | Instant-Scale Serverless |
| | GC Pressure | **Near Zero** | Aggressive Resource Pooling |

---

## 💎 The Code Synthesis
Initialize your world with a single destructuring call. Kitwork provides a unified ecosystem for your logic.

```javascript
/* 1. Initialize & Destructure */
const { router, log, render, http, database, go } = kitwork();

/* 2. ACID Database Transactions (Atomic Blocks) */
const db = database.connection();
db.atomic((tx) => {
    // Both creations run atomically inside the same transaction
    tx.table("accounts").create({ user_id: 1, balance: 1000 });
    tx.table("accounts").create({ user_id: 2, balance: 500 });
    
    // Automatically rolls back if an error occurs, if the block panics,
    // or if a statement returns an Invalid type (e.g. invalid JSON parsing)
    if (maintenanceWindowActive) {
        return JSON.parse("{"); // Invalid JSON parses into value.Invalid -> Triggers Rollback!
    }
});

/* 3. Fluid Routing & Logic Hooks */
router.get("/api/v1/orders/:id")
    .cache("30s") // Smart context-aware LRU cache
    .handle((req, res) => {
        const order = database.orders.find(req.params("id"));
        if (!order) return res.status(404).json({ error: "Order not found" });
        
        return res.json({ success: true, data: order });
    })
    .done(res => log.Print(`Request fulfilled: ${res.status}`))
    .fail(err => log.Print(`Emergency Halt: ${err}`));

/* 4. Background Execution (Go-Routine Pattern) */
go(() => {
    log.Print("Asynchronous processing started...");
    database.logs.create({ msg: "System Sync Started", time: now() });
});
```

---

## 🔒 Security Sandboxing & Developer Experience

### 1. Execution Budgets (`MaxEnergy`)
Spam and resource exhaustion attacks are solved at the engine level. Kitwork tracks the energy cost of every byte and instruction. 
If a VM execution block runs into an infinite loop or consumes too much processing power, the engine halts the execution cleanly and returns an `Energy Limit Exceeded` error before any system thread gets locked up.

### 2. Stack Overflow Protection
Recursion depth is actively checked during every method and function call inside the VM. Stack depths exceeding a threshold (typically `64`) are intercepted, triggering a structured stack overflow warning instead of causing Go runtime panic or segmentation faults.

### 3. Source Line Mapping
Kitwork bridges the gap between Go bytecode execution and high-fidelity debugging. 
- The compiler maps every instruction pointer back to its original token byte offset.
- Lexical positions are binary-searched ($O(\log N)$) against line starts to resolve the exact source file line.
- Stack traces cleanly report the precise Javascript file line numbers of errors and exceptions.

---

## 🔄 Resilient Hot-Reloading
Deploy logic code with peace of mind. Kitwork incorporates a non-blocking hot-reload mechanism that ensures **Zero-Downtime Operations**:

*   **Immutable Swapping**: New logic code is compiled in isolation inside a temporary environment. The memory cache pointer is updated atomic-style only upon successful validation.
*   **Compile Fallback**: If an uploaded script has syntax errors or is truncated during a remote transfer, the compilation fails gracefully, and the engine continues serving the old, stable version of the code.
*   **I/O Optimization**: Stat operations on the file watcher are throttled to a minimum interval of 1 second, preserving system file handles and minimizing system resource overhead.

---

## 🗄️ Database Transactions (ACID)
Database operations inside the VM leverage Go's native SQL connection pool and transactions:
*   **Proxy-less Execution**: Go's `*sql.DB` and `*sql.Tx` are abstracted behind an internal `sqlExecutor` interface. Query builders interact with the transaction context transparently.
*   **Fail-Safe Cleanups**: Connection pool leaks are prevented by deferred rollback wrappers. If a JS lambda panics, execution fails, or a Go routine panics, the transaction is automatically rolled back, keeping database locks clean.

---

## 🌟 Advanced Orchestration

### 1. Smart Resource Mapping (Zero-VM Path)
Bypass the script engine entirely for static assets. Kitwork maps local directories to global routes with OS-native speed.
```javascript
router.get("/static/*").directory("./public/assets");
router.get("/favicon.ico").file("./public/favicon.ico");
```

### 2. Natural Language Scheduling
Forget Cron syntax. Define temporal logic in human-readable sequences.
```javascript
kitwork().schedule()
    .daily("01:00")         // Run at 1 AM daily
    .weekly("MON 08:30")    // Weekly sync every Monday morning
    .every("10s")           // High-frequency polling
    .handle(() => database.logs.where(l => l.age > "30d").destroy());
```

### 3. Logic-Aware Templating
Compile templates directly into Go Bytecode. The view receives processed data, maintaining a strict logic-less architecture.
```javascript
const home = render("/pages/home")
    .layout("/layouts/home")
    .global({ title: "Kitwork Dashboard" });

router.get("/").handle((req, res) => {
    const data = { users: database.user.list(5) };
    return res.html(home.bind(data));
});
```

---

## 🏁 Fast Path
Get into the flow in under 60 seconds.

```bash
# Clone the repository
git clone https://github.com/kitwork/engine

# Start the industrial server
go run cmd/server/main.go

# Access the system
# => System Online at http://127.0.0.1:8081
```

---

## ✒️ Author's Note

> *"While the world is busy using AI to automate everything, I choose to breathe a soul into every line of code. I write code like essays, like unspoken confessions. I use AI not to replace myself, but as a mirror to reflect my own inner world. I expose this system to the world simply because it is beautiful, crazy, and dreamy."*
> 
> — **Huỳnh Nhân Quốc**, *Indie-Stack Developer*

---
*© 2024-2026 Kitwork Foundation. Industrial Logic Infrastructure.*