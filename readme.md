# 🚀 Kitwork Engine Documentation
> **"Logic as Infrastructure. Nanosecond Latency. Zero-GC Runtime."**

![Go Version](https://img.shields.io/badge/go-1.21%2B-00ADD8?style=flat-square&logo=go)
![Architecture](https://img.shields.io/badge/arch-stack--vm-orange?style=flat-square)
![License](https://img.shields.io/badge/license-MIT-blue?style=flat-square)

**Kitwork Engine** is a high-performance embedded scripting runtime specifically designed for building scalable backend systems. It combines the raw speed of a custom stack-based VM with the ease of use of a JavaScript-like syntax.

---

## 📚 Table of Contents

- [🚀 Quick Start](#-quick-start)
- [🧠 Core Concepts](#-core-concepts)
- [🌐 Web Primitives](#-web-primitives)
  - [Routing](#routing)
  - [Request Handling](#request-handling)
  - [Response Control](#response-control)
  - [Cookies & Sessions](#cookies--sessions)
- [🗄️ Database Access](#️-database-access)
- [📦 Caching System](#-caching-system)
- [⚡ Concurrency & Flows](#-concurrency--flows)
- [🛠️ Utility Functions](#️-utility-functions)
- [⚙️ Configuration](#️-configuration)

---

## 🚀 Quick Start

1.  **Clone & Run**:
    ```bash
    git clone https://github.com/kitwork/engine
    go run cmd/server/main.go
    ```
    The server starts on port `8100` (default) loading scripts from `demo/`.

2.  **Write Your First Logic**:
    Create `demo/first.js`:
    ```javascript
    work("HelloAPI")
        .get("/hello", () => {
            return { message: "Hello from Kitwork!" };
        });
    ```
    
3.  **Test It**:
    ```bash
    curl http://localhost:8100/hello
    ```

---

## 🧠 Core Concepts

*   **Work**: A unit of logic that groups related endpoints and background tasks. Think of it as a "Service" or "Controller".
*   **Task Context**: Every request runs in an isolated, ultra-lightweight context. Data is lazy-loaded (parsed only when requested) and zero-copy where possible.
*   **Zero-GC**: The engine pools `Task` objects and VM stacks, meaning effectively **0 bytes of garbage** are generated per request logic execution.

---

## 🌐 Web Primitives

### Routing
Kitwork uses a high-performance Trie-based router.

```javascript
work("UserModule")
    .get("/users", listUsers)           // Static Path
    .get("/users/:id", getUser)         // Dynamic Path Parameter
    .post("/users", createUser)
    .put("/users/:id/status", updateStatus);
```

### Request Handling
Access request data efficiently.

| Function | Description | Example |
| :--- | :--- | :--- |
| `params(key)` | Get URL path parameter. | `params("id")` for `/users/:id` |
| `query(key)` | Get URL query string. | `query("page")` for `?page=2` |
| `body(key?)` | Get JSON Body. Read-once, auto-cached. | `const { email } = body()` |
| `header(key)` | Get Request Header. | `header("Authorization")` |
| `cookie(name)` | Get Cookie value. | `cookie("session_id")` |

### Response Control

| Function | Description | Example |
| :--- | :--- | :--- |
| `status(code)` | Set HTTP Status Code. | `status(201)` (Created) |
| `redirect(url)` | Redirect browser. | `redirect("/login")` |
| `return val` | Send JSON response. | `return { ok: true }` |

## 🚀 Performance Markers

Real-world benchmarks running on local development environment (Jan 2026):

| Metric | Result | Context |
| :--- | :--- | :--- |
| **Throughput (Raw)** | **~14,112,000 ops/sec** | Direct Bytecode Execution |
| **Throughput (Logic)** | **~605,000 ops/sec** | Complex Recursive Workflows |
| **Latency (Core)** | **~70ns** | Pure Logic Execution Time |
| **Memory Overhead** | **~8 bytes/op** | Near Zero-GC allocation |

### Cookies & Sessions
Securely manage user sessions.

```javascript
// Setting a secure cookie
cookie("token", "xyz-secret", {
    httpOnly: true,  // Prevent JS from accessing (XSS protection)
    secure: true,    // Send only over HTTPS
    maxAge: 3600,    // Expire in 1 hour
    path: "/"        // Valid for whole site
});
```

---

## 🗄️ Database Access (Ultra-Smart Query Builder)

Kitwork Engine provides a powerful, minimalist, and elite database query SDK. Our philosophy is **"Simple is the new Smart"** — use the `.where()` function for almost every need.

### 🌟 Magic Lambda Syntax
Instead of error-prone strings, Kitwork uses arrow functions (Lambdas) to interact with columns. It's safe, prevents typos, and supports perfect code autocompletion.

```javascript
// Minimalist, safe, and intuitive
db().table("user").where(u => u.username == "boss").get();
```

### 🧠 Smart Operator Detection
The engine automatically infers the appropriate SQL operator based on the data you provide, making your code look "clean" and more like natural language:

*   **Auto-LIKE Detection**: Triggered when a string contains the `%` character.
    ```javascript
    // Becomes: WHERE "username" LIKE 'Apple%'
    db().table("user").where(u => u.username == "Apple%").get();
    ```
*   **Auto-IN Detection**: Triggered when the value is an Array.
    ```javascript
    // Becomes: WHERE "id" IN (10, 20, 30)
    db().table("user").where(u => u.id == [10, 20, 30]).get();
    ```

### 🛠 Power Shortcuts
| Feature | Syntax | Expected SQL |
| :--- | :--- | :--- |
| Quick Find by ID | `.find(1)` | `WHERE "id" = 1` |
| Get First Record | `.first()` | `LIMIT 1` |
| Ordering Data | `.orderBy("age", "DESC")` | `ORDER BY "age" DESC` |
| Pagination | `.limit(10).offset(10)` | `LIMIT 10 OFFSET 10` |

```javascript
// Complex query in just a few lines
const users = db().table("user")
    .where(u => u.role == "admin")
    .where(u => u.is_active == true)
    .orderBy("created_at", "DESC")
    .limit(10)
    .get();
```

### 📈 Aggregates & Mutations
```javascript
// Aggregates
let total = db().table("orders").sum("amount");
let average = db().table("products").avg("price");

// Data Mutations
db().table("user").insert({ name: "Alice", age: 25 });
db().table("user").where(u => u.id == 1).update({ status: "active" });
db().table("user").where(u => u.id == 99).delete();
```

---

## 📦 Caching System

Kitwork provides a high-performance, explicit caching mechanism. Unlike "magic" caching, Kitwork requires an explicit **Key** to ensure data consistency and predictability.

### Usage Patterns

| Pattern | Description | Example |
| :--- | :--- | :--- |
| `cache(key)` | **Get**: Retrieve a value from the global cache. | `const data = cache("my_key")` |
| `cache(key, value, ttl)` | **Set**: Manually store a value with a specific TTL. | `cache("user_1", userData, "1h")` |
| `cache(key, ttl, callback)` | **Get or Set**: Retrieve value; if missing, execute callback, store result, and return. | `const data = cache("list", "1d", () => db().get())` |

### TTL Formats
The duration parameter supports flexible, human-readable strings:
*   **Standard**: `"30s"`, `"15m"`, `"1h"`, `"2h45m"` (Standard Go durations)
*   **Extended**: `"1d"`, `"7d"` (Day-based durations)
*   **Numeric**: `60` (Defaults to seconds)

### Why Explicit Caching?
By using explicit keys, you avoid the "stale data" layout issues common in automatic caches. You know exactly what is cached and can easily implement cache invalidation logic.

---

## ⚡ Concurrency & Flows

Kitwork exposes Go's concurrency model simply and safely.

### Parallel Execution
Execute multiple non-dependent blocking operations at the same time.

```javascript
const { user, orders, analytics } = parallel({
    user: () => db().table("users").where("id", 1).first(),
    orders: () => db().table("orders").where("user_id", 1).get(),
    analytics: () => http().get("https://analytics-service/user/1")
});
```

### Background Jobs (`go`)
Fire-and-forget tasks that shouldn't block the response.

```javascript
post("/order", () => {
    // ... process order ...
    
    // Send email in background
    go(() => {
        http().post("https://mailer/send", { to: user.email, subject: "Order Confirm" });
    });
    
    return { status: "processing" };
});
```

### Resource Cleanup (`defer`)
Register logic to run **after** the response is sent (like `defer` in Go).

```javascript
defer(() => {
    log("Request finished at " + now());
});
```

---

## 🛠️ Utility Functions

*   **`log(...args)`**: High-performance structured logging.
*   **`now()`**: Get current timestamp in nanoseconds.
*   **`uuid()`**: Generate a generic unique ID.
*   **`http()`**: HTTP Client with `.get(url)`, `.post(url, body)`.

---

## ⚙️ Configuration

The engine looks for `work.json` or `work.yaml` in the running directory.

**Example `work.yaml`**:
```yaml
port: 8100
debug: true
source: "./demo/api"
```

---

*This documentation tracks version v0.1.0 of the Kitwork Engine.*