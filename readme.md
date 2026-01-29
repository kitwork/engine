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

### 🌟 Modern Entity-Style Syntax
Kitwork 1.0 introduces **Proxy-based Entity Resolution**, allowing you to access tables as if they were native properties.
```javascript
// 1. Fetch exactly one record (Entity Framework Style)
const user = db.user.find(u => u.email == "admin@kitwork.vn");

// 2. Multi-Database support with property chaining
const remoteUser = db("secondary").user.take(5);

// 3. Chainable aggregates that return raw values
const totalAmount = db.orders.where(o => o.status == "paid").sum("amount");

// 4. Batch lookup using automatic Set Inclusion (IN)
const activeItems = db.products.where(p => p.id == [10, 20, 30]).toList();
```

### 🚀 Industrial One-Liners
Kitwork is engineered to collapse traditional multi-line queries into single, readable statements.
```javascript
// Find the most recent entry with architectural sorting
const lastOrder = db.orders.last();

// Immediate top-N results
const topUsers = db.user.take(5);

// Check if a record exists
const hasAdmin = db.user.where(u => u.role == "admin").any();
```

### ⚙️ Inference-driven Joins
The Kitwork VM reflects on Lambda parameter names to identify schema relationships. No strings required for table identifiers.
```javascript
// Variable 'orders' is automatically reflected to the "orders" table context
db.users
    .join((orders) => orders.user_id == users.id)
    .take();
```

### 🧠 Operator Persistence (Smart Detection)
The engine infers the correct SQL operator based on data patterns at execution time.
- **Pattern Match (LIKE)**: `u.name == "%Apple%"` ➔ `WHERE name LIKE $1`
- **Set Inclusion (IN)**: `u.id == [1, 2, 3]` ➔ `WHERE id IN ($1, $2, $3)`

### 📈 Analytical Aggregates
Handle complex data transformations with high-performance terminal methods.
```javascript
const count = db.orders.count();
const average = db.products.avg("price");
const maxPrice = db.products.max("price");
```

### 🛠 Terminal Execution Methods
| Method | Description | SQL Projection |
| :----- | :---------- | :------------- |
| **`.take(n?)`** | Finalizes query and returns Array results. | `SELECT ... LIMIT n` |
| **`.toList()`** | Alias for `.get()`, returns all matched records. | `SELECT ...` |
| **`.one()`** | Returns a single Object (Record) or Null. | `LIMIT 1` |
| **`.find(id/fn)`** | High-speed lookup. If Fn is used, returns single record. | `WHERE ... LIMIT 1` |
| **`.firstOrDefault()`** | EF Style alias for `.first()`. | `LIMIT 1` |
| **`.sum(col)`** | Returns the sum of a column as a number. | `SELECT SUM(col) ...` |

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
| `header(key)` | Request headers. | `const auth = header("Authorization")` |
| `status(code)` | Set HTTP status code. | `status(201)` |
| `redirect(url)`| Immediate redirect. | `redirect("/home")` |
| **`readfile(path)`** | Reads a local file content. | `const html = readfile("view.html")` |
| **`html(content)`**| Returns HTML response. | `return html("<h1>Hi</h1>")` |

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
- 🚀 [kitwork](https://kitwork.io)

**Support Development** → [Sponsor KitWork / Huỳnh Nhân Quốc](https://github.com/sponsors/huynhnhanquoc)