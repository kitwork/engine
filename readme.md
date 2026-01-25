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
- [🗄️ Database (Structured Query Builder)](#️-database-structured-query-builder)
- [🌐 Web Stack Primitives](#-web-stack-primitives)
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

## 🗄️ Database (Structured Query Builder)
A high-performance SDK designed for complex logic execution. Kitwork leverages **Parameter-based Schema Inference** and **Operator Persistence** to eliminate boilerplate while maintaining 100% predictable SQL output.

### 🚀 Industrial One-Liners
Kitwork is engineered to collapse traditional multi-line queries into single, readable statements.
```javascript
// 1. Fetch exactly one record by any criteria
const user = db().from("users").find(u => u.email == "admin@kitwork.vn");

// 2. Immediate top-N results with zero configuration
const topUsers = db().from("users").take(5);

// 3. Find the most recent entry with architectural sorting
const lastOrder = db().from("orders").last();

// 4. Batch lookup using automatic Set Inclusion (IN)
const activeItems = db().from("products").where(p => p.id == [10, 20, 30]).take();
```

### ⚙️ Inference-driven Joins
The Kitwork VM reflects on Lambda parameter names to identify schema relationships. No strings required for table identifiers.
```javascript
// Variable 'orders' is automatically reflected to the "orders" table context
db().from("users")
    .join((orders) => orders.user_id == users.id)
    .take();
```

### 🧠 Operator Persistence (Smart Detection)
The engine infers the correct SQL operator based on data patterns at execution time.
- **Pattern Match (LIKE)**: `u.name == "%Apple%"` ➔ `WHERE name LIKE $1`
- **Set Inclusion (IN)**: `u.id == [1, 2, 3]` ➔ `WHERE id IN ($1, $2, $3)`

### 📈 Analytical Grouping & Aggregates
Handle complex data transformations directly at the storage layer with industrial reliability.
```javascript
const stats = db().from("orders")
    .group("user_id")
    .having(o => o.total_amount > 1000)
    .orderBy("total_amount", "DESC")
    .take(10);
```

### 🛠 Terminal Execution Methods
| Method | Description | SQL Projection |
| :----- | :---------- | :------------- |
| **`.take(n?)`** | Finalizes query and returns Array results. | `SELECT ... LIMIT n` |
| **`.one()`** | Returns a single Object (Record) or Null. | `FETCH FIRST 1 ROWS ONLY` |
| **`.last()`** | Architectural internal reverse-sort to fetch latest entry. | `ORDER BY id DESC LIMIT 1` |
| **`.find(id/fn)`** | High-speed lookup by Primary Key or Lambda reference. | `WHERE id = $1 LIMIT 1` |

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