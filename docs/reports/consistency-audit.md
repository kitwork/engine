# Kitwork Engine — Consistency Audit

This document presents a systematic audit of code, documentation, comments, API names, error propagation, and execution behavior across the Kitwork Engine codebase.

---

## 1. Discrepancy Matrix

### 1.1 Source Code vs. Documentation & Comments

| Area | Stated in Docs / Comments | Actual Implementation | Impact & Location |
|---|---|---|---|
| **`.safe()` Error Rescue** | `navigation.go` and `result.go` state that `.safe()` rescues hard `Invalid` errors (e.g. DB failures, `fail("boom")`). | Compiler schema v3 lowers `X.safe()` to an internal protected evaluator. The VM rescues only `RUNTIME_ERROR`; energy, cancellation, stack, program and native-panic diagnostics remain fatal. | Resolved. Compiler, runtime, artifact round-trip, pooled determinism and HTTP contracts cover the boundary. |
| **Legacy `.result()` references** | Earlier tests referred to both `.result()` and `.safe()`. | `.result()` is removed; current VM contracts cover only the single `SafeResult` shape. | Resolved. |
| **3-Tier Architecture RFC** | [ARCHITECTURE.md](file:///d:/project/kitwork/engine/docs/ARCHITECTURE.md) describes route layout using `app/` subfolders and `index.kitwork.js`. | Production layout uses `apps/<identity>/<domain>/app/` with `router.kitwork.js` and `page.kitwork.html`. | Medium doc ambiguity. Header note added to `ARCHITECTURE.md`, but file remains misleading. |
| **`Tenant` Ownership** | `Tenant` struct comments refer to `Tenant` as resource owner. | [STABILITY.md](file:///d:/project/kitwork/engine/docs/STABILITY.md) & [RUNTIME_ARCHITECTURE.md](file:///d:/project/kitwork/engine/docs/RUNTIME_ARCHITECTURE.md) confirm `Tenant` is now a compatibility facade; real ownership lies in `app.Runtime` and `site.Generation`. | Medium conceptual drift. |

---

## 2. API & Behavioral Inconsistencies

### 2.1 Error Handling API Audit (`.safe()`, `.result()`, `.must()`)
1. **`.safe()` protected evaluation**:
   - **Value Layer**: `Value{K: Invalid, V: "boom"}.Safe()` produces `SafeResult{ ok: false, error: "boom" }`.
   - **VM Layer**: `fail("boom").safe()` now evaluates the receiver inside a compiler-generated protected lambda. Ordinary runtime errors become `SafeResult`; host and resource-boundary diagnostics still fail closed.
2. **`.must()` Scope Asymmetry**:
   - `id.Generator.Must(length)` exists in Go backend code.
   - JS subset has no `.must()` method on DB queries or value wrappers. Calling `.must()` on a JS query builder fails as an unknown method.

### 2.2 Nil vs. Zero Value (`Value{}`)
- `Value{}` default Go initialization sets `K = 0` (`value.Invalid`), **not** `value.Nil` (which is `1`).
- Returning an uninitialized `Value{}` from a Go native function into the VM stack causes the VM to treat it as an unhandled runtime error.
- **Rule Violation Risk**: Developers writing native bridge functions often return `Value{}` assuming it represents `null`/`nil`.

### 2.3 Syntax Restrictions (Array vs Object Trailing Commas)
- Parser ([parser.go](file:///d:/project/kitwork/engine/compiler/parser.go)) strictly rejects trailing commas in array literals (`[1, 2,]` -> Parse Error: `unexpected token comma`).
- Object literals (`{ a: 1, b: 2, }`) allow trailing commas.
- While deliberate in language design, this asymmetry causes developer friction.

### 2.4 Cast Method Shadowing on `Value`
- `value.Invoke(name, args)` ([methods.go](file:///d:/project/kitwork/engine/value/methods.go)) checks built-in cast methods (`int`, `float`, `string`, `json`, `len`) before object/map key lookup.
- If a map object contains a property named `"int"` or `"string"`, calling `map.int` executes `Kind.Method("int")` instead of fetching the map property value!
- `env` avoids this bug by wrapping its map inside a `Proxy` object that overrides `OnInvoke`. Plain maps in user code remain vulnerable to key shadowing.

### 2.5 Database Result Representation
- `entity.where(...).first()` returns `Value{K: Nil}` when no record matches.
- `entity.where(...).list()` returns `Value{K: Array, V: &[]value.Value{}}` (empty array) when no records match.
- Query execution errors return `Value{K: Invalid, V: "error message"}` (hard failure).

### 2.6 Router Handler Error Response Lifecycle
- When a route handler fails with `Value{K: Invalid}`, `reqRouter.err` is set.
- If an `.error((ctx) => ...)` hook is defined, it runs. If `.error()` returns a JSON object without explicitly calling `ctx.status(500)`, `reqRouter.response` retains default status `200` while delivering the error payload, causing HTTP response status inconsistencies.
