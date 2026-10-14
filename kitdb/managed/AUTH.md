# Root-Scoped Connection Accounts

Catalog auth manages connections to the registered databases of one root. It is
not website authentication, a global user directory, file encryption, or a full
PostgreSQL role/GRANT implementation.

Managed folder attach/detach and transfer rules: [FOLDERS.md](FOLDERS.md).

Active-session revoke, account incarnation and immediate termination rules:
[REVOCATION.md](REVOCATION.md).

```text
db/
  .catalog/data.kitdb
  shop/data.kitdb
  blog/data.kitdb
```

The existing catalog database stores a bounded, versioned auth document through
the ordinary WAL. No `.system`, auth sidecar, or auth tables are added to user
databases. EnableAuth publishes the auth document and catalog v2 authority marker
atomically. Older readers reject catalog v2; missing/corrupt auth fails closed,
never falls back to a configured server password.

## Operator Commands

Run from `engine/`. Initialize/register the root and existing database folders
with `init-root` and `register-database` first. Stop every node serving this root
before auth mutations; `.catalog` exclusive ownership rejects a second opener.

```powershell
$env:KITDB_AUTH_PASSWORD = '<administrator password, at least 12 bytes>'
go run ./cmd/kitdb auth-init ./db admin

$env:KITDB_AUTH_PASSWORD = '<different reader password>'
go run ./cmd/kitdb auth-user -role reader -databases shop ./db shop_reader

$env:KITDB_AUTH_PASSWORD = '<different writer password>'
go run ./cmd/kitdb auth-user -role writer -databases shop,blog ./db builder

go run ./cmd/kitdb auth-list ./db
Remove-Item Env:KITDB_AUTH_PASSWORD
```

Passwords are read only from `KITDB_AUTH_PASSWORD`, not positional arguments or
output. Account names are safe ASCII basenames and case-insensitive. New accounts
require 12..1024 password bytes; updates with an absent/empty password retain the
existing verifier. Updating an account replaces its role/grants/status: specify
the full desired set, not a partial patch. Disable a reader without password change:

```powershell
go run ./cmd/kitdb auth-user -role reader -databases shop -disabled ./db shop_reader
```

At least one enabled administrator must remain. Auth cannot be disabled by these
commands. The first implementation supports at most 256 accounts and a 1 MiB auth
document. CLI commands require offline ownership; the limited root-admin SQL
commands below can create accounts and change grants through the serving owner.
Offline filesystem ownership is the trusted operator boundary; possessing the
database files is privileged access, not constrained by connection passwords.

## SQL Account Views

Managed-root connections expose two virtual, read-only views through native
SQL, pgwire (including the maintenance database) and KitSQL:

```sql
SELECT username, is_admin, disabled
FROM kitdb_catalog.users
ORDER BY username;

SELECT username, database_name, role,
       can_connect, can_read, can_write, can_schema
FROM kitdb_catalog.grants
ORDER BY username, database_name;

SELECT database_name, can_read, can_write, can_schema
FROM kitdb_catalog.grants
WHERE username = $1;
```

Root administrators see all accounts and grants. Ordinary accounts see only
themselves, even when WHERE/OR asks for another username. Connect-only accounts
can inspect their own account/grants without receiving read access to user data.
No verifier, salt, password or filesystem path is a view column.

`role` is reader/writer/developer/custom, or admin for root-wide administrator
grants. Administrator grants are expanded over currently registered databases.
Disabled accounts remain visible to administrators; their effective permission
flags are false. A user without grants still appears in users, but has no grants
rows. Legacy managed roots without catalog auth report only the configured
connection account; these views are not available in standalone-file mode.

This first profile supports column projections, WHERE with bound parameters,
ORDER BY known fields, LIMIT/OFFSET and COUNT(*). Joins, CTEs, UNION, DISTINCT,
other aggregates and arbitrary projection expressions are rejected. Results
obey the node's row limit and a fixed 65,536-candidate evaluation ceiling.
These are virtual views over safe catalog snapshots, not writable user tables.
Transaction queries do not gain additional visibility. These views cannot mutate
catalog state. The root-admin commands below are separate autocommit operations.

## SQL Root Commands

Catalog-auth root administrators may run these commands from a managed user
database through native SQL, pgwire or KitSQL, or from pgwire's maintenance DB:

```sql
CREATE USER alice WITH PASSWORD 'a-long-local-test-password';
GRANT CONNECT, READ ON DATABASE shop TO alice;
GRANT WRITE ON DATABASE shop TO alice;
REVOKE WRITE ON DATABASE shop FROM alice;
ALTER USER alice WITH PASSWORD 'a-new-long-local-test-password';
ALTER USER alice NOLOGIN;
ALTER USER alice LOGIN;
DROP USER alice;
DROP USER IF EXISTS alice;
```

Host clients should parameterize the password:
`CREATE USER alice WITH PASSWORD $1`. User/database names are identifiers, not
value parameters. GRANT/REVOKE accept CONNECT, READ, WRITE, SCHEMA only, without
implied permissions. CREATE USER creates a non-admin account with no grants;
duplicates fail rather than replacing passwords. GRANT/REVOKE preserve the
password and disabled status, changing only the named database's grant. Removing
every permission removes that grant. Preset roles may become a custom list.

The authenticated actor must still be an enabled administrator in the live
catalog at publication. Database schema authority is not root authority.
Configured legacy server credentials cannot enable catalog auth via SQL.
Read-only nodes reject management. Existing administrator rights cannot be
modified by per-database GRANT/REVOKE.

These commands use the existing .catalog owner and ordinary catalog transaction,
never a side connection/file owner. They require autocommit: BEGIN/data
transactions, including read-only and maintenance transactions, reject them.
This avoids a false promise of atomicity between root accounts and user data.
ALTER USER accepts PASSWORD and LOGIN/NOLOGIN, with optional WITH; password and
login status may be changed atomically in one command. It preserves grants and
root-admin status. Concurrent credential replacement or drop/recreation during
hashing rejects the stale update with SQLSTATE 40001 rather than modifying a
different account; retry from current state. DROP USER removes the complete
account and its grants in one catalog transaction, never database files or data.
IF EXISTS suppresses only missing-account errors, not authorization or admin
protection. Removing/disabling the last enabled root administrator is refused.
SQL admin promotion and broader PostgreSQL role options remain unsupported.

NOLOGIN, password rotation and DROP USER block future authentication; they do
not forcibly close existing data sessions. A dropped account disappears from
both account views, including its grants. LOGIN restores login permission but
does not reset the password or grant new database access. IF EXISTS is useful
for an acknowledged drop retry. Last-admin rejection occurs before publishing
any accompanying password/status change.

Recovery tests hard-exit a child after acknowledged SQL CREATE, password
rotation, NOLOGIN, combined PASSWORD/LOGIN and DROP without owner cleanup, then
verify the recovered credentials/status/grants from a fresh root. These tests
do not certify physical power-loss behavior or a production release.

Passwords are KDF verifiers at rest. Server SQL trace redacts password-bearing
statements, including malformed ones, and parser errors do not echo password
tokens. Client query history and network confidentiality remain client/transport
concerns; the local cleartext pgwire profile is not a production TLS/SCRAM service.

GRANT/REVOKE affect new pgwire sessions and newly opened native logical pools.
Already-open sessions keep their immutable policy snapshot; close/reconnect
(recreate the native pool) to apply changes. KitSQL reauthenticates each HTTP
request, so the next request sees the new grants. Account views show current
configured permissions, not a retroactive rewrite of an existing session policy.
No live revocation of data sessions or cross-device synchronization is claimed.

## Roles and Permissions

- `admin`: read/write every registered database in this root; full database-name
  visibility. Root lifecycle and full account administration use offline operator
  APIs; the limited SQL root commands above are also allowed.
- `reader`: SELECT/read-only transactions on specifically granted databases.
- `writer`: connect, read and data writes (INSERT/UPDATE/DELETE), without schema DDL.
- `developer`: connect, read, data writes and schema operations, including
  CREATE/ALTER/DROP, ANALYZE and REINDEX. Schema authority is powerful: it can
  destroy data by dropping objects. It is not a safe read-only maintenance role.
- Disabled accounts cannot authenticate. A sibling root has independent accounts.

Alternatively, a grant may specify an explicit `permissions` list instead of
`role`: `connect`, `read`, `write`, `schema`. Permissions imply nothing:
`read` alone does not allow a connection. Mixing role and permissions, unknown
permissions and duplicates are rejected. Empty grants are rejected.

```powershell
go run ./cmd/kitdb auth-user -permissions connect,read,schema -databases shop ./db schema_operator
go run ./cmd/kitdb auth-user -role developer -databases shop ./db developer
```

The password environment variable is still required for new accounts. Explicit
permissions are stored only in the root catalog, never in user database schemas.
Database names refer to the root registration; this patch does not change folder
registration, identity or attach/detach behavior.

The same policy is checked before SQL execution/stream opening through native,
pgwire and KitSQL, including prepared statements and explicit transactions.
Sequence mutations require read and write. Write-only grants cannot use
RETURNING or INSERT SELECT to retrieve/copy data. A connection-only account may
run the exact health probe `SELECT 1` and connection controls, but not data or
database schema queries. Pgwire's virtual maintenance database continues to show
only databases the account may connect to; it exposes no user table contents.

Root lifecycle and backup remain host-trusted offline operations. SQL account
commands require root admin, not a client `backup` or database `manage` grant.
There are no table/column-level grants.

Auth document v2 distinguishes the new grants; older auth binaries reject v2.
Existing v1 writer accounts are read as `developer`, preserving their former
DDL authority. An operator must explicitly replace their role with `writer`
to remove DDL. The next account update writes v2; catalog root version remains 2.

Authenticated metadata queries list only permitted databases plus the virtual
maintenance database (`kitdb` by default). Neither `.catalog` nor `.system` is
exposed as an ordinary database. User tables contain no account/password metadata.

## Serving and Adapters

```powershell
go run ./cmd/kitdbpg -root ./db -listen 127.0.0.1:5434
```

No configured password is needed when catalog auth is enabled, and configured
credentials are ignored rather than accepted as an administrator backdoor.
Unconfigured legacy/flat roots retain their existing required server credentials.
Single-file `-file` mode remains a separate host-trusted deployment, not a root
auth endpoint. Pgwire is still the loopback cleartext profile: `sslmode=disable`.
This change adds neither TLS nor SCRAM and does not authorize a public listener.

Native clients use `node.OpenAuthenticatedDatabase(ctx, user, password, database)`.
Unauthenticated `node.OpenNativeDatabase` is refused for auth-enabled roots.
The pool borrows the existing node's relational engines; it never opens a side DB.
Host code holding a raw standalone engine remains trusted, not a sandboxed client.

KitSQL uses the same entrance:

```go
handler, err := kitsql.NewHandler(kitsql.HandlerOptions{
    Open: kitsql.OpenNodeDatabase(node),
    AllowInsecureLocal: true, // development loopback only
})
```

Invalid credentials produce `KSQL_AUTH`; a valid account without a database grant
produces `KSQL_FORBIDDEN` (403). Readers are enforced by the same relational
read-only contract as pgwire/native, including explicit transactions. Statement
errors remain separate from authentication and do not make writes replay-safe.

Session permissions are captured at connection creation. Offline password/status
updates still require stopping the node. SQL grants may change while serving,
with the new-session semantics described above. No live data-session revocation
API is claimed.

KitSQL v1 verifies credentials on each HTTP request; native and pgwire verify at
pool/connection creation. The password KDF therefore adds per-request cost to
KitSQL. No token/session optimization or throughput guarantee is claimed here.

## Active Sessions

Database-local table SELECT roles are documented in [LOCAL_ROLES.md](LOCAL_ROLES.md).
Root login/account permissions remain separate from those portable definitions.
See [ROLE_VIEWS.md](ROLE_VIEWS.md) for configured versus session-effective rights.

`kitdb_catalog.sessions` is a read-only, process-local view, not a persisted table
in `.catalog`. Root administrators see all registered sessions; other accounts
see only their own, filtered before SQL predicates. It supports the same bounded
single-view projection, WHERE, ORDER BY, LIMIT/OFFSET and COUNT(*) as users/grants.

```sql
SELECT session_id, username, database_name, adapter, started_at, transaction_state
FROM kitdb_catalog.sessions
ORDER BY session_id;

SELECT COUNT(*) FROM kitdb_catalog.sessions WHERE adapter = 'pgwire';
```

Adapters are `native`, `pgwire`, and `kitsql`. Each row represents one physical
authenticated connection borrowing a node engine, or a pgwire maintenance
session. A lazy native pool has no row before its first connection; idle pooled
connections still count. KitSQL rows exist only while an HTTP request owns its
connection, including the request querying this view. Standalone engine adapters
outside a node are not registered.

`started_at` is UTC RFC3339 text. `transaction_state` is `idle`, `transaction`, or
`failed` (pgwire aborted explicit transaction). It is not a query activity state:
`idle` also includes autocommit work. Transaction state reflects completed
operation boundaries; the view is not a transactionally consistent fleet snapshot.
IDs are monotonic within one node lifetime and restart-local, not durable identity.
No query text, password, verifier, filesystem path, or remote address is exposed.

`PostgresNodeOptions.MaximumSessions` bounds this registry across all adapters,
including idle connections and maintenance sessions. Default: 1024; supported
range: 1..65536. Exhaustion rejects new connections with SQLSTATE `53300` and
releases their acquired engine reference. The pgwire listener's separate
connection limit still applies. Node stats distinguish `RegisteredSessions`
from the existing data-engine reference count `Sessions`.

Connection close removes its row after transaction rollback and lease release.
Node Close drains registered logical connections before closing engines and the
manager. Native transaction-context cancellation and pgwire disconnect roll back
uncommitted data. This adds no automatic revocation, query history, or audit log.
LOGIN/NOLOGIN, DROP USER and grants retain their new-authentication semantics.

### Explicit Session Termination

```sql
SELECT session_id, username, adapter, transaction_state, terminating
FROM kitdb_catalog.sessions ORDER BY session_id;

KILL SESSION 12;
```

This is KitDB runtime-control SQL, not a PostgreSQL compatibility claim.
It requires catalog authentication and autocommit, outside data or maintenance
transactions. A live enabled root administrator may target any session; an
ordinary enabled user may target only another physical session of that account.
Authority is rechecked against current accounts, not just a captured admin flag.
Disabled or removed accounts cannot issue this command. Missing and unauthorized
targets share SQLSTATE `42501` to avoid an ID oracle. The issuing physical
session cannot target itself (`55000`); use another connection. Invalid syntax
or value parameters fail with `42601`.

Acknowledgment means cancellation has been requested, not that cleanup has
finished. The row stays registered, with `terminating=true`, until rollback and
lease release finish. Repeated requests while the row exists coalesce into one
close worker; workers are bounded by the registry ceiling. Prepared-statement
Describe validates authority without canceling anything.

Termination cancels operation contexts and closes the pgwire transport, including
idle reads and query-admission waits. Native row streams are closed on lifetime
cancellation. An unfinished explicit transaction rolls back. Already committed
work is not undone, and a commit that wins the durability race can remain
committed. A host trace callback or other non-cooperative code cannot be
preempted; cleanup waits until it returns. This is not a forced process kill.

KitSQL termination affects only the selected HTTP request's connection, not
later requests. Native/pgwire pools may reconnect if the account still has LOGIN
and a connect grant; this command neither disables an account nor closes its
logical pool. For offboarding, first deny new authentication, then explicitly
terminate remaining sessions.

IDs are local to one node lifetime: fetch current IDs after restart, not stored
IDs. Standalone engine adapters outside an authenticated managed node do not
receive this control surface.

Verification:

```text
go test -race ./kitdb/relational -run ^TestSessionRegistry -count=1
go test -race ./kitdb/kitsql -run ^TestSQLAccountAdministrationAdapters$ -count=1
```

## Password Storage

Verifiers use the Go standard library's PBKDF2-HMAC-SHA256, 600,000 iterations,
16-byte random salt per password, and a 32-byte derived key. Parameters are fixed
and validated on open, not accepted from unbounded input. Unknown/disabled users
perform the bounded KDF too; each root admits at most four concurrent derivations.
Password comparison uses constant-time comparison. This is a stdlib-only choice,
not a claim of FIPS certification or an Argon2id implementation.

Work-factor reference: [OWASP Password Storage Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html).

## Verify

```text
go test -race ./kitdb/managed
go test -race ./kitdb/kitsql -run ^TestNodeCatalogAuthAcrossAdapters$ -count=1
go test ./cmd/kitdb
```

Coverage includes persistence/reopen, wrong passwords, disabled users, last-admin
protection, root isolation, missing-auth fail-closed behavior, native fallback
denial, database grant checks, metadata filtering, and reader/writer SQL behavior
through native/pgwire/real HTTP KitSQL. This is not release or long-run qualification.
