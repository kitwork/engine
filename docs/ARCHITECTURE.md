# Kitwork — Application Architecture (RFC, draft)

> The three-tier model for applications running on the Kitwork runtime.
> Status: **draft for review.** Nothing here is implemented yet — it captures the
> intended convention so we can react to it before changing the engine.

---

## 0. TL;DR

- The **engine** is a multi-tenant host. A **site** is a folder. The engine resolves
  `Host → tenant/domain folder` and runs that folder as an isolated capsule.
- Each request flows through **three tiers**:

  | Tier | Where | Trust | Job |
  |---|---|---|---|
  | ① **SSR shell** | server | trusted (your code) | render the page + SEO data |
  | ② **Kit JS hydrate** | client | your code | make the rendered DOM interactive |
  | ③ **Logic capsule** | client → server | **untrusted, sandboxed** | live, user-driven data |

- **One rule that keeps the model clean:**
  > Data that needs **SEO / first paint** → rendered by tier ① (SSR).
  > Data that is **user-driven and live** (filter, paginate, mutate) → tier ③ (capsule).

- HTML and JS are **never mixed in one file.** Colocation happens at the *folder* level,
  not the *file* level.

---

## 1. Two levels: engine vs site

The runtime is **not** "one app that manages all routes." It is a host that loads many
isolated site-folders. Keep these levels separate:

| Engine (the binary, shared process) | Site (one tenant/domain = one folder) |
|---|---|
| resolve `Host →` tenant/domain folder (`host/`) | thin composition root + `app/` route tree |
| isolate each tenant in its own VM context (`runtime/`, `value/`) | its own DB / config / secrets |
| lazy-load + cache compiled bytecode per tenant (`compiler/`, `opcode/`, `jit/`) | hot-reload independently |
| gas / energy accounting + rate limits per tenant (`energy/`) | its own routes / static / tasks |
| capsule auth: signature + capability + identity (`security/`, `id/`, `token/`) | — |
| cluster: tenant is the unit of placement / migration | — |

"Global" inside a site = **per-tenant** (its own `database.connect()`), never process-global.
Process-global belongs to the engine only.

---

## 2. Site convention — tiers ① and ②

### 2.1 Folder layout (worked example: `kitwork.io`)

```
kitwork.io/                          ← one tenant (engine maps Host → here)
  config.kitwork.yml                 ← declarative config (domain, db, cache)
  app.kitwork.js                     ← OPTIONAL: route map + middleware + lifecycle

  app/                               ← ❶ ROUTE TREE — folder = route
    _layout_.kitwork.html            ← root layout (pulls in head/navbar/footer)
    page.kitwork.html                ← "/"
    about/        page.kitwork.html
    founder/      page.kitwork.html
    docs/
      _layout_.kitwork.html          ← nested layout (sidebar/toolbar)
      _sidebar_.kitwork.html         ← feature-local partial
      page.kitwork.html              ← /docs
      routing/    page.kitwork.html  ← /docs/routing
    blog/
      page.kitwork.html              ← /blog
      index.kitwork.js               ← loader (list)
      [slug]/
        page.kitwork.html            ← /blog/:slug
        index.kitwork.js             ← loader (find by slug)
    users/
      index.kitwork.js               ← server route (tier ①)
      page.kitwork.html              ← view
      client.kitwork.js              ← capsule client logic (tier ③)
    api/
      gold/index.kitwork.js          ← JSON API (no page.html)

  components/                        ← shared partials (NOT routes)
    _head_  _navbar_  _footer_
  lib/                               ← shared code (imported, not routed)
  public/                            ← static assets, served directly + cached
  tasks/                             ← cron / workers (the work/ pillar) — non-HTTP
```

### 2.2 File-type legend

| File | Role | Is a route? |
|---|---|---|
| `page.kitwork.html` | view of the route — **template only** | ✅ (by folder) |
| `index.kitwork.js` | server handler / loader — **logic only** | ✅ |
| `client.kitwork.js` | client hydration + capsules — **logic only** | ❌ (shipped to browser) |
| `[param]/` | dynamic segment | ✅ |
| `_layout_.kitwork.html` | layout, **nested down the tree** | ❌ |
| `_guard_.kitwork.js` | runs before handlers, inherited by children | ❌ |
| `_*`, `components/`, `lib/`, `public/`, `tasks/` | partials / shared / static | ❌ — **source is never served** |

**Security invariant:** only `public/` is served statically. Handler/capsule source
(`*.kitwork.js`, `_*`) is **never** exposed — logic stays on the server.

### 2.3 Route module — `index.kitwork.js` (logic only, zero HTML)

```js
import { route } from "kitwork"

export default route("/users")
  .cache("5s")
  .get((ctx) => ctx.view({ users: ctx.db.table("user").list(10) }))   // renders ./page.kitwork.html
  .post((ctx) => ctx.json(ctx.db.table("user").create(ctx.body)))
```

- The module declares its own path (`route("/users")`); the composition root just `use()`s it.
- `ctx.view(binding)` renders the **sibling** `page.kitwork.html`.
- `ctx.db` is injected by middleware (DI-lite), not imported globally.
- A folder with only `page.kitwork.html` (no `index.js`) renders directly with site defaults.

### 2.4 View — `page.kitwork.html` (template only, zero JS logic)

```html
<section class="kw-section">
  <h1 class="kw-heading">Users</h1>
  <ul data-kit="users-list">
    {{ users | each: <li data-kit-row>{{ .name }}</li> }}
  </ul>
  <input data-kit-filter placeholder="filter…">
</section>
```

Only `{{ binding }}` templating + `data-*` hooks for Kit JS. No inline `<script>`.

### 2.5 Client + capsule — `client.kitwork.js` (logic only)

```js
import { kit, db } from "kitwork/client"

kit.hydrate("users-list", (el) => {
  kit.on("[data-kit-filter]", "input", async (e) => {
    // looks like server code, but is captured as a CAPSULE and shipped to the server:
    const users = await kit.run(() => db.table("user").where(u => u.active).list(20))
    kit.render(el, users)          // Directive Sync — updates in place, no reload
  })
})
```

### 2.6 Composition root — `app.kitwork.js` (OPTIONAL, map + wiring, no routes inline)

```js
import { router, database } from "kitwork"
import home  from "./app/index.kitwork.js"
import users from "./app/users/index.kitwork.js"
import gold  from "./app/api/gold/index.kitwork.js"

const db = database.connect()
router.context({ db })                 // inject ctx.db for all modules

router.use(home)                       // explicit map — one glance = whole site
router.use(users)
router.use(gold)
```

- **Explicit `use()` is the default** (visibility + control, no magic).
- A site that registers nothing falls back to **auto-mount** by folder (zero-config tenants).
- `kitwork routes` prints the discovered route tree — visibility without a hand-kept list.

### 2.7 `config.kitwork.yml` (declarative; no `port` — that is the engine's)

```yaml
domain: ["kitwork.io", "www.kitwork.io"]
hot_reload: true
database: { type: postgres, name: kitwork_io }
cache: { default: "5s" }
```

---

## 3. Capsule model — tier ③ (the differentiator)

The client expresses **intent as logic**, not as a request. That logic is captured,
compiled to bytecode, signed, and sent to one generic endpoint; the server runs it
**within the authenticated identity's granted permissions**, under a gas budget.

```
client:  kit.run(() => db.table("user").where(u => u.active).list(20))
            → serialize to bytecode  → POST /kitwork/run
server:  identity?  → grant = { user: [read] }
         static-analyze capsule → needs { user: read }      (← enabled by the JS subset)
         needs ⊆ grant ?  → apply gas  → execute in sandbox  → JSON
```

### 3.1 Why this is safe — the language *is* the security model

Executing client-supplied logic is the most dangerous thing you can build. It is only
safe because of Kitwork's existing constraints:

- **No `while` / no `try/catch`** → every capsule is **statically analyzable**: the server
  knows exactly which tables/operations it touches *before* running it. Capability is
  **inferred, not declared** (no manual `.can()` needed) and intersected with the grant.
- **Gas** (`energy/`) → no infinite or runaway-expensive capsule (DoS-proof).
- **db-only sandbox** → a capsule can only speak through `db.table()`; no fs, no net, no
  arbitrary code.
- **Identity grant** (`id/`, `token/`, `security/`) → per-identity table caps:
  ```
  guest → { user: [read], post: [read] }
  admin → { user: [read, write, delete], … }
  ```

### 3.2 Architectural consequence

If capsules carry the **data layer**, a site needs far fewer hand-written routes: mostly
**pages + one capsule runner**. Authorization is centralized per **identity**, not scattered
across endpoints.

---

## 4. The sacred rule (and SEO)

Same `db.table()` API in tiers ① and ③ — only the **trust context** differs (server-full vs
identity-scoped). The boundary that must never blur:

> **Indexable content → tier ① (SSR).** Bots only see what SSR renders.
> Capsule data is post-load and **not indexed.** The founder optimizes for SEO; this line
> is non-negotiable.

---

## 5. Phased roadmap (ship value at every step — none is a multi-year foundation)

- **Phase 0 — today.** Central `app.kitwork.js`, `render.directory("views")`, catch-all. Works.
- **Phase 1 — site convention.** Folder = route; `page.html` + `index.js`; explicit `router.use()`;
  `public/ lib/ components/ tasks/`. Pure refactor of one tenant; engine adds folder mount + `ctx`.
  *Validate on a copy of `kitwork.io` before committing.*
- **Phase 2 — capsules, read-only.** `kit.run()` + `/kitwork/run` + identity grant (read), gas,
  pre-eval. Lowest-risk slice of the moat.
- **Phase 3 — capsules, writes.** Add write/delete grants + audit. Tighten sandbox.
- **Phase 4 — cluster.** Tenant placement / migration across nodes.

Keep the convention surface **ruthlessly small** — resist adding `(group)`, complex precedence,
etc. until a real need appears. Kitwork's edge is minimalism; this is a one-person project, not Next.js.

---

## 6. Open decisions

1. Directory names: **`app/`** (signals route modules) vs keep **`views/`** · **`public/`** vs **`assets/`**.
2. Handler filename: **`index.kitwork.js`** vs **`route.kitwork.js`**.
3. Capsule location: companion **`client.kitwork.js`** (recommended — keeps HTML pure) — confirmed.
4. Explicit `router.use()` as default, auto-mount as opt-in — confirmed.

## 7. Non-goals

- No NestJS-style decorators / DI containers / module manifests.
- No HTML+JS in a single file.
- Not a re-implementation of Next.js — borrow file-system routing + nested layouts/guards,
  skip the rest.
