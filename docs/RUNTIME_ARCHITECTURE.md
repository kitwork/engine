# Kitwork Runtime Architecture

This document is the source of truth for the production ownership model.
`ARCHITECTURE.md` remains the historical three-tier and logic-capsule RFC.

## Ownership hierarchy

```text
Host / Engine
  AppRegistry
    AppRuntime(identity)
      SiteRuntime(domain)
        Generation(version)
          RequestScope
            VM lease
```

- The host owns listeners, TLS, process signals, the VM pool, and the app registry.
- One `AppRuntime` exists per identity and owns identity-wide infrastructure.
- One `SiteRuntime` exists per domain, publishes monotonic generations, and
  owns persistent cache, rate-limit, and SSE state across reloads.
- One active `Generation` owns its executable route graph, prepared render
  plan, immutable template/environment/presentation/source snapshots, RAM
  response/fetch cache, and `LifetimeSite` capabilities.
- One request scope exists per HTTP request and owns request/response state.
- A VM is leased for an execution. Apps and sites do not own fixed VMs.

## Filesystem

```text
apps/<identity>/
  _cron/
  _core/
  .data/
  <domain>/
    router.kitwork.js
    page.kitwork.html
```

The identity folder is the app boundary. A domain folder is the site boundary.

## Current migration status

Implemented:

- `core.Engine` owns one `app.Runtime` per identity;
- domains under that identity receive distinct `site.Runtime` children;
- the identity scheduler and its sites share the same app runtime;
- configured database connections are opened exactly once per app runtime;
- site-local SQLite connections are keyed by canonical path but owned and
  closed by the parent app runtime;
- detached work is accepted, cancelled, and drained by the app runtime, so
  site eviction and generation reload do not stop app work;
- the cron scheduler is an app-owned lifecycle resource; compatibility
  tenants only provide its bytecode execution adapter;
- `LifetimeApp` capability instances live on that app runtime and are shared
  across sibling domains;
- every HTTP request owns a `request.Scope`, cancellation context,
  `LifetimeRequest` capability cache, primary VM lease, and tracked child VM
  leases;
- trusted host middleware may attach an immutable authenticated principal and
  permission set to that request scope;
- `core.Engine.SetAuthorizer` is the production seam that resolves those
  trusted inputs after app/site routing and before request-scope creation;
- capability registrations may require permissions, checked against the
  request without passing request state into app-scoped factories;
- every loaded Tenant facade is paired with one monotonic `site.Generation`;
  requests pin that generation until their scope closes;
- every route folder is discovered and compiled before activation; JIT, theme,
  favicon, and asset declarations are frozen as one presentation snapshot;
- the complete executable route graph and its compiled folder programs belong
  to the generation; request resolution only reads published nodes and never
  discovers or recompiles a route;
- every HTML template is copied into an immutable generation snapshot; default
  and notfound render trees are assembled and parsed before activation;
- server-template paths, literals, operators, and conditions are compiled into
  immutable expression trees with that render plan. Requests walk those trees
  through lexical scope frames and write into one output builder; they do not
  split, parse, or copy the complete scope for each expression or loop item;
- source-driven JIT CSS, material, icon, logo, client-runtime, font, and theme
  presentation is prepared with static render trees; parser-aware template
  minification prepares their inline assets once;
- a KitJS-enabled generation prepares one exact closed Hydrate delivery for
  every distinct document component graph. Each delivery freezes the only
  valid classic-`defer` order: runtime, Hydrate, graph opener, dependency-ordered
  services, an optional common-components bundle, then individual components;
- staged KitJS scripts are immutable site content assets addressed as
  `/jit/<sha256>.<suffix>.js`. The generation owns their exact ordered
  role/hash/URL/SRI references, while the site content store retains identical
  bytes across generation hand-off for cache continuity;
- at least two exact component name/version pairs common to every prepared
  document form one stable `components` chunk. Those packages are excluded
  from all individual component chunks; route-only components remain separately
  cacheable. A different incoming delivery graph forces Drive to normal browser
  navigation before Morph, while unchanged chunks reuse immutable browser and
  site caches;
- staged injection preserves effective charset and meta CSP declarations before
  the script sequence and places that sequence before any active or potential
  base URL. Unsafe or dynamic head order fails generation preparation. Drive
  additionally requires exact ordered effective meta CSP equality; either CSP
  response header (`Content-Security-Policy` or its report-only form) forces
  normal navigation because a fetched policy cannot be installed by Morph;
- templates with data-driven presentation attributes retain the complete
  request pipeline. Other requests only bind data and hydrate without reading
  templates or reparsing the already-minified document and inline assets;
- authored Hydrate expression attributes are decoded from source HTML to the
  browser-equivalent DOM value before verification, JIT class extraction, and
  server pre-render. Generation minification keeps quotes until those passes
  complete; request data remains opaque afterward;
- every generation owns a frozen executable-source manifest covering routers,
  native imports, templates, `.env`, absent router markers, and
  route-directory structure;
- hot reload checks that manifest and replaces the complete generation for
  root, subfolder, imported-module, environment, and route-graph changes;
- `.env` is loaded once as an immutable generation snapshot; a changed file
  prepares and publishes a complete replacement;
- RAM response and fetch entries belong to one generation and are terminally
  cleared after that generation drains;
- disk-persisted responses and rate-limit budgets belong to `SiteRuntime` and
  survive generation replacement;
- the SSE broker and replay history belong to `SiteRuntime`; reload preserves
  them, while site shutdown stops streams before waiting for generation drain;
- a generation that fails compilation or initialization is discarded while
  the previous generation continues serving;
- `LifetimeSite` capability instances live on the generation and close only
  after its accepted requests drain;
- reload prepares and initializes a generation before atomically activating it;
  stale generations cannot be reactivated and retire only after their request
  leases drain;
- SSE releases its VM before entering the long-lived stream while retaining
  the request scope until the stream ends;
- hot reload never recompiles an active route node in place; it replaces the
  `work.Tenant` execution facade while preserving both runtimes;
- site eviction closes only that site; engine shutdown closes the full hierarchy.
- `kitwork check` evaluates the executable host manifest, discovers every app
  and site, prepares route graphs and render plans through the production
  pipeline, compiles cron sources, reports all failures, and exits without
  opening listeners, activating generations, or starting schedulers.
- `core.Engine.Health()` exposes only bounded process-local aggregates:
  requests and in-flight high-water marks, fixed latency buckets, VM totals,
  prepared/fallback render counts, response-cache outcomes, generation
  prepare/activate/drain outcomes, and current ownership counts. It records no
  URL, tenant, argument, or user labels; observed Program identities are capped
  and copied as checksums rather than retained as Program pointers.

## Runtime responsibilities

### App

- identity;
- app-wide lifecycle;
- cron and background work;
- shared database and lifecycle resource managers;
- app-scoped capabilities;
- child site registry.

### Site

- domain;
- generation publication and retirement;
- persistent response store;
- bounded immutable content-addressed assets retained across generation hand-off;
- immutable staged KitJS bytes keyed by exact SHA-256 plus validated role/suffix;
- rate-limit budgets;
- SSE connections and replay history;

### Generation

- per-generation filesystem route tree;
- compiled folder programs, handlers, guards, and metadata;
- immutable HTML template snapshot and prepared render plan;
- frozen rendering, asset selection/references, and JIT configuration;
- per-document staged KitJS graph selection and ordered SRI references;
- immutable environment and executable-source manifest;
- RAM response and fetch caches;
- generation-scoped capabilities.

### Request

- request and response;
- cancellation;
- authenticated identity and permissions;
- request-scoped capabilities;
- VM lease.

## Migration rule

`work.Tenant` remains a compatibility facade while ownership moves behind
`app.Runtime`, `site.Runtime`, and the future request scope. Public
`.kitwork.js` APIs must not change during this migration.

The migration is complete only when:

1. domains under one identity share exactly one app runtime;
2. each domain has exactly one isolated site runtime;
3. hot reload replaces execution state without restarting the app;
4. site eviction does not stop sibling sites;
5. app shutdown drains every site, request, job, and capability;
6. pooled VMs retain no app, site, or request references.

All six conditions are now covered by the production lifecycle and regression
tests. Filesystem boundaries and static render presentation are prepared with
the generation. Bounded production observability, allocation budgets, and
generation publication/drain soak tests now guard that path. Prepared
template-expression evaluation has its own deterministic allocation gate. The
next architecture work is continued lifecycle hardening driven by bounded
runtime signals and production profiles, without adding another runtime layer.
Logic capsules are a parked experimental RFC; they are not the next layer and
must not create a parallel runtime or influence current public APIs.
