# Kitwork Engine

> **"The cloud became an estate to operate. Kitwork is a disagreement."**

[![Go Version](https://img.shields.io/badge/go-1.26+-black?style=flat-square&logo=go)](https://golang.org)
[![License](https://img.shields.io/badge/license-AGPL--3.0-blue?style=flat-square)](#license--sponsorship)
[![VM Throughput](https://img.shields.io/badge/instruction-~27ns-green?style=flat-square)](#performance--benchmarks)
[![Cold Boot](https://img.shields.io/badge/cold%20boot-%3C10ms-green?style=flat-square)](#performance--benchmarks)
[![Architecture](https://img.shields.io/badge/architecture-sovereign--vm-orange?style=flat-square)](#ownership-hierarchy--execution-model)

**Kitwork Engine is cloud infrastructure compiled into a single, sovereign Go binary.** It executes a deliberately-constrained JavaScript subset (**Kit JS dialect**) on a custom stack-based bytecode VM — featuring energy metering, strict multi-tenant sandboxing, atomic generation hot reload, filesystem-routed API & SSR rendering, fluent SQL query building, zero-build JIT assets (CSS, Icons, Logos), automatic TLS, built-in rate limiting, semantic RSS/Sitemap generation, and real-time SSE streaming.

One engine process hosts unlimited tenants and domains. Deploying a new website or backend service is as simple as dropping a directory into place.

---

## 📋 Table of Contents

1. [The Philosophy & Contract](#the-philosophy--contract)
2. [Why a Custom VM?](#why-a-custom-vm)
3. [Ownership Hierarchy & Execution Model](#ownership-hierarchy--execution-model)
4. [Engine Architectural Invariants](#engine-architectural-invariants)
5. [The Kit JS Language Subset](#the-kit-js-language-subset)
6. [Engine Package Map](#engine-package-map)
7. [Host Bootstrap & Server Configuration](#host-bootstrap--server-configuration)
8. [Routing, Lifecycle & Parameter Injection](#routing-lifecycle--parameter-injection)
9. [HTML View Engine & Layout Slots](#html-view-engine--layout-slots)
10. [Industrial Query Builder](#industrial-query-builder)
11. [JIT Engine Suite](#jit-engine-suite)
12. [Security Model](#security-model)
13. [Performance & Benchmarks](#performance--benchmarks)
14. [Preflight Verification & CLI](#preflight-verification--cli)
15. [License & Sponsorship](#license--sponsorship)

---

## 🏛️ The Philosophy & Contract

Traditional cloud deployment stacks accumulate complexity: Redis for caching, RabbitMQ for queues, Node/V8 for execution, NGINX for ingress, and Kubernetes for orchestration. Teams end up operating heavy machinery instead of shipping software.

Kitwork collapses that entire infrastructure estate into **one binary with one strict execution philosophy**:

### The Five Falsifiable Rules

1. **What is supported behaves exactly like JavaScript.** No "almost" edge cases or subtle semantics drift.
2. **What is removed fails at compile time with clear guidance.** Absence is an explicit design statement, never a runtime surprise.
3. **Every workload is bounded.** All opcodes consume energy; infinite loops cannot compile or execute.
4. **One binary is the complete platform.** If a feature requires an external runtime service to operate, it does not ship.
5. **State outlives machines.** Transient Node RAM holds nothing precious; ACID-compliant database storage is the single source of truth.

---

## ⚡ Why a Custom VM?

Running untrusted multi-tenant code safely and efficiently is the core challenge of modern cloud execution:

| Approach | Isolation Level | Cold Boot | Memory Footprint | Unbounded Code Guard? |
| :--- | :--- | :--- | :--- | :--- |
| **Containers / microVMs** | OS / Hypervisor | 100ms – seconds | Hundreds of MBs / tenant | ❌ No — full OS execution inside |
| **Embedded V8 / Goja** | Interpreter / Heap | Milliseconds | Tens of MBs / tenant | ❌ No — requires external watchdog timers |
| **Kitwork Engine VM** | **Bytecode & Stack** | **< 10ms** | **Single Go process** | **✅ Yes — unbounded constructs fail at compile time** |

Because Kitwork owns the complete pipeline — lexer, parser, native ESM bundler, bytecode compiler, opcodes, stack VM, and capability manager — safety is an inherent property of the **language definition**, not an external sandbox wrapper around a general-purpose engine.

---

## 🏗️ Ownership Hierarchy & Execution Model

The production engine enforces a strict multi-tier ownership model to guarantee isolation, hot reloading safety, and zero-copy performance:

```text
Host / Engine (Process)
  └── AppRegistry
        └── AppRuntime (Identity scope)
              ├── Database Pool & Connections
              ├── Task Group & Scheduler (Cron)
              ├── App-scoped Capabilities (LifetimeApp)
              └── SiteRuntime (Domain scope)
                    ├── Persistent Cache & SSE Broker
                    └── Generation (Version / Monotonic State)
                          ├── Executable Route Graph & Programs
                          ├── Prepared HTML Render Plan & Template Snapshot
                          ├── Immutable Environment (.env) & Source Manifest
                          ├── JIT CSS / Icon / Logo / Asset Snapshots
                          └── RequestScope (Per-request context)
                                ├── VM Lease (from sync.Pool)
                                ├── Energy Meter & Stack Depth Guard
                                └── LifetimeRequest Capabilities
```

### Resource Isolation & Lifecycle Boundaries

- **Host Engine:** Owns listeners, AutoSSL/TLS, signal handling, shared VM pool, and app registry.
- **App Runtime (`app.Runtime`):** One per tenant identity (`apps/<identity>`). Manages database connections, background tasks (`go(fn)`), cron schedules, and `LifetimeApp` capabilities.
- **Site Runtime (`site.Runtime`):** One per domain (`apps/<identity>/<domain>`). Maintains persistent response stores, rate limits, and long-lived SSE channels across reloads.
- **Generation (`site.Generation`):** An immutable, monotonic version snapshot of a site. Owns compiled route bytecode, parsed render plans, `.env` snapshots, and `LifetimeSite` capabilities. Requests pin a generation for their entire lifecycle.
- **Request Scope (`request.Scope`):** Created per HTTP request. Manages cancellation context, authentication claims, `LifetimeRequest` capability instances, and temporary VM leases.
- **VM Lease:** Reclaimed stack VMs allocated from `sync.Pool`. VMs carry zero app or site state between requests (`ResetForPool` / `FastReset`).

---

## 🛡️ Engine Architectural Invariants

The Kitwork Engine enforces seven structural stability invariants verified by continuous contract tests:

### 1. App and Site Isolation
Pooled VMs must never leak root scopes, closures, variables, or gas limits between execution cycles. Database connections are owned by the parent `AppRuntime` and shared across sibling sites under the same identity. Evicting a site domain closes only that site, leaving sibling sites and identity-wide database pools active.

### 2. Energy and Bounded Execution
Every bytecode instruction consumes energy tracked against `MaxEnergy`. `InstructionSpec` defines canonical opcode widths, stack impacts, and energy costs. Script execution checks context cancellation within at most 64 opcodes. Call stack recursion is hard-capped at 64 frames to prevent Go runtime stack overflow.

### 3. Capability Lifetimes
Capabilities declare explicit dependency scopes:
- `LifetimeTransient`: Fresh instance per resolution (e.g. VietQR builders, HTTP request builders).
- `LifetimeRequest`: Shared within a single `request.Scope`.
- `LifetimeSite`: Bound to the active `site.Generation`; closed when requests drain during generation retirement.
- `LifetimeApp`: Identity-wide instance shared by schedulers and sibling domains; closed on app unload.
- `LifetimeSingleton`: Process-wide instance bound to host shutdown.

### 4. Detached Background Work (`kitwork().go(fn)`)
Background functions snapshot their lexical closure chain, builtins, top-level variables, and energy limits prior to returning. Detached work is managed by the `AppRuntime` task group, allowing site generation reloads without aborting background execution.

### 5. Atomic Monotonic Generation Swaps
Site file modifications trigger background compilation of a candidate `site.Generation`. If compilation or rendering setup fails, the candidate is safely discarded while the active generation continues serving without interruption.

### 6. Zero-Allocation Fast Path
Static assets (`/assets/*`, `.txt`, `.ico`, pre-rendered files) are served straight from disk using `io.Copy` zero-copy streams, completely bypassing VM allocation and script parsing.

### 7. Deferred Effect Boundaries (`value.Committer`)
The `COMMIT` opcode acts as the boundary for builders that accumulate configuration state (e.g. `http.get(url).cache("5m").persist("1d")`). Committing executing chains is idempotent and side-effect free.

---

## 🔤 The Kit JS Language Subset

To ensure microsecond startup times and absolute host security, Kitwork Engine executes a strict subset of JavaScript:

### Core Dialect Rules

#### 1. Arrow Functions ONLY (No `function` Keyword)
The `function` keyword is removed from the lexer and parser. All functions must use arrow syntax:
```javascript
// ✅ CORRECT
const add = (a, b) => a + b;
export const handleRequest = (ctx) => ctx.json({ ok: true });

// ❌ COMPILE ERROR
function add(a, b) { return a + b; }
```

#### 2. Multi-Level Scope Closures
Nested arrow functions capture outer lexical block scopes across arbitrary nesting depths:
```javascript
const filterUsers = (query) => {
    const matched = [];
    departments.forEach((dept) => {
        dept.members.forEach((user) => {
            if (user.name.indexOf(query) !== -1) {
                matched.push(user); // Captures `matched` across 2 scope boundaries
            }
        });
    });
    return matched;
};
```

#### 3. Trailing Comma Discipline
- **Arrays REJECT trailing commas:** `[1, 2, 3,]` throws a compile-time syntax error.
- **Objects ALLOW trailing commas:** `{ a: 1, b: 2, }` is valid.

#### 4. Parenthesized Arrow Return Objects
Implicit object returns from arrow functions must be parenthesized to avoid block statement ambiguity:
```javascript
// ✅ CORRECT
const makeUser = (id) => ({ id: id, role: "member" });

// ❌ COMPILE ERROR
const makeUser = (id) => { id: id, role: "member" };
```

#### 5. Native Native Import Resolution (ESM)
Modules are resolved directly by the engine's built-in bundler without Node.js or external toolchains:
```javascript
import { router, database } from "kitwork"
import { formatCurrency } from "./_core/utils.js"
```

### Deliberately Removed Language Constructs

| Removed Language Feature | Architectural Rationale | Recommended Alternative Pattern |
| :--- | :--- | :--- |
| `while`, `do-while` | Eliminates infinite / unbounded compute loops on host threads | `.map()`, `.filter()`, `.find()`, `.reduce()` |
| `try` / `catch` / `throw` | Avoids hidden control-flow jumps; forces explicit error returns | Explicit checks, `safe()` wrappers, `.catch()` |
| `switch` | Simplifies VM opcode tree; encourages lookup maps | `if / else if / else` or object lookup dictionaries |
| `class` / `this` | Data remains pure data; behavior is composition of functions | Object literals and factory arrow functions |

---

## 🗺️ Engine Package Map

The `engine/` repository is organized into focused Go packages maintaining zero third-party framework dependencies:

```text
engine/
├── app/          # AppRuntime, tenant identity isolation, AppRegistry & VM pooling
├── builtins/     # JS standard library (Math, Date, String, Array, JSON, Console, etc.)
├── capabilities/ # Dependency injection, permissions & capability lifetime management
├── cmd/          # Engine CLI commands (kitwork check, preflight reports)
├── compiler/     # Lexer, Parser, AST transformation, Bytecode emitter, ESM bundler
├── config/       # Host configuration parser, YAML/JS manifest reader
├── core/         # Core engine orchestrator, Engine, ServeHTTP, Preflight Check pipeline
├── database/     # Industrial Query Builder, AST-to-SQL compiler (SQLite, Postgres, MySQL)
├── dns/          # AutoSSL Let's Encrypt manager, HostPolicy domain verifier
├── domain/       # Host domain matchers, canonical redirects, site discovery
├── host/         # OS host environment adapters & network detection
├── id/           # ShortBase32, CUID, and KSUID identifier generators
├── jit/          # Zero-build JIT engines (CSS atomic compiler, Icons, Logos, Components, JS)
├── logger/       # Structured slog handler with format & file outputs
├── render/       # HTML View Engine (.kitwork.html), @slot resolver, template compiler
├── request/      # Scope context, HTTP payload reader, parameter injection
├── runtime/      # Stack VM, opcode specifications, program verification, energy accounting
├── security/     # SSRF IP sanitizer, JWT signer/verifier, rate limiters
├── site/         # SiteRuntime, monotonic Generation lifecycle, SSE broker, RAM/Disk caches
├── utilities/    # VietQR/Napas QR generator, SVG, HTTP client, Gzip/Brotli compression
├── value/        # Value types (NaN-boxed / tagged union model), Committer interface
└── work/         # Tenant compatibility facade, request lifecycle handlers, scheduler adapter
```

---

## ⚙️ Host Bootstrap & Server Configuration

The host engine boots via `engine.Run()`, reading an executable JavaScript manifest (`app.kitwork.js` or `server.kitwork.js`).

### Host Manifest (`app.kitwork.js`)

```javascript
import { server, env } from "kitwork"

server
  .port(env.PORT || 8080)
  .root(env.ROOT || "apps")
  .hostname(env.HOSTNAME || "kitwork.io")
  .hotReload(true)
  .allowLocal(env.ALLOW_LOCAL || false)
  .trustProxy(env.TRUST_PROXY || false)
  .rateLimit({ rate: 2000, ip: 120, browser: 240, period: "1s" })
  .database({
    alias: "system",
    type: env.DB_TYPE || "postgres",
    host: env.DB_HOST || "localhost",
    port: env.DB_PORT || 5432,
    user: env.DB_USER || "postgres",
    password: env.DB_PASSWORD || "secret",
    name: env.DB_NAME || "kitwork_db",
    sslmode: "disable"
  })
  .logger({
    level: env.LOG_LEVEL || "info",
    format: "text",
    logfile: "logs/engine.log"
  });

server.run().catch((err) => console.log("Host boot error:", err));
```

### Fluent Server Builder API Reference

| Method | Type | Description |
| :--- | :--- | :--- |
| `.port(number)` | `Number \| String` | Sets host HTTP listening port (default: `8080`). |
| `.root(path)` | `String` | Root directory containing tenant apps (default: `"apps"`). |
| `.hostname(domain)` | `String` | Primary host cluster domain. |
| `.hotReload(bool)` | `Boolean` | Enables filesystem watcher & instant bytecode replacement. |
| `.allowLocal(bool)` | `Boolean` | Bypasses ACME/AutoSSL certificate acquisition for offline local dev. |
| `.trustProxy(bool)` | `Boolean` | Trust `X-Forwarded-For` and `X-Real-IP` HTTP headers from reverse proxies. |
| `.rateLimit(opts)` | `Object` | Sets global host rate limits: `{ rate, ip, browser, user, period }`. |
| `.database(opts)` | `Object` | Registers system or app database connection pool configuration. |
| `.canonical(mode)` | `String` | Configures auto-redirects: `"apex"` (www ➔ apex) or `"www"` (apex ➔ www). |
| `.redirects(map)` | `Object` | Static domain redirect mapping: `{ "old.com": "new.com" }`. |
| `.logger(opts)` | `Object` | Configures structured logging: `{ level, format, logfile }`. |
| `.run(port?)` | `Promise` | Evaluates server manifest and starts network listeners. |

---

## 🚦 Routing, Lifecycle & Parameter Injection

Tenant routes are declared inside `router.kitwork.js` files located within app domain folders (`apps/<identity>/<domain>/router.kitwork.js`).

### Request Handler Injection

Kitwork handlers feature **Dynamic Parameter Injection**. The VM inspects arrow function parameter names and automatically injects matching capabilities:

```javascript
import { router, database } from "kitwork"

const db = database.connect("system")

// Inject `ctx` (Unified Context)
router.get("/api/status").handle((ctx) => {
    return ctx.json({ status: "online", timestamp: Date.now() });
});

// Inject `req` and `res` individually
router.get("/api/users/:id").handle((res, req) => {
    const userId = req.params("id");
    const user = db.user.find(userId);
    if (!user) return res.status(404).json({ error: "User not found" });
    return res.json({ user: user });
});

// Inject `sse` (Server-Sent Events Broker)
router.get("/api/events").handle((sse) => {
    return sse.connect({ channel: "updates" });
});

// Catch error handler with `err` and `res`
router.get("/api/legacy").catch((err, res) => {
    return res.status(500).json({ success: false, message: err });
});
```

### Injected Parameter Index

- **`ctx` / `context`:** Unified context wrapper providing `.json()`, `.html()`, `.text()`, `.redirect()`, `.status()`, `.params()`, `.query()`, `.cookie()`, `.body()`.
- **`req` / `request`:** Request reader for headers, method, path, IP, and query parameters.
- **`res` / `response`:** Direct HTTP response writer.
- **`sse`:** Server-Sent Events broker helper.
- **`err` / `error` / `e`:** Captured execution error payload (for `.catch()` blocks).

---

## 🖼️ HTML View Engine & Layout Slots

Kitwork HTML views (`.kitwork.html`) separate rendering into **Assembly** (layout slot inheritance) and **Binding** (data interpolation).

### Layout Shell and `@name` Slots

Layout slot tokens like `{{ @head }}` or `{{ @sidebar }}` resolve modular partial files with directory inheritance fallbacks:

```html
<!-- views/index.kitwork.html (Layout Shell) -->
<!DOCTYPE html>
<html lang="en">
<head>
  <title>{{ $.siteTitle }}</title>
  {{ @head }}
</head>
<body>
  <header>{{ @navbar }}</header>
  <main>{{ @page }}</main>
  <aside>{{ @sidebar }}</aside>
  <footer>{{ @footer }}</footer>
</body>
</html>
```

### Clean Slot Resolution Hierarchy
When rendering a slot `{{ @sidebar }}`, the engine searches:
1. `views/sidebar.kitwork.html` in the current folder.
2. Parent directories up to the tenant views root.
3. Legacy `_sidebar_.kitwork.html` fallbacks.

### Template Expression Syntax

```html
<!-- Data Interpolation -->
<h1>Welcome, {{ user.name }}</h1>
<p>Site: {{ $.siteName }}</p> <!-- `$.` accesses global root data -->

<!-- Conditional Branches -->
{{ if user.is_admin }}
  <span class="badge">Admin</span>
{{ else }}
  <span class="badge">Member</span>
{{ end }}

<!-- Loops -->
<ul>
  {{ for item in products }}
    <li>{{ item.name }} - ${{ item.price }}</li>
  {{ end }}
</ul>

<!-- Local Variable Bindings -->
{{ let is_active = user.status == "active" }}
```

---

## 🗄️ Industrial Query Builder

The Kitwork Query Builder translates JavaScript lambda expressions into parameterized SQL queries executed without reflection overhead.

```javascript
import { database } from "kitwork"

const db = database.connect("system")

// 1. Fetch with Magic Lambda Filtering
const activeUsers = db.user
    .where(u => u.status == "active")
    .where(u => u.age >= 18)
    .sort(u => u.created_at, "desc")
    .take(20);

// 2. Automatic Set Inclusion (Auto-IN)
// SQL: SELECT * FROM products WHERE id IN (10, 20, 30);
const products = db.products.where(p => p.id == [10, 20, 30]).list();

// 3. Pattern Matching (Auto-LIKE)
// SQL: SELECT * FROM customers WHERE email LIKE '%@kitwork.io';
const team = db.customers.where(c => c.email == "%@kitwork.io").list();

// 4. Strict Mutations (Requires .where() clause)
const updated = db.user
    .where(u => u.id == 42)
    .update({ login_count: 5 });

// 5. Data Aggregates
const totalSales = db.orders.where(o => o.status == "paid").sum("total");
```

---

## 🎨 JIT Engine Suite

Kitwork features built-in Just-In-Time (JIT) presentation compilers that generate CSS, icons, and client interactions on-the-fly without Node.js or build steps:

- **JIT CSS:** Zero-config utility CSS generator parsing HTML view utility classes.
- **JIT Icons & Logos:** Automatically renders SVG masks for Tabler icons (`<i class="icon-user">`) and Simple Icons brand logos (`<i class="logo-github">`). Served inline or cached via `/jiticons` / `/jitlogo`.
- **JIT Components:** Material styling abstractions (`.button`, `.card`, `.prose`).
- **JIT JS & Hydrate:** Declarative client behavior kernel driven by `data-kitwork-action` verbs (`toggle`, `dialog`, `tab`, `copy`, `theme`) and `$ page scope` state synchronization.

---

## 🔒 Security Model

| Security Layer | Enforcement Mechanism |
| :--- | :--- |
| **Language Sandbox** | Unbounded loops, eval, and arbitrary class instances rejected at compile time. |
| **Energy Accounting** | Every VM opcode decrements energy; execution aborts upon reaching `MaxEnergy`. |
| **Stack Sentinel** | Execution depth hard-capped at 64 call frames; prevents host goroutine stack overflow. |
| **SSRF Shield** | Outbound `http` requests block loopback (`127.0.0.1`), private RFC1918 IPs, and AWS metadata endpoints. |
| **SQL Safety** | Parameterized queries enforced; `.update()` and `.delete()` without `.where()` fail strictly. |
| **Environment Isolation** | Tenant VMs can only read keys defined in their local `apps/<identity>/<domain>/.env`. |
| **Rate Limiting** | Host-level & tenant-level token bucket rate limiters per IP, User, Browser fingerprint, or endpoint. |

---

## 📊 Performance & Benchmarks

Benchmarked on an Intel i7-11850H (8 Cores / 16 Threads) running Go microbenchmarks (`work/bench_core_test.go`) and `k6` HTTP load scenarios against live tenant routes:

| Metric | Measured Engine Performance |
| :--- | :--- |
| **VM Execution Speed** | ~36,500,000 instructions / sec |
| **Instruction Latency** | ~27 nanoseconds / instruction |
| **HTTP Throughput (k6)** | **33,287 requests / sec** (200 concurrent VUs) |
| **Latency Under Load** | **p50: 3.5 ms** · **p95: 18.8 ms** |
| **Success Rate** | **100.00%** (0 errors across 499,510 requests) |
| **Full Tenant Cold Boot** | **9.8 ms** (ESM resolution + AST compile + route tree + render plan) |
| **Script Pipeline Cold Boot** | **1.7 ms** (Lex ➔ Parse ➔ Compile ➔ VM Execute) |

---

## 🧪 Preflight Verification & CLI

Before deploying tenant code to production, run preflight validation to verify routes, imports, environment variables, and HTML templates without starting a network listener:

```bash
# Verify the entire host engine setup and all tenant applications
go run . check
```

Or run test suites inside the `engine/` directory:

```bash
cd engine
go build ./...
go test ./...
go vet ./...
go test -race ./...
```

---

## 📄 License & Sponsorship

Kitwork Engine is dual-licensed software maintained by the Kitwork Foundation:

- **Open Source License:** **GNU Affero General Public License v3.0 (AGPL-3.0)** — see [`LICENSE`](LICENSE).
- **Commercial License:** For proprietary closed-source deployments, enterprise embedding, or custom licensing, contact **[support@kitwork.org](mailto:support@kitwork.org)**.

**Your applications are yours.** The AGPL covers the engine, not the code you run on it. The
[Application Exception](LICENSE-EXCEPTION.md) states this in writing: `.kitwork.js` modules,
templates, assets and everything the engine generates from them — including pages served to your
visitors — are separate works you license however you like. What AGPL section 13 asks for is the
other case: if you **modify the engine itself** and offer that modified engine over a network,
publish those modifications.

Components meant to be copied into your own codebase are deliberately permissive rather than AGPL:
`@kitwork/kitjs` (the client kernel) and `kitwork.d.ts` (editor type definitions) are MIT.

Contributions are welcome — start with [`CONTRIBUTING.md`](CONTRIBUTING.md). Because of the
commercial half of the dual licence, a first contribution needs the agreement in [`CLA.md`](CLA.md).

**Created by Huỳnh Nhân Quốc** · Kitwork Foundation · [Sponsor on GitHub](https://github.com/sponsors/huynhnhanquoc)
