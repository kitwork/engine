# KitSQL v1

KitSQL is KitDB's first-party remote SQL protocol. It is an adapter over the
same relational engine and transaction kernel used by native access and the
PostgreSQL-compatible listener. It is not another storage engine and does not
create another copy of the database.

## Access paths

| Path | Purpose | Transport |
| --- | --- | --- |
| Native KitDB | Lowest-latency access in the owning Go process | No network |
| KitSQL | First-party remote applications and services | HTTP(S) + typed JSON |
| PostgreSQL compatibility | Existing DB managers, drivers, and PostgreSQL-shaped tooling | pgwire |

The public connection URL is:

```text
kitsql://user:password@host/database
```

TLS with normal certificate and hostname verification is the default. The
query parameter `sslmode=disable` is accepted only for `localhost`, `127.0.0.1`,
or another loopback address. Clients never follow redirects because a redirect
could forward credentials to another authority.

## Server configuration

Kitwork can publish KitSQL on its existing web listener without opening another
port:

```javascript
import { app, env } from "kitwork"

app
  .database("./.kitdb/", {
    user: "kitdb",
    password: env.require("KITDB_PASSWORD"),
    kitsql: true,
    memory: "128mb",
    concurrency: 8,
  })
  .web(8080)
```

The endpoint is `POST /_kitsql/v1/query`. It is absent unless `kitsql: true` is
declared. A managed root resolves the database segment of the URL through its
`.catalog`; a single file resolves it through the configured `alias` (or
`default`).

## Go client

Import `github.com/kitwork/engine/kitdb/kitsql`, then use either its checked
opener or the registered `database/sql` driver:

```go
database, err := kitsql.Open(
    "kitsql://kitdb:secret@shop.example.com/shop",
)
```

```go
database, err := sql.Open(
    "kitsql",
    "kitsql://kitdb:secret@127.0.0.1:8080/shop?sslmode=disable",
)
```

`app.connect("shop", env.KITSQL_URL)` uses the same driver inside Kitwork.

## Wire contract

Requests and responses use `application/vnd.kitsql+json`. Every value has an
explicit type. Integers and floating-point numbers travel as strings so JSON
decoders cannot silently round 64-bit values. Bytes use base64 and timestamps
use RFC 3339 with nanoseconds.

KitSQL v1 is deliberately bounded by default:

- request body: 1 MiB
- parameters: 4,096
- result rows: 10,000
- result payload estimate: 16 MiB
- concurrent requests: 64
- execution time: 30 seconds

The host adapter derives its request concurrency from `app.database`
configuration. Capacity exhaustion returns HTTP 429 rather than creating an
unbounded queue.

## Transaction boundary

One v1 request is one atomic SQL execution. The stateless driver rejects
`database/sql` transactions because `BEGIN`, later statements, and `COMMIT`
could otherwise land on different HTTP requests. This is an honest protocol
boundary, not a KitDB kernel limitation: native and PostgreSQL compatibility
already support session transactions. A future KitSQL session extension must
add bounded leases, expiry, cancellation, and recovery before exposing this
capability.
