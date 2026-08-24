# KitURL — Kitwork Capability Mapping Matrix

This document maps every product requirement of the KitURL service directly to Kitwork Engine's underlying implementation packages, APIs, existing tests, readiness status, acceptable workarounds, and engine modification flags.

---

## 1. Capability Mapping Matrix

| KitURL Product Requirement | Kitwork Engine API / Feature | Engine Package Source | Existing Test Reference | Readiness Status | Workaround Accepted? | Requires Engine Patch? |
|---|---|---|---|---|---|---|
| **Short Code Generation** | `id.Shortlink()` / `id.Short()` generator | [engine/id/id.go](file:///d:/project/kitwork/engine/id/id.go) | `id_test.go` | **Ready** | No | No |
| **HTTP 301/302 Redirect** | `ctx.response.redirect(url, status)` / `reqRouter.response.Header()` | [engine/work/response.go](file:///d:/project/kitwork/engine/work/response.go) | `router_features_test.go` | **Ready** | No | No |
| **Link Schema Storage** | `ctx.db.table("links")` SQLite ORM builder | [engine/work/db.sqlite.go](file:///d:/project/kitwork/engine/work/db.sqlite.go) | `db_sqlite_test.go` | **Ready** | No | No |
| **Unique Slug Validation** | `ctx.db.table("links").where("slug", s).first()` | [engine/work/entity_scope.go](file:///d:/project/kitwork/engine/work/entity_scope.go) | `entity_scope_test.go` | **Ready** | No | No |
| **Rate Limiting** | `router.ratelimit({ rate: 10, per: "1m" })` | [engine/work/ratelimit.go](file:///d:/project/kitwork/engine/work/ratelimit.go) | `router_ratelimit_rules_test.go` | **Ready** | No | No |
| **Expired Link Cleanup** | `_cron/cleanup.kitwork.js` app cron scheduler | [engine/work/cron.go](file:///d:/project/kitwork/engine/work/cron.go) | `cron_persist_test.go` | **Ready** | No | No |
| **Detached Counter Update** | `kitwork().go(fn)` background worker pool | [engine/work/go.go](file:///d:/project/kitwork/engine/work/go.go) | `go_test.go` | **Ready** | No | No |
| **Environment Configuration** | `env.PORT`, `env.require("API_TOKEN")` | [engine/work/env.go](file:///d:/project/kitwork/engine/work/env.go) | `env_test.go` | **Ready** | No | No |
| **Inline Error Handling** | `query.safe()` / `SafeResult` | [engine/value/result.go](file:///d:/project/kitwork/engine/value/result.go) | `result_vm_test.go`, `safe_rescue_test.go` | **Ready** | No | No |
| **Health Check & Telemetry** | `ctx.json({ status: "ok" })` | [engine/work/context.go](file:///d:/project/kitwork/engine/work/context.go) | `bench_handler_test.go` | **Ready** | No | No |

---

## 2. Detailed Capability Verification Findings

1. **Routing & Views**:
   - `router.kitwork.js` and `page.kitwork.html` tree routing in [engine/work/router_tree.go](file:///d:/project/kitwork/engine/work/router_tree.go) is 100% feature-complete for handling static endpoints (`/health`), dynamic shortcode paths (`/[code]`), and REST sub-routers (`/api/v1/links`).
2. **Database Engine**:
   - `ctx.db.table("links")` in [engine/work/db.sqlite.go](file:///d:/project/kitwork/engine/work/db.sqlite.go) enforces parameterized queries and mandatory `.where()` guards on `.update()` and `.delete()`.
3. **Inline Error Boundary**:
   - `fail().safe()` and hard application failures are recoverable as `SafeResult`.
   - Energy, cancellation, stack, program and native-panic diagnostics remain fatal by design.
