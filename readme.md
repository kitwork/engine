# 🚀 Kitwork Engine
> **"High-Performance Execution Engine for Complex Business Logic."**

![Go Version](https://img.shields.io/badge/go-1.21%2B-00ADD8?style=flat-square&logo=go)
![Architecture](https://img.shields.io/badge/arch-stack--vm-orange?style=flat-square)
![Efficiency](https://img.shields.io/badge/gc-zero--pressure-green?style=flat-square)
![License](https://img.shields.io/badge/license-MIT-blue?style=flat-square)

**Kitwork Engine** is an industrial-grade logic infrastructure designed for high-concurrency systems. It provides a specialized runtime to execute complex workflows with nanosecond precision, bridging the gap between low-level Go performance and high-level developer productivity.

---

## 📚 Table of Contents
- [🚀 Quick Start](#-quick-start)
- [🧠 Core Philosophy](#-core-philosophy)
- [🗄️ Database (Industrial Query Builder)](#️-database-industrial-query-builder)
- [🎨 JIT CSS Engine](#-jit-css-engine)
- [🌐 Web Stack Primitives](#-web-stack-primitives)
- [⚡ Industrial Concurrency](#-industrial-concurrency)
- [📦 Explicit Caching System](#-explicit-caching-system)
- [☁️ Cloud-Native Built-ins](#️-cloud-native-built-ins)
- [🛠️ Performance Markers](#-performance-markers)
- [⚙️ Modular Configuration](#️-modular-configuration)

---

## 🚀 Quick Start
Get up and running in under 30 seconds:
```bash
git clone https://github.com/kitwork/engine
go run cmd/server/main.go
# Server online at http://localhost:8081
```

Define your first Logic Work (`demo/api/hello.js`):
```javascript
work("Service")
  .get("/hello", () => {
    return { 
        status: "Online", 
        engine: "Kitwork 1.0",
        entropy: random(1000) 
    };
  });
```

---

## 🧠 Core Philosophy
*   **Nanosecond Execution**: A proprietary stack-based VM that executes bytecode instructions in ~70ns.
*   **Zero-Copy Logic**: Data moves through the system without redundant allocations.
*   **Logic as Infrastructure**: Your business logic is decoupled from the server implementation.
*   **Zero-GC Pressure**: The engine pools task contexts and VM stacks, ensuring consistent performance without GC pauses.
*   **Agent-Native Design**: Built to be easily manipulated by AI agents while remaining 100% predictable for human developers.

---

## 🗄️ Database (Industrial Query Builder)
A high-performance SDK designed for complex logic execution. Kitwork leverages **Parameter-based Schema Inference** and **Operator Persistence** to eliminate boilerplate while maintaining 100% predictable SQL output.

### 🌟 Modern Entity-Style Syntax
Kitwork introduces **Proxy-based Entity Resolution**, allowing you to access tables as if they were native properties.
```javascript
// 1. Fetch exactly one record (ID or Lambda)
const user = db.user.find(1);
const active = db.user.find(u => u.email == "admin@kitwork.io");

// 2. Multi-Database support with property chaining
const remoteUser = db("secondary").user.limit(5).list();

// 3. Batch lookup using automatic Set Inclusion (IN)
const activeItems = db.products.where(p => p.id == [10, 20, 30]).list();
```

### ✍️ Writing Data (Strict & Returning)
Data writing operations in Kitwork are safe and return full objects from the database by default.
```javascript
// 1. Create: Returns the FULL OBJECT (including auto-generated id, created_at)
const newUser = db.user.create({
    username: "kitwork_pro",
    email: "pro@kitwork.io"
});

// 2. Update: STRICT MODE (Requires .where() for security)
const updated = db.user
    .where(u => u.id == newUser.id)
    .update({ is_active: true });

// 3. Delete vs Destroy
db.user.where(u => u.id == 1).delete();  // Soft Delete (sets deleted_at)
db.user.where(u => u.id == 1).destroy(); // Hard Delete (physical removal)
```

---

## 🎨 JIT CSS Engine
Kitwork includes a high-performance **Just-In-Time (JIT) CSS Generator** written in Go. It scans your HTML files and generates only the utility classes you actually use, ensuring minimal bundle size and maximum performance.

### ⚡ Key Features:
- **Zero-Config Generation**: No complex Webpack/Vite setups required.
- **Atomic Reliability**: Every utility class is mapped to deterministic CSS rules.
- **Dynamic 3D Support**: Native support for 3D components like cubes, including responsive sizing and duration.

```bash
# Generate CSS on the fly
go run demo/css_jit_demo.go
```

Example Utility Usage:
```html
<div class="cube-area cube-size-120 tablet:cube-size-80">
    <div class="cube-block cube-rotate-center">
        <!-- 3D Logic Visuals -->
    </div>
</div>
```

---

## 🌐 Web Stack Primitives

### Zero-Copy Routing
Kitwork uses a high-performance Trie-based router for maximum throughput.
```javascript
work("App")
    .get("/users", listUsers)           // Static
    .get("/users/:id", getUser)         // Dynamic: params("id")
    .post("/users", createUser)          // POST Payload
```

### Request & Response Mapping
| Function | Description | Example |
| :------- | :---------- | :------ |
| `payload()` | GET/POST combined payload. | `const data = payload()` |
| `query(key?)`| URL Query parameters. | `const page = query("page")` |
| `params(key?)`| Route dynamic segments. | `const id = params("id")` |
| `header(key)` | Request headers. | `const auth = header("Authorization")` |
| `body()` | Full Raw Body or JSON. | `const raw = body()` |
| `status(code)`| Set HTTP status code. | `status(201)` |
| `redirect(url)`| Immediate redirect. | `redirect("/home")` |
| `cookie(k, v)`| Secure cookie management. | `cookie("token", val, { secure: true })` |

---

## ⚡ Industrial Concurrency
High-concurrency logic made simple and safe.

### High-Performance Parallelism
Execute independent tasks concurrently to maximize I/O utilization.
```javascript
const { user, profile } = parallel({
    user: () => db.user.find(1),
    profile: () => http.get("https://api.svc/profile/1")
});
```

### Advanced Flow Control
*   **`go(() => ...)`**: Dispatch heavy tasks to background workers.
*   **`defer(() => ...)`**: Lifecycle management to run logic **after** the response is sent.

---

## 📦 Explicit Caching System
Predictable caching with human-readable duration strings (e.g., "1d", "1h30m").
```javascript
const data = cache("top_sales", "1h", () => {
    return db.orders.where(o => o.amount > 1000).list();
});
```

---

## ☁️ Cloud-Native Built-ins
System utility functions designed for Agentic Workflows:
*   **`random()`**: 
    - `random(n)`: Integer 0..n-1.
    - `random(min, max)`: Integer range.
    - `random(array)`: Random selection from array.
    - `random()`: Float 0..1.
*   **`now()`**: Returns system real-time (Proxy).
*   **`readfile(path)`**: High-speed file reading (I/O optimized).
*   **`log(...args)`**: Context-aware logging.

---

## 🛠️ Performance Markers
Kitwork is built for speed. Period.

| Metric | Result | Context |
| :----- | :----- | :------ |
| **Instruction Speed** | **~14,112,000 ops/s** | Raw VM Instruction Throughput |
| **Logic Complex Ops** | **~605,000 ops/s** | Real-world data transformation |
| **VM Overhead** | **~70ns** | Pure execution latency |
| **GC Pause Impact** | **Near-Zero** | Pooled resources architecture |

---

## ⚙️ Modular Configuration
Enterprise-ready modular setup for scaling databases and services.
```yaml
port: 8081
debug: true
source: ["./demo/api"]
# Modular Database Configuration
databases: 
  - "config/database/master.yaml"
# Modular SMTP Configuration
smtps: ["config/smtp/service.yaml"]
```

---

## 👨‍💻 Foundation & Architecture
> **"Performance is not an afterthought; it is the infrastructure."**

## 👨‍💻 Logic Engine Architect
**Huỳnh Nhân Quốc**
- ⚙️ Core Engine & Bytecode Development
- ⚡ High-Performance Runtime (Golang)
- 📜 Scripting Syntax & Logic Design
- 🚀 [Kitwork](https://kitwork.io) & [Engine](https://github.com/kitwork/engine)

**Support Development** → [Sponsor KitWork / Huỳnh Nhân Quốc](https://github.com/sponsors/huynhnhanquoc)