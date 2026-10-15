# KitSQL v1

KitSQL is KitDB's first-party remote SQL protocol. It is an adapter over the
same relational engine and transaction kernel used by native access and the
PostgreSQL-compatible listener. It is not another storage engine and does not
create another copy of the database.

## Access paths

Managed nodes can share root-scoped accounts with native and pgwire via
`HandlerOptions.Open: kitsql.OpenNodeDatabase(node)`. See
[catalog connection accounts](managed/AUTH.md) for offline provisioning, reader/
writer grants, verifier storage, and catalog-v2 compatibility. This is separate
from website/application auth and does not automatically enable auth for a root.

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

Requests use `application/vnd.kitsql+json`. Exec responses and compatibility
query responses use the same media type. The v1 Go driver requests
`application/vnd.kitsql.stream+json` for queries; the server then sends one
newline-delimited `columns` frame, bounded `row` frames, and one `complete`
frame. A failure discovered after the headers is an `error` frame and becomes
`Rows.Err()` at the client. The driver still accepts the original aggregate
JSON response so a newer client can talk to an older v1 server.

Every value has an explicit type. Integers and floating-point numbers travel
as strings so JSON decoders cannot silently round 64-bit values. Bytes use
base64 and timestamps use RFC 3339 with nanoseconds.

For a single-table row projection that does not require a materializing
operator, the native relational adapter now carries one fixed-snapshot cursor
all the way into `database/sql`. Its one-row handoff applies backpressure when
the remote client is slow. Closing rows cancels the producer and releases the
snapshot. `JOIN`, aggregate, `DISTINCT`, uncovered `ORDER BY`, query-cache
hits, and other materializing plans keep the existing bounded executor. Thus
stream framing does not claim that every SQL plan is storage-to-client
streaming.

KitSQL v1 is deliberately bounded by default:

- request body: 1 MiB
- parameters: 4,096
- result rows: 10,000
- encoded result payload: 16 MiB (including JSON escaping, base64 and framing)
- concurrent requests: 64
- admitted request lifetime: 30 seconds, including body reads and response writes

Streamed results count every columns, row and completion frame, including its
newline, against the byte budget. A fixed terminal byte-limit error may exceed
that budget so the client sees an explicit failure, never a successful partial
query. Other terminal error messages are capped at 512 bytes. The driver caps result
bytes at 32 MiB with a 4 KiB terminal-control allowance, verifies the completion
row count, and closes the response body on malformed or truncated streams.

On the standard HTTP server, read/write deadlines and request cancellation
interrupt stalled socket I/O. Middleware wrapping the response writer must
preserve ResponseController support (for example via `Unwrap`); a custom writer
without deadline support cannot provide the same socket-lifetime guarantee.
Deadline state is cleared when the handler returns. Closing or cancelling a
stream releases its snapshot, database lease and request admission slot.
With Go's TLS transport, a failed write can additionally spend up to five
seconds sending `close_notify` before the handler returns. QueryTimeout bounds
query work and ordinary socket I/O, not that standard-library teardown grace.

The host adapter derives its request concurrency from `app.database`
configuration. Capacity exhaustion returns HTTP 429 rather than creating an
unbounded queue.

Relational execution also shares the owning database's 32-slot snapshot budget.
It reserves capacity before entering the catalog/commit gate, without opening a
snapshot file or copying the WAL overlay while waiting. Up to 128 callers wait
FIFO, for at most two seconds per admission attempt (or their shorter context).
The existing bounded catalog-consistency retry can make multiple attempts;
the request deadline remains the overall bound. This does not increase the
snapshot ceiling or replace HTTP request admission or the native connection pool.

Admission also checks accounted snapshot bytes; frontend and kernel transaction
writes share a separate per-DB budget. Both default to 64 MiB. Budget rejection
returns `KSQL_BUSY` / 503, including capture-time growth beyond a reservation.
See [memory budgets](MEMORY_BUDGET.md) for configuration and scope; these are
not whole-process RAM caps.

## Execution errors and retry safety

The Go client exposes `*kitsql.ProtocolError` with `Status`, `Code`, `Message`,
and `Retryable()`. Execution errors preserve these distinctions:

| Code | HTTP status | Meaning |
| --- | ---: | --- |
| `KSQL_CONFLICT` | 409 | Optimistic transaction validation rejected the write |
| `KSQL_BUSY` | 503 | Database snapshot/transaction capacity is exhausted |
| `KSQL_ADMISSION_TIMEOUT` | 503 | Snapshot admission wait expired before execution |
| `KSQL_TIMEOUT` | 504 | Execution context deadline expired |
| `KSQL_CANCELLED` | 408 | Execution context was canceled |
| `KSQL_UNAVAILABLE` | 503 | Database is closed or unavailable |
| `KSQL_DURABILITY` | 500 | Durability outcome is uncertain |
| `KSQL_EXECUTION` | 400 | Other SQL execution failures |
| `KSQL_FORBIDDEN` | 403 | Database access or SQL statement permission denied (SQLSTATE 42501) |

HTTP admission still returns `KSQL_BUSY` with status 429. After stream headers
have been sent, a terminal error frame carries the code rather than changing
the HTTP status. Transport cancellation may return a context/transport error
instead of a protocol response.

`Retryable()` is true only for conflict, busy, and admission-timeout codes.
The client never automatically replays a statement. A caller must retry the
whole logical transaction after a conflict and use bounded backoff/deadlines
for capacity rejections. Never blindly replay a write after a transport error,
execution timeout, cancellation, or uncertain durability: its outcome may need
reconciliation. Syntax and other execution failures require correction first.

## Transaction boundary

One v1 request is one atomic SQL execution. The stateless driver rejects
`database/sql` transactions because `BEGIN`, later statements, and `COMMIT`
could otherwise land on different HTTP requests. This is an honest protocol
boundary, not a KitDB kernel limitation: native and PostgreSQL compatibility
already support session transactions. A future KitSQL session extension must
add bounded leases, expiry, cancellation, and recovery before exposing this
capability.

## Validation

See [Streaming validation](kitsql/STREAM_VALIDATION.md) for real HTTP/TLS
lifecycle coverage, native SQL parity, shutdown checks, benchmark commands,
and the remaining production-qualification boundaries.
