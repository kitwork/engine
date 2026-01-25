# 🚀 Kitwork Engine
> **"High-Performance Execution Engine for Complex Business Logic."**

![Go Version](https://img.shields.io/badge/go-1.21%2B-00ADD8?style=flat-square&logo=go)
![Architecture](https://img.shields.io/badge/arch-stack--vm-orange?style=flat-square)
![Efficiency](https://img.shields.io/badge/gc-zero--pressure-green?style=flat-square)
![License](https://img.shields.io/badge/license-MIT-blue?style=flat-square)

**Kitwork Engine** is an industrial-grade logic infrastructure designed for high-concurrency systems. It provides a specialized runtime to execute complex workflows with nanosecond precision, bridging the gap between low-level Go performance and high-level developer productivity.

## 📚 Table of Contents
- [🚀 Quick Start](#-quick-start)
- [🧠 Core Concepts](#-core-concepts)
- [🌐 Web Stack Primitives](#-web-stack-primitives)
  - [Zero-Copy Routing](#zero-copy-routing)
  - [Request & Response Mapping](#request--response-mapping)
  - [Security & Cookies](#security--cookies)
- [🗄️ Database (Structured Query Builder)](#️-database-structured-query-builder)
  - [Parameter-based Schema Inference](#parameter-based-schema-inference)
  - [Smart Operator Inference](#smart-operator-inference)
  - [Analytical Terminal Methods](#analytical-terminal-methods)
- [⚡ Industrial Concurrency](#-industrial-concurrency)
- [📦 Explicit Caching System](#-explicit-caching-system)
- [🛠️ Performance Markers](#-performance-markers)
- [⚙️ Modular Configuration](#️-modular-configuration)

## 🚀 Quick Start
Get up and running in under 30 seconds:
```bash
git clone https://github.com/kitwork/engine
go run cmd/server/main.go
# Server online at http://localhost:8100
```

Define your first Logic Work (`demo/api/hello.js`):
```javascript
work("Service")
  .get("/hello", () => {
    return { status: "Online", engine: "Kitwork 1.0" };
  });
```

## 🧠 Core Concepts
*   **Nanosecond VM**: A proprietary stack-based VM that executes bytecode instructions in ~70ns.
*   **Logic as Infrastructure**: Your business logic is decoupled from the server implementation.
*   **Zero-GC Pressure**: The engine pools task contexts and VM stacks, ensuring consistent performance without GC pauses.

## 🌐 Web Stack Primitives

### Zero-Copy Routing
Kitwork uses a high-performance Trie-based router for maximum throughput.
```javascript
work("App")
    .get("/users", listUsers)           // Static
    .get("/users/:id", getUser)         // Dynamic Parameter
    .post("/users", createUser)          // POST Payload
    .put("/users/:id/status", update)    // Method-based mapping
```

### Request & Response Mapping
| Function | Description | Example |
| :------- | :---------- | :------ |
| `params(key)` | Dynamic path parameters. | `const id = params("id")` |
| `query(key)` | URL query string data. | `const p = query("page")` |
| `body(key?)` | JSON request payload (Lazy-load). | `const email = body("email")` |
| `header(key)` | Request headers. | `const auth = header("Authorization")` |
| `status(code)` | Set HTTP status code. | `status(201)` |
| `redirect(url)`| Immediate redirect. | `redirect("/home")` |

### Security & Cookies
Modern security defaults out-of-the-box.
```javascript
cookie("session_id", "secret-token", {
    httpOnly: true, // XSS Prevention
    secure: true,   // HTTPS only
    maxAge: 3600    // 1-hour expiration
});
```

## 🗄️ Elite ORM (The Magic DB)
The most intuitive database SDK ever built for a scripting environment.

### 🌟 Magic Lambda Join (Double Inference)
Parameters in Kitwork aren't just variables; they are **Schema References**. The engine infers join logic directly from your parameter names.
```javascript
// Variable 'orders' automatically maps to table "orders"
db().from("users")
    .join((orders, users) => orders.user_id == users.id)
    .take();
```

### 🧠 Smart Operator Inference
Stop typing `.like()` or `.in()`. Let the data speak for itself.
*   **Auto-Pattern**: `u.name == "Apple%"` ➔ `WHERE name LIKE 'Apple%'`
*   **Auto-Set**: `u.id == [1, 2, 3]` ➔ `WHERE id IN (1, 2, 3)`

### 🛠 Elite Terminal Methods
| Method | Description | SQL Result |
| :----- | :---------- | :--------- |
| **`.take(n?)`** | Execute the query and return Array. | `SELECT * LIMIT n` |
| **`.one()`** | Return a single Object or null. | `LIMIT 1` |
| **`.last()`** | Smart ordering to fetch latest record. | `ORDER BY id DESC LIMIT 1` |
| **`.find(id/fn)`** | Lookup by Primary Key or Lambda. | `WHERE id = $1 LIMIT 1` |

## ⚡ Industrial Concurrency
High-concurrency logic made simple and safe.

### High-Performance Parallelism
Execute independent tasks concurrently to maximize I/O utilization.
```javascript
const { user, profile } = parallel({
    user: () => db().from("users").find(1),
    profile: () => http().get("https://internal.service/profile/1")
});
```

### Advanced Flow Control
*   **`go(() => ...)`**: Dispatch heavy tasks to background workers.
*   **`defer(() => ...)`**: Lifecycle management to run logic **after** the response is sent.

## 📦 Explicit Caching System
Explicit key management ensures your cache is as predictable as your code.
```javascript
// Get-or-Set with human-readable TTL
const data = cache("top_sales", "1h30m", () => {
    return db().from("orders").where(o => o.amount > 1000).take();
});
```

## 🛠 Performance Markers
Kitwork is built for speed. Period.

| Metric | Result | Context |
| :----- | :----- | :------ |
| **Instruction Speed** | **~14,112,000 ops/s** | Raw VM Instruction Throughput |
| **Logic Complex Ops** | **~605,000 ops/s** | Real-world data transformation |
| **VM Overhead** | **~70ns** | Pure execution latency |
| **GC Pause Impact** | **Near-Zero** | Pooled resources architecture |

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

## 👨‍💻 Foundation & Architecture
> **"Performance is not an afterthought; it is the infrastructure."**

## 👨‍💻 Logic Engine Architect
**Huỳnh Nhân Quốc**
- ⚙️ Core Engine & Bytecode Development
- ⚡ High-Performance Runtime (Golang)
- 📜 Scripting Syntax & Logic Design
- 🚀 [kitwork.vn](https://kitwork.vn)

**Support Development** → [Sponsor KitWork / Huỳnh Nhân Quốc](https://github.com/sponsors/huynhnhanquoc)