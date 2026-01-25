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

## �️ Database Access

The `db()` intrinsic provides a fluent Query Builder. It currently mocks data but is designed to plug into PostgreSQL/MySQL drivers.

```javascript
// 1. Select
const users = db().table("users")
    .where("active", true)
    .where("age", ">", 18)
    .limit(10)
    .get();

// 2. Find One
const admin = db().table("users").where("role", "admin").first();

// 3. Insert
db().table("orders").insert({
    user_id: 101,
    amount: 99.50,
    status: "pending"
});

// 4. Update
db().table("users").where("id", 101).update({ status: "banned" });
```

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