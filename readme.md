# 🚀 Kitwork Engine
> **"High-Performance Sovereign Logic Infrastructure & Nanosecond Runtime."**

![Go Version](https://img.shields.io/badge/go-1.21%2B-00ADD8?style=flat-square&logo=go)
![Architecture](https://img.shields.io/badge/arch-stack--vm-orange?style=flat-square)
![Efficiency](https://img.shields.io/badge/gc-zero--pressure-green?style=flat-square)
![License](https://img.shields.io/badge/license-MIT-blue?style=flat-square)

**Kitwork Engine** is an industrial-grade logic infrastructure designed for high-concurrency systems. It provides a specialized runtime to execute complex workflows with **70ns execution precision**, bridging the gap between low-level Go performance and high-level logic flexibility.

---

## 🚀 Quick Start
```bash
# 1. Clone & Bootstrap
git clone https://github.com/kitwork/engine
go run cmd/server/main.go

# 2. Check Industrial Heartbeat
# Server online at http://localhost:8081
```

Define your first Logic Work (`demo/api/hello.js`):
```javascript
work("Service")
  .get("/hello", () => {
    return { 
        status: "Operational", 
        engine: "Kitwork 14.2",
        entropy: random() 
    };
  });
```

---

## 🧠 Core Philosophy (The Sovereign Way)
*   **Nanosecond Execution**: A proprietary stack-based VM that executes logic in ~70ns.
*   **Zero-Copy Logic**: Data flows through the system without redundant allocations or GC pressure.
*   **Architectural Sovereignty**: Business logic is compiled to deterministic bytecode, independent of the server core.
*   **Agent-Native Design**: Optimized for predictable manipulation by AI Coding Assistants.

---

## 🗄️ Smart ORM (Industrial Query Builder)
A high-performance SDK that leverages **Proxy-based Entity Resolution** and **Parameter Inference** to eliminate boilerplate while maintaining absolute SQL predictability.

```javascript
// 1. Proxy Entity Lookup
const user = db.user.find(1);
const admin = db.user.find(u => u.role == "admin");

// 2. Set Inclusion (Automatic IN Clause)
const users = db.user.where(u => u.id == [1, 2, 3]).list();

// 3. Strict Mode Security
db.user.where(u => u.id == 1).update({ status: "ACTIVE" }); // .where() is mandatory
```

---

## 🎨 Industrial JIT CSS Engine
A Go-powered **Just-In-Time CSS Generator** that scans your HTML and generates a minimal, high-performance static framework.

### 🛠️ Configuration (Industrial Standard)
- **No Aliases**: Use full names like `background-brand` or `margin-top-12px` for clarity.
- **Explicit Units**: Native support for `px`, `pct` (%), `rem`, `vh`, `vw`.
- **Negative Values**: Standardized prefix (e.g., `-translate-y-4px`).

```html
<!-- Industrial Red & Glassmorphism -->
<nav class="background-black-30 blur-medium border-bottom-1px border-white-5">
    <button class="background-brand text-white hover:-translate-y-2px hover:shadow-glow transition-all">
        Sovereign Core
    </button>
</nav>
```

---

## 🖼️ Render & Layout System
A zero-allocation template engine featuring **Composite Rendering**.

```javascript
work("Dashboard")
    .get("/admin", () => ({ status: "SYNCED" }))
    .layout({ 
        $navbar: "view/navbar.html", // Pre-renders to {{ $navbar }}
        $footer: "view/footer.html" 
    })
    .render("view/work.html");
```

### 🛡️ XSS Protection Syntax
| Syntax | Behavior | Mode |
| :--- | :--- | :--- |
| `{{ $variable }}` | **Raw HTML** | Trusted Infrastructure (Layouts) |
| `{{ variable }}` | **Auto-Escaped** | Unsafe Data (User input, DB content) |

---

## ⏰ Sovereign Scheduler (`.schedule()`)
Manage recurring rituals with nanosecond precision using a human-centric semantic API.

```javascript
work("DailyAudit")
    .handle(() => log("Audit Sync: OK"))
    .daily("01:00", "13:00") // Dual-phase sync
    .weekly("MONDAY 08:30")
    .every("5m");            // Universal interval parser
```

---

## 🛠️ Performance Metrics
| Metric | Bench Score | Context |
| :----- | :---------- | :------ |
| **VM Instruction** | **~14.1M ops/s** | Raw Bytecode Velocity |
| **Logic Processing** | **~605K ops/s** | Real-world Transformation |
| **Clock Latency** | **70ns** | Execution Precision |
| **GC Overhead** | **Zero** | Resource Pooling Architecture |

---

## ⚙️ Logic Architect
**Huỳnh Nhân Quốc**
⚙️ Core VM & Bytecode Design | ⚡ High-Velocity Go Runtime | 🚀 Industrial Sovereignty