# Kitwork: The Zero-Go Work Runtime

> **"Infrastructure as Configuration. Logic as JavaScript. Runtime as Binary."**

Kitwork is a standalone, high-performance logic platform that allows you to deploy backend services using only **YAML/JSON** and **JavaScript**. It abstracts away the complexity of Golang, offering a "No-Code Infrastructure" experience powered by a nano-second latency virtual machine.

---

## 🏗️ The Trio: Configuration, Logic, and Runtime

Kitwork operates on three core components that live at your project root:

1.  **`work.yaml` / `work.json`**: Define your system metadata and routes declaratively.
2.  **`work.js`**: Implement your business logic using modern JavaScript syntax.
3.  **`work.exe`**: The universal binary that boots your environment instantly.

### 1. Declarative Infrastructure (`work.yaml` or `work.json`)
Choose your preferred format to define routes:

**YAML (`work.yaml`)**
```yaml
name: "OrderSystem"
version: "1.0.0"
routes:
  - method: "GET"
    path: "/status"
  - method: "POST"
    path: "/order"
```

**JSON (`work.json`)**
```json
{
  "name": "InventoryAPI",
  "version": "1.2.0",
  "routes": [
    { "method": "GET", "path": "/items" }
  ]
}
```

### 2. Modern Logic Engine (`work.js`)
Write high-performance handlers with a JS-like DSL:

```javascript
// Automatically links to "OrderSystem" defined in YAML
work("OrderSystem").handle((req) => {
    const data = payload();
    
    // Parallel processing support!
    const { user, stock } = parallel({
        user: () => db().table("users").find(data.user_id),
        stock: () => http().get("/inventory/" + data.sku)
    });

    return {
        order_id: now().text(),
        status: stock.available ? "confirmed" : "out_of_stock",
        customer: user.name
    };
});
```

---

## ⚡ Key Capabilities

-   **Zero-Go Experience**: Deploy complex backends without touching a single line of Go code.
-   **Hybrid Config**: Merge routes from JSON/YAML with logic from JavaScript seamlessly.
-   **Parallel Power**: Built-in `parallel()` function uses Goroutines under the hood for non-blocking I/O.
-   **Modern Syntax**: Supports **Object & Array Destructuring** (`const { a, b } = ...`).
-   **Pooled Efficiency**: Nano-second latency and zero-allocation runtime during execution.

---

## 🚀 Getting Started

### 1. Build the Binary (Optional for users)
If you are the developer of the engine:
```bash
go build -o work.exe ./cmd/kit/main.go
```

### 2. Launch the Runtime
Simply place `work.exe` in your project folder containing `work.json` and `work.js`, then run:
```bash
./work.exe
```

### 3. Check it out
The server opens on port `8080` by default.
-   **API**: `http://localhost:8080/your-path`

---

## 🛠️ Built-in Functions

| Function | Description |
| :--- | :--- |
| `db()` | Fluent SQL query builder with "Magic Where". |
| `http()` | Optimized HTTP client for service mesh calls. |
| `parallel()` | Runs multiple tasks in parallel (Array/Object). |
| `payload()` | Access incoming request parameters safely. |
| `log()` | Structured, performance-optimized logging. |
| `now()` | High-precision system time. |

---

**Kitwork** - Bringing elite infrastructure performance to Every Developer. Fast, Simple, Standalone.
