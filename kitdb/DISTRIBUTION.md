# KitDB RC Distribution

KitDB is a pure-Go database with embedded and standalone entry points. These
bundles are release candidates for controlled testing. The compiled stability
remains `release-candidate`; packaging does not qualify a deployment as stable.

## Build A Candidate

From a clean committed Engine checkout, with Go 1.26 and dependencies available:

```sh
go run ./cmd/kitdbdist --version v1.0.0-rc.1 --output .artifacts/kitdb-rc
```

The output directory must not exist. The builder produces Windows/amd64 and
Linux/amd64 directories and ZIP archives plus `SHA256SUMS`. Use
`--targets linux/amd64` or `--targets windows/amd64` for one platform.

Each archive contains `kitdb`, `kitdbpg`, `kitdbcanary`, operator
documentation, license texts, and `manifest.json`. The manifest records the
full commit, candidate version, toolchain, kernel compatibility, platform, and
SHA-256/size of every bundled file except itself. The archive checksum covers
the manifest too. Builds use `CGO_ENABLED=0`, `-trimpath`, and amd64 baseline v1.
Distribution dependency checks reject Kitwork VM/host packages and unreviewed
external dependencies. The delivered binaries use the Go standard library and
the reviewed KitDB/search packages only. The Go license is included.

Only reviewed files are copied; application data, `.env`, logs and profiling
artifacts are excluded. The source tree is checked before and after packaging.
Do not edit that checkout during the build. Fixed ZIP timestamps support
repeatability on the same build host/toolchain; cross-host byte identity has
not been qualified. Native binaries are executed to check their build identity;
cross-built binaries still require a native platform test.

The builder never pushes a tag, uploads an archive, or changes the compiled
stability. License texts are preserved as stored in the repository, including
the explicitly draft status of `LICENSE-EXCEPTION.md`. Review licensing and
corresponding-source publication before external distribution.

## Run The Binaries

Unzip the native archive. Commands below assume its directory is on PATH; on
Windows PowerShell use `./kitdb.exe`, `./kitdbpg.exe`, and so on.

```sh
kitdb version
kitdb query --create demo.kitdb "CREATE TABLE products (id BIGINT PRIMARY KEY, name TEXT NOT NULL, price BIGINT NOT NULL)"
kitdb query demo.kitdb "INSERT INTO products (id,name,price) VALUES (1,'keyboard',100)"
kitdb query demo.kitdb "SELECT id,name,price FROM products ORDER BY id LIMIT 20"
kitdb doctor demo.kitdb
```

`kitdb version` preserves the existing kernel compatibility fields and adds
`build` with the candidate version, full commit, toolchain and platform.
Other bundled commands expose the same identity with `--version`. Ordinary
unstamped builds report `development`.

Set `KITDB_TOKEN` in the process environment, then start a local server:

```sh
kitdbpg -file demo.kitdb -database demo -listen 127.0.0.1:5433 -retain-history
```

Connect with a PostgreSQL client using host `127.0.0.1`, port `5433`, database
`demo`, user `kitdb`, the configured token as password, and `sslmode=disable`.
The listener is loopback-only. See `POSTGRES_COMPATIBILITY.md` for the supported
SQL/protocol profile. Stop the server before running offline `doctor`.

## Multiple Databases And Users

Single-file mode above uses one listener credential. For separate users,
database grants and portable local roles, initialize a managed root instead.
This example uses PowerShell 7, a fresh `node` directory, and the unpacked
executables in the current directory:

```powershell
New-Item -ItemType Directory -Path ./node/shop -ErrorAction Stop
./kitdb.exe init-root ./node
./kitdb.exe query --create ./node/shop/data.kitdb "CREATE TABLE articles(id BIGINT PRIMARY KEY,title TEXT)"
./kitdb.exe query ./node/shop/data.kitdb "INSERT INTO articles VALUES(1,'First public article')"
./kitdb.exe register-database ./node shop shop
```

Check each command's exit code before continuing. Bootstrap the root's first
administrator offline. The password comes from the process environment, not a
command argument; do not store it in a script, transcript or source control:

```powershell
$env:KITDB_AUTH_PASSWORD = Read-Host 'New KitDB admin password' -MaskInput
try {
    ./kitdb.exe auth-init ./node admin
    if ($LASTEXITCODE -ne 0) { throw 'Administrator bootstrap failed' }
} finally {
    Remove-Item Env:KITDB_AUTH_PASSWORD -ErrorAction SilentlyContinue
}
./kitdb.exe auth-list ./node
./kitdbpg.exe -root ./node -listen 127.0.0.1:5433 -verify-on-open
```

Connect using database `shop`, user `admin`, the bootstrap password and
`sslmode=disable`. This is a local cleartext transport profile, not a public
Internet endpoint. A new user has no database rights until explicitly granted.
Run the following as the administrator in that connection:

```sql
CREATE USER reader WITH PASSWORD 'replace-with-a-unique-test-password';
GRANT CONNECT ON DATABASE shop TO reader;
CREATE ROLE article_reader;
GRANT SELECT ON TABLE articles TO ROLE article_reader;
GRANT ROLE article_reader ON DATABASE shop TO reader;

SELECT role_name, role_id FROM kitdb_catalog.roles;
SELECT * FROM kitdb_catalog.role_members;
SELECT * FROM kitdb_catalog.table_grants;
```

The SQL password is only a placeholder. Applications should bind it as `$1`
with their PostgreSQL driver rather than concatenate it into SQL. Do not enable
query logging or save SQL-editor history containing real passwords.

Reconnect as `reader`: `SELECT * FROM articles` should work; `INSERT` and
`CREATE TABLE` should fail with SQLSTATE `42501`. An incorrect password should
fail authentication with `28P01`, not be counted as a permission-denial test.
To allow selected writes, grant `INSERT`, `UPDATE` or `DELETE` on the table to
the local role; this does not grant schema changes or administrator rights.

The ownership boundary is deliberate:

- `node/.catalog/` owns root logins, password verifiers, database registrations,
  database access grants and user-to-local-role mappings.
- `node/shop/data.kitdb` owns the database's data, schema, local roles and table
  permissions. Back up and restore it using the operator commands, not by
  copying a file from a live writer.
- Copying or restoring one database does not copy the root's accounts or grant
  access to a same-named user in another root. The destination administrator
  must explicitly grant database access and map the restored role.
- Direct-file CLI operations are trusted offline operator access, not SQL
  sessions; they are not constrained by SQL user permissions. Protect the
  filesystem and restrict access to the operator executables accordingly.

`init-root`, `register-database`, `auth-init`, `auth-user`, `auth-list`, and
`root-catalog` require offline root ownership. Stop the root server before
running them. Online account management goes through the authenticated SQL
administrator; never open a second offline owner while a server is running.

### Restore Into Another Root

Stop the source server and all other writers first. Use fresh destination paths:

```powershell
./kitdb.exe verify ./node/shop/data.kitdb
./kitdb.exe backup ./node/shop/data.kitdb ./shop-backup.kitdb
New-Item -ItemType Directory -Path ./restored-node/shop -ErrorAction Stop
./kitdb.exe init-root ./restored-node
./kitdb.exe restore ./shop-backup.kitdb ./restored-node/shop/data.kitdb
./kitdb.exe verify ./restored-node/shop/data.kitdb
./kitdb.exe register-database ./restored-node shop shop
```

Check every exit code. Bootstrap a fresh destination administrator with
`auth-init` as above, then start `kitdbpg -root ./restored-node`. Recreate the
destination login and grant `CONNECT` plus `ROLE article_reader` explicitly.
The role and its table permissions already belong to the restored database;
do not recreate the role merely to connect a new root user to it. Verify both
allowed reads and denied writes using a fresh reader connection. Keep the
source and destination independent; do not copy the old `.catalog` merely to
make a restored database accessible.

## Verify The Delivered Executables

From the source checkout, set `KITDB_DIST_DIRECTORY` to the unpacked native
bundle, then run:

```sh
go test ./cmd/kitdbdist -run '^TestKitDBDistributionNativeJourney$' -count=1 -v -timeout 5m
```

This verifies file checksums and all three binary identities, then exercises
standalone SQL, search, PostgreSQL-protocol transactions, an
independent doctor backup/restore drill, and a short storage canary using the
actual executables in a disposable database directory.

The required `authorization-portability` subtest also uses those delivered
executables. It bootstraps an isolated managed root, creates password users via
SQL, grants database access and local table roles, and checks exact `28P01`
authentication failures and `42501` permission denials. After a hard server
restart, accounts, acknowledged rows and grants must survive. An independent
backup/restore preserves exact article rows and local role identity, but does
not transfer root logins or mappings: a same-named destination user with CONNECT
only cannot read until explicitly mapped. The restored database must remain
writable by its administrator. Finally all three child servers are reaped and
offline CLI commands reacquire both roots and verify both databases.

Set `KITDB_NATIVE_REPORT` to a NEW JSON path to retain structured qualification
evidence (relative paths resolve from the module root):

```powershell
$env:KITDB_DIST_DIRECTORY = 'C:/test/kitdb-unpacked'
$env:KITDB_NATIVE_REPORT = '.artifacts/kitdb-native-journey.json'
go test ./cmd/kitdbdist -run '^TestKitDBDistributionNativeJourney$' -count=1 -json -timeout=5m
```

The report records candidate/manifest identity, all three mandatory subtests,
ten authorization stages, success, and `stable_release_qualified=false`.
Filtered, failed or skipped mandatory subtests cannot qualify. A configured
report without a binary directory fails rather than skipping; an existing
report path is refused, so retain each run separately. Ordinary source-only
`go test` still skips this opt-in journey when neither variable is configured.
The RC workflow requests the report, checks its commit against checkout HEAD,
and retains it beside the raw Go JSON events. These are bounded tests, not a
24-hour storage run or a stable-release admission decision.

The `mixed-application` subtest adds two concurrent transaction writers and
three readers (snapshot totals, composite-key JOIN, and SEARCH aggregates).
Its independent order model checks every retained row and the transactionally
maintained revenue counter. A barrier makes readers run while both writers
have uncommitted changes. It kills the native server with another transaction
open, checks acknowledged data after restart, restores a verified backup to a
different database, and performs timestamp recovery after deliberate UPDATE
and DELETE damage. Only explicit `40001` transaction conflicts are retried;
transport errors at COMMIT are not assumed safe to retry. The two existing
SEARCH stable-boundary retry errors are separately retried with a 32-attempt
ceiling and reported in the test log, not counted as uninterrupted availability.
Other SEARCH errors fail immediately. This is bounded
application and process-crash evidence, not power-loss or long-soak evidence.

During development, `KITDB_APPLICATION_BINARY_DIRECTORY` can point to freshly
built `kitdb` and `kitdbpg` executables for
`go test ./cmd/kitdbdist -run '^TestKitDBApplicationNativeJourney$' -count=1 -v`.
That opt-in does not validate a distribution manifest or qualify a release.

### Standalone Commerce Gate

```sh
go test ./cmd/kitdbdist -run '^TestKitDBCommerceNativeJourney$' -count=1 -v -timeout 5m
```

This test always builds the current checkout's native `kitdb` and `kitdbpg`
with CGO disabled and the standalone dependency allowlist enforced. It starts
only disposable databases on dynamically allocated loopback ports, never an
existing application server. Both development/release gate plans require it;
the release campaign repeats it ten times. `KITDB_COMMERCE_REPORT` optionally
writes its bounded JSON evidence at a path relative to the module root. The
gate supplies `.artifacts/kitdb-commerce-gate.json`; repeated runs rely on the
enclosing gate result instead of overwriting that file with a last-run result.

The `commerce-objects` subtest of `TestKitDBDistributionNativeJourney` runs the
same scenario against the checksum-verified delivered binaries, not a rebuild:

- Four related tables: products, orders, order_items and audit. Named sequence,
  a nonnegative-money domain, pure SQL functions, and transactional triggers.
- INSERT/UPDATE expressions and RETURNING, compound item keys, unique order
  references, foreign keys, stock checks, and exact NUMERIC cents beyond 2^53.
- A final domain, trigger, RETURNING, FK or duplicate-key failure must roll
  back the order, lines, stock changes and earlier audit actions. PostgreSQL
  requires ROLLBACK after the failed transaction; sequence gaps are expected.
- An independent integer-cent model compares every business field and audit
  event, maintained counts, a secondary-index predicate and JOIN aggregates.
  Readers on another connection must not see an open transaction's writes.
- Kill the actual native server before client rollback, require an abnormal
  exit, then recover acknowledged commits without the pending order/audit.
- Verify and restore a backup into a different, writable database. Restore to
  a timestamp before bulk UPDATE/DELETE, trigger removal and function-body
  replacement, then create another order to prove recovered objects execute.
  The deliberately damaged source stays damaged; recovery does not rewrite it.

The fixture uses the currently supported CHECK comparison/OR profile rather
than implying general CHECK expression support (CHECK IN is not enabled by
this test). This is a bounded correctness drill, not a throughput benchmark,
multi-tenant soak or physical power-loss qualification. It does not promote
the development DOMAIN/TRIGGER profile or a dirty checkout to stable 1.0.

### Standalone Import Gap

`kitdbimport` is **not included** in these bundles. Its resumable CSV/JSONL
protocol requires COPY plus `PRAGMA import_status/import_cancel/import_forget`
and atomic KIMP checkpoints. These currently belong to the Kitwork adapter;
`kitdb/relational` does not implement them. The first native distribution
journey confirmed that the importer fails against `kitdbpg` at `import_status`.
Building the importer successfully does not establish standalone compatibility.

Port and qualify that integration, including disconnect/retry/schema-change
and crash atomicity, before offering standalone resumable bulk import. Do not
pull the Kitwork VM/host into the bundle to hide this gap. Until then, use the
documented SQL transaction/INSERT interface for application writes; the RC
does not claim a ready-to-use bulk migration tool. The native journey also
checks that unsupported COPY fails without damaging rows or the connection.

Run the long canary binary on the intended storage filesystem:

```sh
kitdbcanary --root /qualified/storage/canary --duration 24h --tenants 128 --workers 16 --max-open 32 --json canary-24h.json
```

Reports include `build`, `requested_workload_ms`, `workload_ms`, and
`workload_completed`. For 24-hour evidence, both workload durations must reach
86,400,000 ms, the workload must complete, and `success` must be true. Total
`duration_ms` also includes setup and final backup/restore; it cannot substitute
for actual workload duration. Interrupted runs do not qualify. Reports from
`development` builds do not identify a packaged candidate.

## Remaining Promotion Evidence

Before 1.0 stable, retain same-commit Windows and Linux release reports and
native binary journeys, a deployment-filesystem 24-hour canary, independent
backup/PITR/restore drills, publisher restart evidence, upgrade/rollback
rehearsal, and workload-specific limits. Follow `RELEASE_1_0.md` and
`PRODUCTION.md`; a short smoke test never replaces those drills.

The Go source module is currently `github.com/kitwork/engine`. Standalone
binaries do not require a Kitwork host. A future public module path and its
API commitment must be decided before a stable Go-library release; this RC
builder does not rename packages or publish a new repository.

The dedicated `kitdb-rc.yml` workflow builds and tests native bundles on both
platforms and uploads CI artifacts for review. It performs no public release.
