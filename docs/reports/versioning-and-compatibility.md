# Kitwork Engine — Versioning & Compatibility Strategy

This document defines the formal semantic versioning rules, compatibility guarantees, bytecode contracts, deprecation policies, and experimental feature guidelines for the Kitwork Engine.

---

## 1. Versioning Architecture Overview

```text
[ Kitwork Engine Release Version (e.g. v1.0.0-RC1) ]
  ├── 1. Host Public API (SemVer: MAJOR.MINOR.PATCH)
  ├── 2. Kitwork JS Subset Language Spec (SemVer)
  ├── 3. Bytecode Format Version (Frozen BytecodeVersion = 2)
  ├── 4. Storage Encoding Version (ProgramEncodingVersion = 1)
  └── 5. Compiler Schema Version (CompilerSchemaVersion = 1)
```

---

## 2. Versioning Guarantees by Dimension

### 2.1 Public JavaScript API Surface (SemVer v1.x Guarantee)
- **Scope**:
  - `import { router, route, database, env, kitwork } from "kitwork"`
  - `Context` properties (`ctx.request`, `ctx.response`, `ctx.db`, `ctx.view()`, `ctx.json()`, `ctx.status()`, `ctx.query`, `ctx.params`)
  - Template directives (`{{ .binding }}`) and layout structure (`_layout_.kitwork.html`, `page.kitwork.html`).
- **Guarantee**: Backward compatibility maintained across all v1.x minor/patch releases. Removing or altering a public method requires a deprecation warning period of at least **one minor version cycle** (e.g. deprecate in v1.2, remove in v2.0).

### 2.2 Bytecode Contract & VM Freeze (v2 Frozen)
- **Scope**: VM Opcode numeric assignments, instruction specs, constant pool encoding, stack delta rules.
- **Guarantee**:
  - VM v2 contract is frozen and enforced by `engine/runtime/contract_test.go`.
  - `BytecodeVersion == 2` cannot drift silently.
  - Adding or modifying opcodes requires an explicit `BytecodeVersion` increment decision and golden test update.

### 2.3 Local Bytecode Cache Compatibility (`FileCache`)
- **Scope**: `.kitwork/cache/bytecode/` disk cache artifacts.
- **Guarantee**:
  - Artifacts serialize `ProgramEncodingVersion`, `BytecodeVersion`, and source SHA-256 fingerprints using `Program.MarshalBinary`.
  - Upgrading the Kitwork engine binary automatically invalidates old bytecode cache files, triggering transparent source re-compilation without crashing host or tenant processes.

### 2.4 Database Schema Migration Policy
- **Scope**: Host system SQLite databases and per-tenant application SQLite stores.
- **Guarantee**:
  - Host system tables (rate limits, persistent responses, crons) use idempotent `CREATE TABLE IF NOT EXISTS` and forward-compatible column additions.
  - Per-tenant databases belong to the tenant application. The engine never drops tenant tables.

---

## 3. Deprecation & Experimental Feature Policies

### 3.1 Deprecation Process
1. Mark API in code with `// Deprecated: use X instead` godoc comments.
2. Emit a non-fatal host warning log (`slog.Warn`) on first call.
3. Maintain the deprecated API for at least one minor release version.
4. Remove the API only on major version releases (v2.0.0).

### 3.2 Experimental API Policy
- Features classified as **Experimental** (e.g. Kit JS Hydrate, SPA morphing, or read-only Logic Capsules) are opt-in and explicitly tagged in documentation.
- Experimental APIs are exempt from SemVer stability guarantees until promoted to **Beta** or **Stable**.
