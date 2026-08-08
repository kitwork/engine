# Kitwork Engine — API Design Review

This document evaluates the public and developer-facing APIs of the Kitwork Engine against key software engineering criteria: predictability, consistency, safety, extensibility, and alignment with Kitwork's design philosophy.

---

## 1. API Evaluation Summary

| API Area | Evaluation Criteria | Strengths | Weaknesses / Design Flaws | Recommendation |
|---|---|---|---|---|
| **Inline Error (`.safe()`)** | Ergonomics & Safety | Single unified return shape (`SafeResult`). No manual destructuring required. | Cannot be called on hard VM errors in JS due to interpreter abort behavior. | Fix VM interpreter to allow `.safe()` to execute on `Invalid` stack values. |
| **Environment Config (`env`)** | Simplicity & Coercion | Auto-coerces numbers, booleans, and strings. Supports `env.require(key)`. | Uses `Proxy` to bypass cast method shadowing (`int`, `string`). Default-true booleans cannot use `\|\| true`. | Retain `Proxy`, document `env.require()` as preferred pattern for strict variables. |
| **Query Builder (`entity().table()`)** | Readability & SQL Safety | Enforces parameterized queries and mandatory `where()` on `update()` / `delete()`. Auto-IN/LIKE detection. | Discrepancy between `first()` returning `null` vs `list()` returning empty array `[]`. | Preserve semantics; standardize return type documentation. |
| **Response Context (`ctx`)** | Expressiveness | Fluent chaining (`ctx.status(201).json(...)`). | Dual behavior: calling `ctx.view()` sets a deferred view builder, while returning a map auto-renders JSON. | Keep current behavior; deprecate ambiguous legacy aliases (`ctx.render()`). |
| **Background Work (`kitwork().go`)** | Decoupling & Simplicity | Takes a lambda, snapshots inputs, and delegates work to identity pool. | Unclear lifetime bound if background job attempts to use request-scoped capabilities. | Restrict `go()` lambdas to transient or app-scoped capabilities only. |

---

## 2. Deep Dive API Audits

### 2.1 Error Handling API: `.safe()` vs. `.must()`
- **Current State**: Kitwork deliberately eliminated the dual array/object error return shapes in favor of one `SafeResult` object returned by `.safe()`:
  ```javascript
  const res = db.table("user").where("id", id).first().safe()
  if (!res.ok) return ctx.status(404).json({ error: res.error })
  return ctx.json(res.value)
  ```
- **Design Assessment**:
  - **Pros**: `res.ok` boolean getter works cleanly without parens. `res.error` returns a readable error message string instead of a nested `{ code, message }` object, preventing double-nested JSON responses.
  - **Cons**: As identified in the consistency audit, calling `.safe()` on expressions that produce a hard `Invalid` error (e.g. `fail("boom").safe()`) fails because the VM halts before `.safe()` is reached.

### 2.2 Environment Variables API: `env` Proxy & Coercion
- **Current State**: `env` auto-coerces values:
  ```javascript
  const port = env.PORT || 8080 // Number 8080 if not set
  const secret = env.require("JWT_SECRET") // Panics/errors if missing
  ```
- **Design Assessment**:
  - **Pros**: Per-tenant `.env` isolation prevents cross-tenant secret leaking.
  - **Cons**: Standard OS environment variable keys (`HOSTNAME`, `PATH`, `HOME`) are filtered/overridden to prevent process host leakage. Boolean default `env.ENABLE_FEATURE || true` always evaluates to `true` if `ENABLE_FEATURE` is `false` (since `false || true` is `true`).

### 2.3 Query Builder API: `database.entity().table(...)`
- **Current State**: Identity-scoped database API enforces multi-tenant security:
  ```javascript
  const users = ctx.db.table("users").where("active", true).list()
  ```
- **Design Assessment**:
  - **Pros**: Prevents SQL injection by enforcing parameterized query args. Mutating operations (`update()`, `delete()`) strictly require a `.where()` clause, preventing accidental full-table wipes.
  - **Cons**: Calling `.update({})` or `.delete()` without `.where()` throws a hard runtime error.

### 2.4 Response Context API: `ctx.view()` vs `ctx.json()`
- **Current State**: Handlers return data or invoke fluent context helpers:
  ```javascript
  export default route("/users").get((ctx) => {
    return ctx.view({ users: ctx.db.table("users").list() })
  })
  ```
- **Design Assessment**:
  - **Pros**: Clean separation of data binding from HTTP response flushing.
  - **Cons**: `ctx.render()` exists as a legacy alias for `ctx.view()`, creating redundant API spellings.

---

## 3. Backward Compatibility & Migration Strategy
1. **Deprecations**:
   - Deprecate `ctx.render()` in favor of `ctx.view()`.
   - Deprecate any leftover references to `.result()`.
2. **Breaking Changes Policy**:
   - VM opcode space and bytecode format (VM v2 contract in `contract_test.go`) are append-only.
   - Public APIs exported via `import { ... } from "kitwork"` require a formal deprecation period before removal.
