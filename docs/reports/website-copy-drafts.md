# Kitwork Engine — Website Copy Drafts

This document contains website copy drafts for Kitwork's public website sections, adhering strictly to technical accuracy without unverified claims ("fastest", "100% bug-free").

---

## 1. Homepage Copy Draft

### Headline
**Run Hundreds of Isolated Web Systems & AI Agents Inside a Single Go Process.**

### Sub-headline
Kitwork is a sovereign multi-tenant execution engine written in Go. Featuring a custom stack-based Virtual Machine running a statically analyzable JavaScript subset, Kitwork delivers instant startup, zero CGO dependencies, and predictable memory footprint.

### Primary CTA
`go get github.com/kitwork/engine` · [Read the Docs](#) · [View on GitHub](#)

---

## 2. "Why Kitwork" Copy Draft

### Key Value Pillars
1. **Zero CGO Dependencies**: Single 14MB Go binary compiling cleanly across Windows, Linux, and macOS without V8 or C++ toolchains.
2. **Deterministic Security Sandbox**: Language subset bans `while` loops, `try/catch`, and prototype mutation. Opcodes consume gas energy (`MaxEnergy`) to halt runaway scripts.
3. **Sovereign Multi-Tenancy**: Run isolated domain runtimes (`SiteRuntime`) sharing database connections and background workers safely inside one host process.
4. **Zero-Allocation Hot Paths**: Reusable VM pools and pre-compiled filesystem route trees eliminate GC overhead on hot HTTP request paths.

---

## 3. Architecture & VM Copy Draft

### The 4-Tier Ownership Model
`Host -> AppRuntime(identity) -> SiteRuntime(domain) -> Generation(version) -> RequestScope(HTTP request)`

Every tenant site folder is compiled into an immutable `RouteTree` and `RenderPlan`. Generation updates swap compiled route graphs atomically without dropping active client requests.

---

## 4. Agent Runtime Copy Draft

### Sandboxed AI Agent Execution
Kitwork provides the ideal runtime for autonomous AI Agents:
- **Durable SQLite Memory**: Identity-scoped database connections for conversation history.
- **Sandboxed Tool Execution**: Capability registry restricting agent tool capabilities.
- **Gas Accounting**: Per-opcode energy ceilings preventing infinite tool recursion.

---

## 5. Benchmarks, Documentation, Open Source, Roadmap & Contributing Copy

### Benchmarks Page Note
> All benchmark results are reproducible using `go test ./work -bench BenchmarkServeHandlerCorpus -benchmem`. Tests run on standard hardware with zero-allocation request hot paths verified.

### Open Source & License
Kitwork is released under the AGPL-3.0 License with a CLA exception for commercial licensing.
