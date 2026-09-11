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

- The host owns listeners, TLS, process signals, the VM pool, the app registry,
  the bounded process-wide full-text search manager, and the bounded KitDB node
  handle/page-cache/maintenance governor.
- The host may capture detached diagnostics and an explicitly requested private
  heap profile. Diagnostics observe owners through bounded snapshots; they do
  not become a parallel owner or an automatic HTTP surface.
- One `AppRuntime` exists per identity and owns identity-wide infrastructure.
- One `SiteRuntime` exists per domain, publishes monotonic generations, and
  owns persistent cache, rate-limit, and SSE state across reloads.
- One active `Generation` owns its executable route graph, prepared render
  plan, immutable template/environment/presentation/source snapshots, RAM
  response/fetch cache, and `LifetimeSite` capabilities.
- One request scope exists per HTTP request and owns request/response state.
- A VM is leased for an execution. Apps and sites do not own fixed VMs.

## Filesystem

One app with routes directly at its root:

```text
app/
  _cron/
  _queue/
  _core/
  .data/
  router.kitwork.js
  page.kitwork.html
```

One app with multiple domain-scoped sites:

```text
app/
  _cron/
  _queue/
  _core/
  .data/
  <domain>/
    router.kitwork.js
    page.kitwork.html
```

Multiple tenant apps:

```text
apps/<identity>/
  _cron/
  _queue/
  _core/
  .data/
  <domain>/
    router.kitwork.js
    page.kitwork.html
```

`app/` is always one app boundary. Its root router selects the direct layout;
otherwise direct children with root routers are domain boundaries and share one
AppRuntime. `apps/` always reserves its first directory level for tenant
identities and its second for domains. The presence of a system database does
not change either filesystem contract.

When `.root(...)` is omitted, host bootstrap inspects `app/` and `apps/`. It
selects the only directory present, and rejects the configuration as ambiguous
when both exist. Explicit `.root("app")` or `.root("apps")` resolves that
ambiguity. A multi-tenant request prefers the system domain registry when it is
connected and can resolve an unregistered domain from one unique
`apps/<identity>/<domain>` source during local or database-free operation.

## Request host resolution

The host normalizes and validates the HTTP Host authority before tenant lookup.
The browser-visible authority and the canonical site domain are deliberately
separate: routing, the site-runtime registry, and tenant caches use the
canonical domain without rewriting `request.Host`.

With local development enabled:

- `localhost`, IPv4 loopback, and IPv6 loopback resolve to the configured
  primary hostname when one is present;
- `<domain>.localhost` resolves to `<domain>`, so
  `kitwork.io.localhost:8080` runs the same `kitwork.io` SiteRuntime;
- an existing exact `*.localhost` site source has precedence for compatibility;
- a single label such as `kitwork.localhost` remains literal until an explicit
  username/alias registry owns that mapping.

Production does not decode the `.localhost` suffix. Malformed authorities are
rejected before redirect or tenant resolution, and aliases cannot create a
second runtime or cache owner for one canonical domain. A local authority also
skips production canonical/domain redirects, so local development cannot be
redirected accidentally to the public site.

## Current migration status

Implemented:

- `core.Engine` owns one `app.Runtime` per identity;
- domains under that identity receive distinct `site.Runtime` children;
- the identity scheduler and its sites share the same app runtime;
- configured database connections are opened exactly once per app runtime;
- site-local SQLite connections are keyed by canonical path but owned and
  closed by the parent app runtime;
- `core.Engine` owns one `kitdb/node.Manager` across every identity. AppRuntime
  adapters hold per-file relational validation gates and operation leases, but
  idle physical KitDB handles remain under the host LRU and global reservation
  budget until pressure, explicit trim, or engine shutdown closes them;
- KitDB checkpoint, verification, verified-backup, retained-history-prune, and
  replica-catch-up maintenance is admitted by that same host owner. Jobs are
  globally and per-database bounded, exact duplicate operations coalesce, and
  every worker borrows normal node leases. A replica catch-up reserves both
  source and target; overlapping jobs cannot run on either path, handle order is
  canonical, and only one multi-database job runs at once to avoid partial
  acquisition deadlock. Verified backup is the managed replica bootstrap and
  safely resumes an immutable destination before sealing and pinning source
  history. Catch-up moves bounded protocol-v1 batches: source read, idempotent
  target apply through the ordinary WAL, then exact cursor acknowledgement.
  Pure-Go wire v1 serializes those canonical WAL frames, and an explicit
  filesystem mailbox publishes synced batches and ACKs without overwrite. The
  same owner now schedules separate publish, apply, and acknowledge jobs,
  reserves the mailbox beside the relevant source or target database, keeps one
  managed batch in flight, and resumes solely from mailbox artifacts, the
  target WAL cursor, and the source pin after manager restart. Mailboxes and
  backup destinations are reservation-only paths: conflicts serialize without
  opening them as databases or charging page cache. This adds no cursor sidecar
  or second durability path and remains host-only. For an explicitly registered
  local topology, one shared node dispatcher and a fixed worker pool may repeat
  those exact jobs with bounded cadence, retry backoff, and health snapshots.
  Link configuration is process-local; idle links retain no lease or per-link
  goroutine, and restart progress still comes only from protocol-owned durable
  state. The mailbox primitive itself has no watcher, and authenticated network
  transport is not implemented. Prune preserves exact transaction boundaries;
  catch-up delegates
  identity, checksum, ordering, and target-WAL durability to the kernel. These
  topology/destructive APIs are host-trusted. Request commits remain durable
  through WAL even when maintenance is queued, rejected, canceled, or failing;
- The same node owner may run explicitly registered local production policies.
  They use one bounded dispatcher and fixed worker pool across databases, select
  the earlier backup or restore deadline, and call the existing verified-backup
  path rather than adding a second writer. Restart truth comes from immutable
  verified anchors plus the source history pin; restore drills compare the
  canonical logical digest in a disposable destination. A policy may also name
  one host-owned publisher; readiness then requires destination read-back
  evidence matching the exact current anchor. The built-in bounded directory
  adapter is suitable for a separately provisioned volume or transfer spool,
  while native object-store transport and proof of physical independence remain
  deployment concerns. Configuration remains process-local, evidence and health
  are path-free, idle policies own no lease or goroutine, and tenant VMs receive
  no filesystem, publisher, or policy authority;
- Kitwork registers stateless row-migration and secondary-index drivers with
  the same node owner. Each dispatch leases one canonical file, takes its
  shared relational gate, reloads checksummed `KRMS` or `KIBS`, commits at most
  one bounded chunk, and yields to weighted maintenance scheduling. Driver
  registration and wake hints are process-local; catalog and WAL state remain
  the only restart authority. Secondary-index foreground admission commits
  only metadata and opens no row cursor; all row/cutover/cleanup progress is
  driver-owned;
- KitDB node limits may be replaced only during host boot. Standalone tenants
  outside Engine receive an app-owned bounded fallback rather than a global;
- `core.Engine` owns one bounded `search.Manager` across every identity and
  domain. Tenant facades borrow it, generation replacement does not close it,
  and engine shutdown closes it only after tenant and app-runtime drain;
- search manager limits may be replaced only during host boot, before any app
  or site loads; runtime stats remain bounded and contain no tenant/query labels;
- search index identities and rebuild gates survive generation hand-off; proven
  site idle/removal closes those indexes, while hot reload leaves them open;
  a same-site request arriving at that boundary waits for the old owner to
  drain, then reopens the durable generation instead of using a closing owner;
- schema database search keeps SQLite as source of truth, tracks table
  freshness through same-transaction revision triggers, and streams stale
  projections into immutable search generations without retaining all rows;
- the opt-in `collection.search()` canary keeps the legacy SQLite result on the
  request path and sends only a bounded Top-K comparison to one non-blocking,
  generation-owned worker. That worker borrows the host search manager, streams
  replacement documents, revalidates source freshness, and is cancelled and
  drained with its generation. A shared 256-task admission budget bounds
  retained shadow work across all loaded generations. Its fixed-cardinality host counters survive
  generation replacement and contain no tenant, collection, document, or query
  labels;
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
- KitJS candidate preparation owns one bounded, generation-local package
  materializer. It normalizes each exact component/service source once; each
  individual package artifact and the single optional common bundle are
  materialized and content-addressed once, then exact-compared and reused by
  later graphs without rehashing. Prepared HTML lookup uses canonical
  identities only. The cache remains candidate-local and
  is discarded; only complete artifacts are retained atomically after every
  validation and capacity check passes;
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
  them, while site shutdown synchronously stops and drains the broker;
- a generation that fails compilation or initialization is discarded while
  the previous generation continues serving;
- `LifetimeSite` capability instances live on the generation and close only
  after its accepted requests drain;
- reload prepares and initializes a generation before atomically activating it;
  stale generations cannot be reactivated and retire only after their request
  leases drain;
- SSE first transfers its client to the site broker, then closes its request
  scope, releases all VM and generation leases, and leaves only the native HTTP
  goroutine under site ownership for the long-lived stream;
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
  prepare/activate/drain-attempt outcomes, in-progress lifecycle gauges, and
  current ownership counts. Preparation succeeds only after the generation,
  bytecode cache, tenant facade, route graph, and render plan have all completed
  `Tenant.Run`; activation remains a separate publication outcome. A generation
  under an observed drain attempt contributes its aggregate lease count plus
  scalar current and high-water oldest-drain ages even after it leaves the
  current tenant cache. Ownership uses a nonblocking engine read lock:
  `ownership_snapshot_available=false` means its top-level counts are zero and
  unavailable while lifecycle and process-global VM gauges remain live. When
  the lock is available, lifecycle gauges are captured only after acquiring it,
  so activation cannot finish between lifecycle and ownership capture. Health
  records no URL, tenant, argument, or user labels; observed Program identities
  are capped and copied as checksums rather than retained as Program pointers.

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
generation publication/drain soak tests now guard that path. A production-path
memory campaign also verifies that closure-heavy requests, native HTTP, pooled
VMs, Programs, and retired generations reach a post-warm-up heap plateau.
Prepared template-expression evaluation has its own deterministic allocation
gate. The next architecture work is continued lifecycle hardening driven by
bounded runtime signals and production profiles, without adding another
runtime layer. Logic capsules are a parked experimental RFC; they are not the
next layer and must not create a parallel runtime or influence current public
APIs.
