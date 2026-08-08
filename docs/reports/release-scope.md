# Kitwork Engine — Release Scope Matrix (v1.0.0-RC1)

This document establishes the official component scope classification for Kitwork Engine's first public release.

---

## 1. Classification Summary Table

| Package / Feature | Status Classification | Public / Internal | Implementation Source | Test Coverage Source | Releasable in RC1? |
|---|---|---|---|---|---|
| **Lexer & Parser** | **Stable** | Internal | [engine/compiler/lexer.go](file:///d:/project/kitwork/engine/compiler/lexer.go), [parser.go](file:///d:/project/kitwork/engine/compiler/parser.go) | `compiler_fuzz_test.go`, `diagnostic_test.go` | **Yes** |
| **Native Import Bundler** | **Stable** | Internal | [engine/compiler/bundler.go](file:///d:/project/kitwork/engine/compiler/bundler.go) | `native_bundle_test.go` | **Yes** |
| **Bytecode Verifier** | **Stable** | Internal | [engine/runtime/verify.go](file:///d:/project/kitwork/engine/runtime/verify.go) | `verify_test.go`, `contract_test.go` | **Yes** |
| **VM Interpreter Loop** | **Stable** | Internal | [engine/runtime/interpreter.go](file:///d:/project/kitwork/engine/runtime/interpreter.go) | `vm_test.go`, `determinism_test.go` | **Yes** |
| **Value System (24-byte)** | **Stable** | Public (Go API) | [engine/value/value.go](file:///d:/project/kitwork/engine/value/value.go), [kind.go](file:///d:/project/kitwork/engine/value/kind.go) | `value_test.go`, `result_test.go` | **Yes** |
| **Filesystem Router Tree** | **Stable** | Public (JS API) | [engine/work/router_tree.go](file:///d:/project/kitwork/engine/work/router_tree.go), [router_serve.go](file:///d:/project/kitwork/engine/work/router_serve.go) | `router_tree_test.go`, `bench_handler_test.go` | **Yes** |
| **HTML Render Plan Engine** | **Stable** | Internal | [engine/render/render.go](file:///d:/project/kitwork/engine/render/render.go), [snapshot.go](file:///d:/project/kitwork/engine/render/snapshot.go) | `render_test.go`, `snapshot_test.go` | **Yes** |
| **`env` Configuration Proxy** | **Stable** | Public (JS API) | [engine/work/env.go](file:///d:/project/kitwork/engine/work/env.go), [engine/configjs.go](file:///d:/project/kitwork/engine/configjs.go) | `env_test.go`, `configjs_test.go` | **Yes** |
| **SQLite Entity DB Builder** | **Stable** | Public (JS API) | [engine/work/db.sqlite.go](file:///d:/project/kitwork/engine/work/db.sqlite.go), `entity_scope.go` | `db_sqlite_test.go`, `entity_scope_test.go` | **Yes** |
| **JIT CSS / Icons Engine** | **Beta** | Internal | [engine/jit/css/jit.go](file:///d:/project/kitwork/engine/jit/css/jit.go), [engine/jit/icons/icons.go](file:///d:/project/kitwork/engine/jit/icons/icons.go) | `router_jitcss_test.go` | **Yes (Default ON)** |
| **Bytecode File Cache** | **Beta** | Public Config | [engine/compiler/cache.go](file:///d:/project/kitwork/engine/compiler/cache.go), [artifact.go](file:///d:/project/kitwork/engine/compiler/artifact.go) | `cache_test.go`, `artifact_test.go` | **Yes (Opt-in)** |
| **Cron Scheduler & Queue** | **Beta** | Public (JS API) | [engine/work/cron.go](file:///d:/project/kitwork/engine/work/cron.go), [queue.go](file:///d:/project/kitwork/engine/work/queue.go) | `cron_persist_test.go`, `queue_test.go` | **Yes** |
| **SSE Broker** | **Beta** | Public (JS API) | [engine/work/sse.go](file:///d:/project/kitwork/engine/work/sse.go) | `sse_test.go`, `sse_lifecycle_test.go` | **Yes** |
| **Kitwork Drive / Hydrate** | **Experimental** | Public (JS API) | [engine/jit/hydrate/render.go](file:///d:/project/kitwork/engine/jit/hydrate/render.go) | `jithydrate_test.go` | **Opt-in Experimental** |
| **Logic Capsules (Tier ③)** | **Incomplete** | Concept / RFC | [engine/docs/ARCHITECTURE.md](file:///d:/project/kitwork/engine/docs/ARCHITECTURE.md) | None | **No (Excluded from RC1)** |
| **Multi-Node Cluster** | **Incomplete** | Concept / RFC | [engine/CLUSTER.MD](file:///d:/project/kitwork/engine/CLUSTER.MD) | None | **No (Excluded from RC1)** |
| **Legacy `.result()` Method** | **Deprecated** | Public | [engine/value/result_test.go](file:///d:/project/kitwork/engine/value/result_test.go#L120) | `result_test.go` | **No (Removed)** |

---

## 2. Detailed Category Breakdown

### 2.1 Stable Components (Publicly Supported in RC1)
- **Engine Core Host (`core.Engine`)**: Manages process listeners, tenant discovery, hot reload orchestration, rate limiting, and VM pool leasing.
- **Go VM & Interpreter (`runtime.VM`)**: Hand-written 24-byte tagged union VM running pre-verified bytecode instructions.
- **Filesystem Route Tree (`RouteTree`)**: Automatic folder-based routing (`router.kitwork.js` and `page.kitwork.html`).
- **Database Engine (`ctx.db.table()`)**: Parameterized query builder with identity scoping and mandatory `where()` mutation guards.
- **HTML Render Engine**: Template layout engine (`_layout_.kitwork.html`) supporting template inheritance and slot binding without hot-path disk reads.

### 2.2 Beta Components (Feature-Complete, Minor Edge Cases under Observation)
- **Bytecode Disk Cache (`compiler.FileCache`)**: Local compiled program storage with SHA-256 source fingerprint verification.
- **Cron Scheduler & Queue Workers (`work.CronScheduler`, `work.QueueWorker`)**: App-owned recurring job engine with SQLite/Postgres persistence backends.
- **JIT CSS & Icon Masking Engine**: On-demand utility CSS and Tabler icon SVG mask generator.

### 2.3 Experimental Components (Subject to Change)
- **Kit JS Hydration & SPA Morphing**: Client-side reactive binding directives (`data-kit-text`, `data-kit-show`).
- **SSE Broker Stream Replay**: Server-Sent Events broker with reconnect buffer history.

### 2.4 Incomplete / Excluded from RC1
- **Tier ③ Logic Capsules**: Untrusted client-sent logic execution under signed identity grants (RFC phase).
- **Multi-Node Cluster Migration**: Cross-server tenant migration and state replication.

---

## 3. Public vs. Internal API Boundaries

```text
[ PUBLIC API SURFACE (Frozen for SemVer v1.x) ]
  ├── JS Import: import { router, route, database, env, kitwork } from "kitwork"
  ├── Context: ctx.request, ctx.response, ctx.db, ctx.view(), ctx.json(), ctx.status()
  ├── View Hooks: {{ binding }}, _layout_.kitwork.html, page.kitwork.html
  └── Host Config: app.web({ bytecodeCache, port, ALLOW_LOCAL })

[ INTERNAL API SURFACE (Private to engine/) ]
  ├── VM Opcodes: runtime.Opcode, runtime.Verify, interpreter.execute
  ├── Compiler: compiler.Parse, compiler.CompileAST, compiler.nativeBundle
  └── Storage Envelope: runtime.ProgramEncodingVersion, Program.MarshalBinary
```
