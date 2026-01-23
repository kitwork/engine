# 🚀 Kitwork Engine Demos

Welcome to the feature showcase of Kitwork Engine. This directory contains categorized examples demonstrating the power of the engine.

## 📂 Folder Structure

### 1. `basics/` 🟢
Simple scripts to get started with syntax and logic.
- **`hello.js`**: Basic logging and JSON return.
- **`logic.js`**: Math operations, objects, and control flow.

### 2. `database/` 🔵
Deep dive into Database integrations (PostgreSQL).
- **`magic_where.js`**: Demonstrates the **Magic Lambda Where** (`.where(u => u.id == 1)`).
- **`transform.js`**: Fetching data and transforming it in-memory using `.map()`.

### 3. `api/` 🟠
Backend development made easy.
- **`shorthand.js`**: Define a REST API endpoint in just **2 lines of code**.
- **`gateway.js`**: Advanced Dynamic Router handling query params and multiple routes.

### 4. `advanced/` 🟣
Pro-level patterns and architecture.
- **`proxy_pattern.js`**: The **Generic Proxy Pattern** — using the same lambda syntax for both SQL generation and direct execution.

---

## 🛠️ How to Run

### Auto-Deploy
The server automatically loads scripts from `demo/api/` on startup.

### Manual Deploy
You can hot-reload or deploy any script without restarting the server:

```bash
# Deploy a specific script
GET http://localhost:8081/deploy?script=basics/hello
```

### Try endpoints
- **Shorthand API**: `http://localhost:8081/api/users`
- **Dynamic Gateway**: `http://localhost:8081/api/dynamic/users?name=bob`
