# Kitwork Engine

> **The cloud became an estate to operate. Kitwork is a disagreement.**

[![Go Version](https://img.shields.io/badge/go-1.25+-black?style=flat-square&logo=go)](https://golang.org)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue?style=flat-square)](#author--license)
[![VM Latency](https://img.shields.io/badge/instruction-70ns-green?style=flat-square)](#performance)
[![Cold Boot](https://img.shields.io/badge/cold%20boot-%3C10ms-green?style=flat-square)](#performance)

**Kitwork Engine is cloud infrastructure compiled into a single Go binary.** It runs a JavaScript dialect on a custom stack-based bytecode VM — with energy metering, per-tenant sandboxing, hot reload, an integrated router, a zero-allocation database layer, and a template engine. One process hosts unlimited domains. Deploying a website means dropping a folder.

Every system starts simple: one app, one database, one server. Then caching brings Redis, queues bring RabbitMQ, events bring Kafka, orchestration brings Kubernetes — and years later the team operates machinery instead of shipping product. Kitwork collapses that estate back into **one runtime with one philosophy**, from the language (which cannot loop forever) to the cluster (which degrades instead of dying).

---

## The Contract — five rules

Everything in this repository follows five falsifiable rules. If a feature violates one, the feature is wrong.

1. **What is supported behaves exactly like JavaScript.** No almost. No silent nulls.
2. **What is removed fails at compile time, with an explanation.** Absence is a statement, never a surprise.
3. **Every workload is bounded** — by the language (no unbounded constructs compile) and by the VM (every instruction is energy-metered).
4. **One binary is the whole platform.** Router, VM, database layer, templates, TLS — if it needs a second service to work, it doesn't ship.
5. **State outlives machines.** Node RAM holds nothing precious; the database is the only memory.

These rules are why the rest of this document can make strong claims.

---

## Table of Contents

- [Why a custom VM?](#why-a-custom-vm)
- [Quick Start](#quick-start)
- [The Language](#the-language-javascript-you-know-bounded-by-design)
- [A Folder Is a Website](#a-folder-is-a-website)
- [Architecture](#architecture)
- [Security Model](#security-model)
- [Performance](#performance)
- [The Cluster](#the-cluster)
- [FAQ](#faq)
- [Documentation](#documentation)

---

## Why a custom VM?

Running untrusted tenant code is *the* defining problem of cloud infrastructure. The industry has three answers:

| Approach | Isolation | Cold boot | Footprint | Can tenant code hurt the host? |
| :--- | :--- | :--- | :--- | :--- |
| Containers / microVMs | OS-level | 100ms – seconds | an image per tenant | Yes — anything goes inside |
| Embedded V8 / goja | interpreter-level | ~ms | heavy (V8) or slow (reflection) | Yes — `while(true)` needs watchdogs |
| **Kitwork VM** | **bytecode-level** | **< 10ms** | **one Go binary** | **No — unbounded constructs do not compile** |

Kitwork owns the entire pipeline — lexer, parser, compiler, opcodes, VM — so safety is a property of the **language definition**, not a patch around someone else's runtime. A tenant cannot harm a node. That single guarantee is what later allows any node to absorb any tenant ([see The Cluster](#the-cluster)).

> Safety was not bolted on, so it costs nothing: the VM core runs at **~14.1 million ops/sec** with **near-zero GC pressure**.

---

## Quick Start

### Embed in your Go application

```bash
go get github.com/kitwork/engine
```

```go
package main

import (
    "log"

    "github.com/kitwork/engine"
)

func main() {
    if err := engine.Run("config.kitwork.yml"); err != nil {
        log.Fatalf("engine startup failed: %v", err)
    }
}
```

### One configuration file, environment-aware

```yaml
port: 8080
root: "tenants"           # multi-tenant root directory

domains:                   # automatic HTTPS via ACME
  - kitwork.vn

max_energy: 1000000        # VM energy budget per execution
hot_reload: true           # atomic bytecode swap on save, <10ms

database:
  type: "postgres"         # PostgreSQL / MySQL
  host: "localhost"
  port: 5432
  user: "postgres"
  password: "${DB_PASSWORD}"   # env vars expand at boot
  name: "postgres"
  max_open: 50
  max_idle: 10
```

### Your first endpoint

```javascript
import { router, database } from "kitwork"

const db = database.connection()

router.get("/api/hello").handle((req, res) => {
    return res.json({
        status: "active",
        time: new Date().toISOString()
    })
})

router.get("/api/users").handle((req, res) => {
    const users = db.table("user").list(10)
    return res.json({ success: true, users: users })
})
```

Save the file. The engine recompiles and atomically swaps the bytecode in under 10 milliseconds. No build step. No restart. No toolchain.

---

## The Language: JavaScript you know, bounded by design

Tenant logic is written in a JavaScript dialect, and Rule 1 governs it: **everything that is supported behaves exactly like standard JS.**

- Operators: `===`, `!==`, ternary `?:`, `%`, `+=` `-=` `*=` `/=`, `++` `--`
- Globals: full `Math` · real `Date` (`Date.now()`, `new Date(ms | string | y,m,d)`, all getters, `toISOString`) · `JSON` · `Object.keys / values / entries / assign / fromEntries` · `Number` / `String` / `Boolean` conversion · `parseInt` / `parseFloat`
- Complete String & Array method families, **Unicode-correct** — indices count characters, not bytes: `"Phường".length === 6`, slicing never breaks Vietnamese text
- Arrow functions, template literals, spread, destructuring, multi-parameter lambdas
- Lexical closures at **any nesting depth** — `forEach` inside `forEach` mutating an outer array works exactly as in JS

```javascript
orders.filter(o => o.total > 500000)
      .map(o => ({ id: o.id, vat: (o.total * 0.1).toFixed(0) }))
      .sort((a, b) => b.vat - a.vat)

"Phường Bến Nghé".indexOf("Bến")   // 7 — character index, Unicode-safe
"5".padStart(3, "0")                // "005"
items.reduce((acc, x) => acc + x.qty, 0)
```

### Deliberately removed — this is the product, not a gap

| Removed | Why | Write instead |
| :--- | :--- | :--- |
| `while`, `do` | No unbounded loops on shared compute, ever | `.map()` / `.filter()` / `.find()` / `.forEach()` |
| `try` / `catch` / `throw` | One visible error path, not invisible jumps | `.done(cb)` / `.fail(cb)` |
| `switch` | Smaller language, fewer ways to disagree | `if / else` or object lookup |
| `class` | Data is data; behavior is functions | object literals + arrow functions |

Per Rule 2, using a removed keyword produces a compile error that teaches:

```text
assemble error: Kitwork không hỗ trợ vòng lặp 'while' (loại bỏ có chủ đích để
tránh vòng lặp vô tận). Hãy dùng .map() / .filter() / .find() trên mảng dữ liệu.
```

This is the same trade Google made with Starlark and CEL, and Linux made with eBPF: when code runs on shared infrastructure, *provable termination is worth more than expressive power*. Kitwork simply makes the trade in a syntax millions already know.

Full language reference: [ENGINE_CAPABILITIES.md](./ENGINE_CAPABILITIES.md)

---

## A Folder Is a Website

One process serves unlimited domains, routed by hostname:

```text
tenants/
  └─ <tenant-identity>/
       └─ <domain>/                    e.g. kitdata.vn/
            ├─ app.kitwork.js          routes & logic → compiled to bytecode
            ├─ views/                  pages, layouts, partials, {{ bindings }}
            ├─ static/                 .static() disk-cache snapshots
            └─ assets/                 css, js, media — served on the zero-VM fast path
```

Drop a folder in, point DNS at the node, the domain is live — each tenant in its own VM sandbox with its own energy budget.

Deployment is `rsync`. Rollback is `git checkout`. It is the operating model of 2005 shared hosting with the isolation and economics of modern serverless — on hardware you control.

---

## Architecture

```mermaid
graph TD
    A[Incoming HTTP Request] --> B{Radix Trie Router}
    B -- Static asset match --> C[Zero-VM fast path: serve from disk]
    B -- Dynamic logic match --> D{Static cache check}
    D -- Hit: .static file --> E[Sequential read → stream body]
    D -- Miss --> F[Acquire VM from sync.Pool]
    F --> G[FastReset state]
    G --> H[Execute stack-based bytecode]
    H --> I[Database queries / ACID transactions]
    H --> J[HTTP fetch / integrations]
    H --> K[Render views / HTML binding]
    K --> L[Save static cache snapshot]
    L --> M[Send response & recycle VM]
```

### Compilation pipeline — source to bytecode, all in Go

1. **Lex & parse** — a hand-written recursive-descent parser builds the AST. No external parser dependencies.
2. **Bundle** — multi-file ESM (`import` / `export`) resolved by esbuild at compile time. No Node.js required.
3. **Compile** — the AST flattens into linear `uint8` opcode sequences plus a constants pool. High-level operations (DB queries, template rendering) get specialized opcodes instead of generic call chains, keeping bytecode short.
4. **Execute** — a stack-based VM with constant-time variable access, multi-level lexical scope chains, and per-opcode energy accounting.

### The zero-allocation philosophy

- **`sync.Pool` VM recycling** — pre-allocated VMs reset in place (`FastReset`); nothing is re-allocated per request
- **Sovereign value model** — a custom `value.Value` struct stores primitives directly, avoiding `interface{}` boxing and pointer-chasing
- **Radix trie routing** — O(L) in path segments, independent of endpoint count; `:param` and `*` wildcards without regex
- **Zero-allocation query builder** — SQL compiled in ~230ns, ~20x faster than reflection-based ORMs ([QUERY_BUILDER.md](./QUERY_BUILDER.md))

### `.static()` — disk caching that feeds the kernel

Responses snapshot into a single offset-delimited binary file:

```text
+------------------------+-------------------------------+---------------------+
| 10-byte length header  | JSON metadata (L bytes)       | raw body payload    |
|                        | status, content-type, headers | HTML, JSON, images  |
+------------------------+-------------------------------+---------------------+
```

One open, one sequential read, then `io.Copy` straight to the socket. No `Seek` syscalls, no RAM staging; expiry rides the OS file ModTime.

---

## Security Model

| Layer | Mechanism |
| :--- | :--- |
| **Language** | Unbounded constructs rejected at compile time — `while(true)` cannot exist in bytecode |
| **Energy budget** | Every opcode carries a weight; execution aborts the instant `max_energy` is exceeded |
| **Stack sentinel** | Call depth > 64 raises a controlled VM error — the Go runtime stack is never at risk |
| **Memory guards** | String builders (`repeat`, `padStart`) hard-capped; one tenant cannot balloon node RAM |
| **Source mapping** | Every instruction maps to a source line — failures report `app.kitwork.js:L53`, not hex dumps |
| **ACID boundaries** | Script transactions wrap `*sql.Tx` with deferred recovery: any VM error triggers automatic rollback, zero connection-pool leakage |

---

## Performance

Load-tested with `k6` against a single local node ([methodology](./BENCHMARK.md)):

| Metric | Result |
| :--- | :--- |
| VM core throughput | ~14,100,000 ops/s |
| Instruction latency | ~70 ns |
| HTTP throughput | 12,726 req/s |
| Response latency | p50 1.16 ms · avg JSON 90 µs |
| Success rate | 100.00% (0 / 127,292 failed) |
| Cold boot | < 10 ms |
| GC pressure | near zero |

---

## The Cluster

A Kitwork cluster has **no special servers**. Every node runs this same engine; only responsibility differs — Gateway, Coordinator, Worker. Coordination obeys the Contract:

- **State outlives machines** — the database is the only memory (Rule 5)
- **Correctness never rides the bus** — elections are database leases, not homemade consensus
- **Lose efficiency before availability** — when Workers die, Coordinators execute; when Coordinators die, Gateways execute
- **Every workload is bounded** — Rule 3 is what makes absorbing a neighbor's load safe. The language is the cluster's immune system.

Performance degrades. The system continues.

Full design and roadmap: [CLUSTER.MD](./CLUSTER.MD)

---

## FAQ

**What is Kitwork Engine?**
A multi-tenant cloud runtime in a single Go binary: it compiles a bounded JavaScript dialect to bytecode and executes it on a custom stack-based VM with energy metering, integrated routing, database access, caching, and templating.

**Is it Node.js-compatible?**
No — deliberately. It is JS-*familiar*: supported syntax behaves exactly like JavaScript, while unbounded constructs (`while`, `try/catch`, `class`) are removed by design and rejected at compile time with instructive errors.

**Why not embed V8 or goja?**
Owning the compiler makes safety (termination, energy budgets, memory caps) a property of the language itself rather than a watchdog around someone else's runtime — and keeps cold boots under 10ms in a small binary.

**Who is it for?**
SaaS platforms hosting untrusted tenant logic, edge and serverless workloads that need instant cold starts, programmable API gateways, and teams who want cloud capability without operating a Kubernetes estate.

**What databases are supported?**
PostgreSQL and MySQL, through a zero-allocation fluent query builder with ACID transaction support.

**What makes it suited to the Vietnamese market?**
Built-in NAPAS 247 / VietQR-compliant QR generation (SVG, every bank BIN, EMVCo-checked) and Unicode-correct strings where indices count characters — Vietnamese text never breaks.

**Is it production-ready?**
The engine powers live multi-tenant sites today. The clustering layer is design-complete ([CLUSTER.MD](./CLUSTER.MD)) and being implemented in phases.

---

## Documentation

| Document | Contents |
| :--- | :--- |
| [ENGINE_CAPABILITIES.md](./ENGINE_CAPABILITIES.md) | Language reference: JS compatibility, removed keywords, cache / static / assets, ESM bundling |
| [CLUSTER.MD](./CLUSTER.MD) | Distributed architecture: invariants, roles, degradation ladder, roadmap |
| [QUERY_BUILDER.md](./QUERY_BUILDER.md) | The zero-allocation database layer |
| [BENCHMARK.md](./BENCHMARK.md) | Load-test methodology and raw numbers |

---

## Author & License

> *"Logic is the soul of machines. Emotion creates civilization."*

Kitwork is written the way one writes an essay — every line argued over, nothing kept that cannot be defended. While the world uses AI to automate everything, this project uses it as a mirror. It is public not because it is finished, but because it is honest: small enough to understand, strange enough to matter, and built to keep running after everything around it fails.

Developed by **Huỳnh Nhân Quốc** under the **Kitwork Foundation** · Apache 2.0 License

Support development: [Sponsor Kitwork](https://github.com/sponsors/huynhnhanquoc)
