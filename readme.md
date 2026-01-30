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
- [🖼️ Render & Layout System](#-render--layout-system)
- [🌐 Web Stack Primitives](#-web-stack-primitives)
- [⚡ Industrial Concurrency](#-industrial-concurrency)
- [📦 Multi-layer Caching](#-multi-layer-caching)
- [🏗️ Static Resource Serving](#️-static-resource-serving)
- [🧩 Functional Data Processing](#-functional-data-processing)
- [🔄 Execution Lifecycle Hooks](#-execution-lifecycle-hooks)
- [⏰ Background Task Scheduling (.schedule())](#-background-task-scheduling-schedule)
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

### ⚡ Power Features:
- **Zero-Config Generation**: No complex Webpack/Vite setups required.
- **Deep Tech UI**: Built-in support for futuristic interactions (`hover:translate-y--2px`, `hover:shadow-glow`).
- **Smart Aliases**: Use `bg-` for `background-`, `font-mono` for code, and standard utility syntax.
- **Explicit Units**: Full control with explicit sizing (e.g., `width-30pct`, `border-1px`, `opacity-80`).
- **Dynamic 3D Support**: Native support for 3D components like cubes, including responsive sizing and duration.

```bash
# Generate CSS on the fly
go run demo/css_jit_demo.go
```

Example Deep Tech Component:
```html
<button class="bg-brand text-white rounded-4px hover:translate-y--2px hover:shadow-glow transition-all">
    <span class="font-mono uppercase">Deploy Node</span>
</button>
```

---

## 🎨 Render & Layout System
Kitwork features a zero-allocation template engine designed for maximum throughput. It supports component-based architecture via **Smart Layout Injection**.

### 🧩 Smart Layouts (`.layout()`)
Inject partials (like navbars, footers) directly into your main view.
Partials are pre-loaded and available as **Raw Placeholders** (prefixed with `$`).

```javascript
work("Dashboard")
    .get("/admin", () => ({ user: "Admin" }))
    .layout({ 
        $navbar: "view/components/navbar.html", // Maps to {{ $navbar }}
        $footer: "view/components/footer.html"  // Maps to {{ $footer }}
    })
    .render("view/dashboard.html");
```

### 🛡️ Smart Security (Syntax)
The engine automatically distinguishes between trusted layouts and unsafe user data based on the variable syntax.

| Syntax | Behavior | Use Case |
| :--- | :--- | :--- |
| `{{ $variable }}` | **Raw HTML** (Unescaped) | Layouts, Trusted Components |
| `{{ variable }}` | **Auto-Escaped** (Safe) | User Input, Database Content |

> **Analogy**: Think of `$` as a "System Flag". If it has a dollar sign, it's trusted infrastructure. If not, it's treated as untrusted data.

---

## �🌐 Web Stack Primitives

### Zero-Copy Routing
Kitwork uses a high-performance Trie-based router for maximum throughput.
```javascript
work("App")
    .get("/users", listUsers)           // Static
    .get("/users/:id", getUser)         // Dynamic: params("id")
    .post("/users", createUser)          // POST Payload
    
// 2. High-Performance Static Redirects
work("Legacy")
    .get("/old-api").redirect("/api/v1/hello")
    .get("/google").redirect("https://google.com", 301);
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
| `redirect(url, code?)`| Logic-based redirect inside handler. | `redirect("/home")` |
| `cookie(k, v)`| Secure cookie management. | `cookie("token", val, { secure: true })` |
| `http.get(url)` | REAL HTTP GET (Auto-parse JSON). | `let res = http.get(url)` |
| `http.post(url, b)`| REAL HTTP POST. | `let res = http.post(url, {key: 1})` |

---

## 📦 Multi-layer Caching
Kitwork offers a hybrid caching strategy to balance between RAM speed and Disk persistence.

### RAM Cache (LRU)
Predictable in-memory caching with human-readable duration strings (e.g., "1h30m").
```javascript
const data = cache("top_sales", "1h", () => {
    return db.orders.where(o => o.amount > 1000).list();
});
```

### Disk Stacking (Static Snapshot)
Tĩnh hóa (Statify) your dynamic responses directly to disk. It uses **OS Metadata (ModTime)** for extreme efficiency.
```javascript
work("GoldPrice")
    .get("/api/gold")
    .static("10m") // Serve static snapshot for 10 minutes
    .handle(() => http.get("https://api.gold/latest"));
```
*   **Security**: Use `.static({ duration: "1h", check: true })` to enable **SHA256 Checksum** verification, preventing manual tampering with cached files.

---

## 🏗️ Static Resource Serving
Bypass the Script Engine entirely for maximum throughput (Zero-VM overhead).

### Unified Asset Serving
The `.assets()` method is polymorphic; it automatically detects whether you are serving a single file or an entire directory.

```javascript
// 1. Directory Mapping (Wildcard)
work("Static").get("/assets/*").assets("./dist/assets");

// 2. Single File Alias
work("Manifest").get("/site.webmanifest").assets("./public/manifest.json");

// 3. Explicit File Serving
work("Logo").get("/favicon.ico").file("./logo.png");
```

---

## 🧩 Functional Data Processing
Transform and filter data using native bytecode-optimized operations. These run directly in the core VM, making them significantly faster than manual loops.

```javascript
const result = rawList
    .map(item => ({ 
        id: item.id, 
        price: item.val * 1.5 
    }))
    .filter(item => item.price > 100);
```
*   **Note**: Always wrap returned objects in parentheses `({ })` when using arrow functions.

---

## 🔄 Execution Lifecycle Hooks
Decouple business logic from post-processing and error management.

```javascript
work("Transaction")
    .handle((req) => {
        if (!req.body().amount) fail("Amount is required");
        return db.pay(req.body());
    })
    .done((res) => log(`Paid: ${res.txId}`))
    .fail((err) => log(`Alert Admin: ${err}`));
```

---

## ⏰ Background Task Scheduling (.schedule())
Decouple business logic from request-cycles. Kitwork includes an industrial background scheduler that executes your business rules with nanosecond precision using a human-centric semantic API.

### ⚡ Atomic Scheduling & Logic Inheritance
The `.schedule()` family of methods allows you to assign recurring rituals to any Work blueprint. Tasks automatically inherit logic from the primary `.handle()` method, ensuring zero redundancy.

```javascript
work("DailyOperations")
    .handle(() => {
        log("Atomic Pulse: Running critical bank synchronization...");
        db.orders.where(o => o.status == "pending").update({ processed: true });
    })
    // 1. Variadic Semantic Times
    .daily("01:00", "13:00") 
    
    // 2. Precise Hour Markers (Minutes 0 and 30)
    .hourly(0, 30)
    
    // 3. Intervals
    .every("5m")
    
    // 4. Weekly/Monthly Milestones
    .weekly("MONDAY 08:30")
    .monthly("1st")
    
    // 5. Advanced Config & Custom Handlers
    .schedule("0 45 22 * * *", (res) => {
        log("Nightly Audit Complete");
    });
```

### 🧠 Smart Parser Capabilities
Kitwork's **Universal Scheduler Parser** handles multiple semantic formats automatically:
- **Time Strings**: `"13:00"` (Daily at 1 PM).
- **Durations**: `"3s"`, `"10m"`, `"1h"` (Simple intervals).
- **Pure Numbers**: `2000` (Every 2000ms).
- **Day-Time Combinations**: `"MONDAY 14:30"` (Weekly on Mondays).
- **Variadic Arguments**: Pass multiple times directly as separate arguments.

- **Unified Runtime**: JS for both APIs and Cron jobs.
- **Persistence**: Tasks are auto-mapped and initiated during engine boot.
- **Zero-Boilerplate**: No arrays or complex objects required for basic multi-scheduling.

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