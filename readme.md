# Kitwork Engine: Logic Organism Specification

> **“Logic is a living organism, not a passive set of commands.”**

Kitwork Engine is a high-performance, distributed logic orchestration system written in **Golang**. It treats source code as **DNA**, the Engine as an **Incubation Environment**, and each script as a **Living Organism (Work)** with its own identity, lifecycle, and execution context.

## 1. Core Architecture: Stack-based VM

Unlike traditional interpreters, Kitwork uses a custom-built **Stack-based Virtual Machine**. Every script undergoes a transformation process:
**JS source → AST → Bytecode → VM Execution**.

### Performance at Scale
- **Throughput:** ~2.5 Million operations per second on standard hardware.
- **Latency:** Execution in nanoseconds (~380ns/op).
- **Memory Efficiency:** Near-zero allocations during execution thanks to advanced **sync.Pool** for VM Stacks, Compiler buffers, and Environment scopes.

## 2. Modern DSL: Context-based & Side-Effect Free

The script environment is designed for developer experience (DX). The `work` object is the **Context** that manages everything from infrastructure declaration to response handling.

### Discovery Before Execution
Infrastructure (Routes, Retries, Versioning) is **discovered** from the code itself.
1. **Discovery Phase:** The engine runs the script once to "extract" the Blueprint.
2. **Execution Phase:** The compiled Bytecode is executed by the VM when triggered (HTTP, Cron, Queue).

## 3. High-Level Examples

### The Modern API (Context-based)
Everything revolves around the `work` context. Methods like `json()`, `now()`, and `db()` are available globally as shortcuts.

```javascript
const w = work({ name: "api users" });

// INFRASTRUCTURE: Discovered by the engine
w.router("POST", "/users");
w.retry(3, "1s");

// LOGIC: Executed by the VM
let data = db().from("users").take(10);

// RESPONSE: Automatic JSON detection if 'return' is used
return data; 
```

### Chaining & Transformation (Prototype Support)
Every value in Kitwork has functional prototypes for quick casting and formatting.

```javascript
let raw = "123.45";
let total = raw.float().int(); // Chaining: string -> float -> int

json({
    status: "ok",
    value: total.string(), // Convert back to string
    at: now().json()      // Chain custom formatters
});
```

## 4. System Components

| Component | Responsibility | Performance |
| :--- | :--- | :--- |
| **Compiler** | JS AST to Stack-based Bytecode | Pooled & Optimized |
| **VM** | Instruction execution unit | Nanosecond latency |
| **Value** | 24-byte atomic unit for scalar/ref types | Cache-friendly |
| **Pools** | Recycles Stacks, Envs, and Buffers | Zero-alloc runtime |

## 5. Built-in Capability (STDLIB)

- **`work()`**: Declare the logic organism and its infrastructure.
- **`db()`**: Fluent query builder for database operations.
- **`now()`**: Native high-precision time.
- **`json()`, `text()`, `html()`**: Formatters and response handlers.

## 6. Roadmap

- [x] **Bytecode VM**: Full instruction set implementation.
- [x] **sync.Pool Integration**: Extreme memory efficiency.
- [x] **Prototype Chaining**: `.int()`, `.string()`, `.json()` for all values.
- [ ] **Durable Runtime State**: Serializing VM state for long-running workflows.
- [ ] **Native SQL Drivers**: Real-world database connections (Postgres/MySQL).

---

**Closing Note:** Kitwork is not a framework that *calls* logic. It is an ecosystem where logic is discovered, given conditions to live, and allowed to disappear when its purpose ends.
