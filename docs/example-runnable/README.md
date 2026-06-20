# Runnable modular tenant (works TODAY)

A real, working tenant assembled from modules via `import`/`export` — using the
**current** engine API (`router.get().handle()`), not the future RFC convention.

```
test/localhost/
  app.kitwork.js            ← entry (thin): imports the route module
  routes/hello.kitwork.js   ← imports router (kitwork) + greet (sibling lib), registers GET /hello
  lib/greet.kitwork.js      ← exports the helper
```

**Verified** end to end by `work/modular_tenant_test.go`: `Run()` compiles + registers,
then a real HTTP `GET /hello` returns `Hello, world!` — proving file-to-file
import/export works through the same path the live server uses.

How imports resolve here — **all native, no esbuild**:
- `import router from "kitwork/router"` → lowered to `kitwork().router`.
- `import { greet } from "../lib/greet.kitwork.js"` and `import "./routes/…"` → the native
  bundler resolves each relative module and IIFE-wraps it
  (`const __kw_mod_N = (() => { …; return { greet }; })();`), then rewrites the import to a
  binding. `as` aliases lower to member bindings too. esbuild has been removed entirely —
  the hand-written parser is the single source of truth for the Kitwork subset.

> This is distinct from `../example/` — that one sketches the **future** RFC convention
> (`route().get()`, `ctx.view`, folder auto-mount) which is **not implemented yet**. The
> files here run on the engine as it exists now.
