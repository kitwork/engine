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
