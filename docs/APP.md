# Kitwork App Manifest

`app.kitwork.js` is the executable host manifest. It describes what an
application is, which surfaces it exposes, and which resources the host owns.
Declaring a surface records configuration during setup; tenant code is not run
and no listener is opened until manifest evaluation and validation succeed.

```javascript
import { app, env } from "kitwork"

app
  .title("Example")
  .web({
    port: env.PORT || 8080,
    allowLocal: env.ALLOW_LOCAL || false,
  })
```

`server.kitwork.js` and the `server` binding remain compatibility paths. New
applications should use `app.kitwork.js` and `app`.

## Surfaces

| Surface | Responsibility | Host runtime |
| --- | --- | --- |
| `app.web(options)` | Filesystem-routed websites and APIs | App/Site/Generation/VM |
| `app.database(path, options?)` | Host-owned KitDB file or managed root | KitDB runtime |
| `app.gateway(target, ...)` | Reverse proxy and load balancing | Gateway pools |
| `app.proxy(config, ...)` | HTTP and SOCKS5 forward proxies | Proxy listeners |
| `app.desktop(options)` | Desktop shell declaration | Native desktop tool |
| `app.mobile(options)` | Versioned mobile shell declaration | Native mobile tool |

Surfaces may be composed when their listeners do not conflict. A web surface
can run with databases, a domain-scoped gateway, and forward proxies in one
host. Gateway-only and proxy-only manifests do not need an application
filesystem root. An owned database surface keeps its existing root contract.

## Web

```javascript
app.web({
  port: env.PORT || 8080,
  hostname: "example.com",
  allowLocal: env.ALLOW_LOCAL || false,
  trustProxy: env.TRUST_PROXY || false,
  hotReload: env.HOT_RELOAD || false,
  bytecodeCache: true,
})
```

`trustProxy` must be enabled only behind an operator-controlled edge. Forwarded
headers affect client identity, rate limiting, canonical redirects, and scheme
detection.

### Root layout

Omit `.root(...)` when exactly one of `app/` or `apps/` exists. An explicit
root selects the deployment contract:

```text
app/router.kitwork.js
    one app, one site

app/<domain>/router.kitwork.js
    one app, multiple domain sites

apps/<identity>/<domain>/router.kitwork.js
    multiple isolated tenant apps
```

Both `app/` and `apps/` existing without an explicit root is an error. The
selected layout is shared by check, prewarm, request resolution, bytecode
cache, TLS discovery, and schedulers.

## Gateway

```javascript
app.gateway(
  {
    domain: "api.example.com",
    target: "http://node-a:3000",
    weight: 2,
    health: "/health",
  },
  {
    domain: "api.example.com",
    target: "http://node-b:3000",
  },
)
```

Each argument is one trusted backend target. `target` is required, `weight`
defaults to `1`, and equal weights produce round-robin selection. Unequal
weights use a prepared smooth weighted schedule. `health` is an optional
absolute path for active checks; passive failures are tracked for every target.
Internally, targets are published as an immutable snapshot. A valid replacement
becomes visible atomically while requests already using the previous snapshot
finish before its workers and connections are closed.

A target without `domain` belongs to the default pool and is valid only in a
gateway-only manifest. This prevents a catch-all gateway from shadowing
`app.web()` routes.

## Forward proxies

```javascript
app.proxy(
  {
    protocol: "http",
    host: "127.0.0.1",
    port: 8888,
  },
  {
    protocol: "socks5",
    host: "0.0.0.0",
    port: 1080,
    username: env.SOCKS_USERNAME,
    password: env.SOCKS_PASSWORD,
  },
)
```

Defaults and invariants:

- `protocol` defaults to `http`; supported values are `http` and `socks5`.
- `host` defaults to `127.0.0.1`.
- `port` is required and must be from 1 through 65535.
- `username` and `password` must be configured together.
- A non-loopback listener requires authentication.
- Loopback and private destinations are blocked unless `allowPrivate: true` is
  explicitly set. Link-local, multicast, and special-purpose destinations
  remain blocked.
- A manifest may declare at most 32 listeners; each listener owns at most
  1,024 active connections.

HTTP supports absolute-form forwarding and HTTPS `CONNECT`. SOCKS5 supports
CONNECT with no-auth or username/password authentication; BIND and UDP
ASSOCIATE are not part of the current contract. Proxy traffic is host-owned and
never consumes tenant VM energy.

HTTP Basic and SOCKS5 username/password authentication do not encrypt the
client-to-proxy connection. A public listener therefore also needs a trusted
network, VPN, or TLS tunnel.

The first release reads immutable listener declarations from the manifest.
Repeated `app.proxy(...)` calls append to the same collection. A future
database-backed registry may become a control-plane source for a reconciler;
it is not part of the current public contract.

## Databases

```javascript
app.database({
  alias: "main",
  path: "./data.kitdb",
  memory: "128mb",
  concurrency: 6,
})

app.postgresql({ alias: "system", url: env.POSTGRESQL_URL })
app.sqlite({ alias: "legacy", name: "./legacy.sqlite" })
```

`app.database` creates a KitDB resource owned by the runtime. Provider methods
and `app.connect({ alias, type, ... })` declare external connections. Tenant
code retrieves either kind with `database.connection(alias)`; it cannot inject
an arbitrary DSN. Each call returns an independent handle, and
`database.connected(alias)` only reports availability. `database.open(...)`
opens an embedded KitDB inside the application boundary. Secrets belong in host
environment values.

Fixed owned databases and external connections share one case-insensitive alias
namespace. Duplicate declarations fail before a database is opened. Managed
roots expose names dynamically; if one of those names collides with an external
connection, resolution fails closed instead of choosing by registration order.

## Host settings

These methods describe the host and do not create a listener by themselves:

| Method | Purpose |
| --- | --- |
| `app.title(name)` | Product name used by desktop and mobile tooling. |
| `app.icon(path)` | Product icon consumed by native tooling. |
| `app.root(path)` | Explicitly select the `app/` or `apps/` filesystem contract. |
| `app.hostname(host)` | Canonical hostname used by the web host and local-domain mapping. |
| `app.hotReload(enabled)` | Enable development filesystem reloads. |
| `app.allowLocal(enabled)` | Disable production TLS/ACME behavior for local development. |
| `app.trustProxy(enabled)` | Trust forwarded identity headers from an operator-controlled edge. |
| `app.redirects(map)` | Map incoming hostnames to replacement hosts or absolute URLs. |
| `app.canonical(mode)` | Select `"apex"`, `"www"`, or an empty value to disable canonical redirects. |
| `app.rateLimit(options)` | Configure host-wide aggregate, IP, browser, and user limits. |
| `app.logger(options)` | Configure host logging level, format, and optional file output. |

## Complete API reference

### Surfaces

| Method | Creates or declares |
| --- | --- |
| `app.web(options?)` | HTTP(S) website/API host and filesystem application runtime. |
| `app.desktop(options?)` | Desktop shell metadata for native tooling. |
| `app.mobile(options?)` | Versioned mobile shell metadata for native tooling. |
| `app.database({ path, ...options })` | Host-owned KitDB file or managed root. |
| `app.gateway(target, ...)` | Reverse-proxy gateway over trusted backend targets. |
| `app.proxy(listener, ...)` | HTTP/SOCKS5 forward-proxy listeners outside tenant VMs. |

### Connections

Connections are resources consumed by app code. Every canonical object form
requires `alias`; the two-argument compatibility forms remain supported.

| Method | Resource |
| --- | --- |
| `app.postgresql({ alias, ...options })` | External PostgreSQL connection. |
| `app.mysql({ alias, ...options })` | External MySQL connection when its driver is linked. |
| `app.sqlite({ alias, name })` | Host-configured SQLite compatibility connection. |
| `app.redis({ alias, ...options })` | Redis connection when its connector is linked. |
| `app.connect({ alias, type, ...options })` | Generic external connector selected by type, driver, or URL scheme. |

Tenant code retrieves these resources through `database.connection(alias)`;
the manifest owns credentials and connection policy.

### Compatibility methods

`app.port(number)`, `app.chrome(mode)`, and `app.window(options)` are retained
for older manifests. New manifests should put these values inside `app.web()`
or `app.desktop()`. `app.databases(list)` and the flat external
`app.database({ alias, type, ... })` form remain compatibility paths; prefer
provider-specific connection methods. `server.run(...)` belongs to the legacy
`server` bootstrap path and is not needed by a modern `app.kitwork.js`.

Surface-specific options should stay inside their surface object when a flat
compatibility method is not already established.

## Lifecycle

Kitwork evaluates and validates the complete manifest before constructing
runtime owners. Host resources then start in dependency order, report an
unexpected listener failure to the process lifecycle, and drain on interruption
or termination. Forward proxies and gateway traffic remain outside tenant VMs;
web requests enter the AppRuntime hierarchy only after host-level dispatch.

`kitwork check` evaluates the same manifest and validates web application
content without opening network listeners. A manifest that has no web surface
is valid for runtime tools, while check has no site graph to prepare.
