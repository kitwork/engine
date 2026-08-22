# KitDB

KitDB is an experimental, embedded, pure-Go database kernel. It is incubated
inside the Kitwork engine repository, but the package does not import the
Kitwork VM, runtime, tenant, database, or search packages.

The current milestone proves a durable path with one canonical main file,
append-only immutable generations, lazy checksummed row reads, bounded read
snapshots, bounded compaction, and a bounded transaction replay window:

```text
Open -> Begin -> Put/Delete -> Commit -> Checkpoint -> Close -> Reopen -> Get
```

`tenant.kitdb` contains immutable sorted mutation segments. Each segment has
checksummed pages and a checksummed sparse directory containing page key
bounds, offsets, lengths, mutation counts, and CRC32C values. Two fixed
superblock slots alternate between generations. The newest valid slot points
to a checksummed manifest describing the complete logical state.

New commits are stored in `tenant.kitdb.wal` as checksummed append-only frames
and become visible in memory only after the WAL has been synced. A normal
`Checkpoint` appends only the sorted WAL overlay plus a new manifest, syncs
those bytes, publishes the inactive superblock slot, syncs again, and only
then rotates the WAL. Old segments are immutable and remain readable during
publication.

If a process dies between those two publications, recovery validates the stale
WAL through the main-file boundary before completing rotation. An incomplete
tail after the durable boundary is removed; a missing or mismatched boundary is
corruption.

Opening a format-v3 database verifies its header, both superblock slots, the
active manifest, and segment directories without reading mutation pages.
`Get` checks the WAL overlay first and then searches segments from newest to
oldest, so the newest put or tombstone wins. Every selected page is verified
before decoding into a bounded LRU cache. `Walk` performs a bounded k-way merge
of the active segments. `Verify` explicitly scans every active persisted page.

```go
db, err := kitdb.Open("tenant.kitdb")
if err != nil {
	return err
}
defer db.Close()

tx, err := db.Begin()
if err != nil {
	return err
}
if err := tx.Put([]byte("product/1"), []byte(`{"title":"Kitwork"}`)); err != nil {
	return err
}
transaction, err := tx.Commit()
if err != nil {
	return err
}
_ = transaction

if _, err := db.Checkpoint(); err != nil {
	return err
}

value, found, err := db.Get([]byte("product/1"))
```

An explicit snapshot keeps one committed logical view while later commits and
incremental checkpoints continue. Range cursors seek through sparse page
directories and stream in raw-key order:

```go
snapshot, err := db.Snapshot()
if err != nil {
	return err
}
defer snapshot.Close()

cursor, err := snapshot.Cursor(kitdb.RangeOptions{
	Prefix: []byte("product/"),
	Limit:  100,
})
if err != nil {
	return err
}
defer cursor.Close()

for cursor.Next() {
	key := cursor.Key()
	value := cursor.Value()
	_, _ = key, value
}
if err := cursor.Err(); err != nil {
	return err
}
```

Snapshots own independent read handles and copied WAL overlays. At most 32 may
be active per database. They do not block commits or incremental checkpoints,
but full compaction returns `ErrSnapshotsActive` until they close. `DB.Close`
closes any snapshots the caller forgot to release.

The default page-cache ceiling is 16 MiB per open database. Hosts managing many
tenants can set a smaller budget or disable it:

```go
db, err := kitdb.OpenWithOptions("tenant.kitdb", kitdb.OpenOptions{
	PageCacheBytes: 4 << 20,
	VerifyOnOpen:   true,
})

stats, err := db.Stats()
```

`VerifyOnOpen` is optional. The default fast-open path verifies each page on
first access; hosts can run `db.Verify()` as an explicit startup check or a
background scrub. Verification reads do not populate the LRU cache.

`Stats` exposes the active generation, logical record count, physical mutation
count, segment count, block count, active file boundary, WAL size, overlay
size, active snapshot count, and page-cache use. Hosts can make maintenance and
admission decisions without inspecting user data.

## Kitwork struct/ORM pilot

Kitwork exposes KitDB through the same schema-aware ORM used by its SQLite and
Turso backends. Raw key/value operations remain kernel internals:

```javascript
import { database } from "kitwork";

const { kitdb, struct, id, text, int, enum, updated } = database;

const products = struct({
  id: id(),
  sku: text().notNull().unique().index(),
  title: text().notNull(),
  price: int().default(0),
  status: enum("active", "disabled").default("active").index(),
  updated_at: updated(),
});

const db = kitdb("catalog.kitdb", { products });

db.products.create({ sku: "KIT-1", title: "Kitwork", price: 10 });
const product = db.products.where("sku", "=", "KIT-1").first();
const active = db.products.where("status", "=", "active").limit(20).list();
```

`struct()` normalizes fields into versioned Schema IR with deterministic
struct/field identities and a schema hash. The pilot supports the shared
`create`, `where`, `find`, `first`, `list`, `sort`, `limit`, `count`, `exists`,
`update`, and `delete` surface. Primary keys, not-null fields, unique fields,
secondary indexes, enum/default/timestamp behavior, and restrictive foreign
keys are enforced atomically with each row mutation.

Files stay below the tenant's `.data` directory, and AppRuntime owns and closes
their handles. Reads use immutable snapshots. Point/equality queries prefer
primary, unique, or declared secondary indexes and fall back to a bounded-memory
row scan only when no suitable index exists. Result limits retain the ORM's
120-row hard cap.

The adapter deliberately does not parse generated SQL. The fluent builder
exports storage-neutral query state, which KitDB executes directly. Existing
rows also pin their schema hash: a changed struct is refused until an explicit
migration exists. Joins, grouping/aggregates, cascade actions, online schema
migration, and the search projection are not yet connected.

## Remote Hrana SQL-light profile

Hrana is an optional network adapter, not KitDB's internal query model or file
format. A tenant explicitly exposes a KitDB file with a token; omitting the
token leaves every remote endpoint closed:

```javascript
const db = kitdb("catalog.kitdb", { products }, {
  token: env.require("KITDB_TOKEN"),
  access: "readwrite",
});
```

The default access is `readonly`. Production deployments must put the endpoint
behind HTTPS because the token is a Bearer credential.

An ordinary libSQL client can then use the tenant URL and file path:

```javascript
import { createClient } from "@libsql/client";

const client = createClient({
  url: "https://shop.example.com/catalog.kitdb",
  authToken: process.env.KITDB_TOKEN,
});

const result = await client.execute({
  sql: "SELECT sku, price FROM products WHERE status = ? LIMIT ?",
  args: ["active", 20],
});
```

The experimental profile serves Hrana JSON pipelines at `/v2/pipeline` and
`/v3/pipeline` and has been exercised end-to-end with `@libsql/client` 0.17.4.
It accepts bound positional/named values and a fail-closed SQL subset:

- `SELECT` fields or `COUNT(*)`, `WHERE` joined by `AND`, `ORDER BY`, and
  bounded `LIMIT`/`OFFSET`;
- one-row `INSERT`, plus guarded `UPDATE` and `DELETE`; mutation statements
  preserve KitDB schema validation, indexes and constraints;
- bounded `RETURNING`, `sqlite_master`/`sqlite_schema`, and the common
  `table_info`, `index_list`, and `foreign_key_list` pragmas.

The first profile intentionally refuses DDL, joins, subqueries, `OR`, other
aggregates, multiple SQL statements, WebSocket/Protobuf/cursor variants, and
interactive or atomic multi-statement transactions. Unsupported syntax returns
an explicit Hrana error instead of being approximated. Result reads retain the
ORM hard cap of 120 rows. A simpler authenticated, read-only JSON endpoint is
also available at `POST /_db/query`.

The visible storage unit is:

```text
tenant.kitdb       canonical main snapshot
tenant.kitdb.wal   active transaction tail
tenant.kitdb.lock  stable process-writer lock
```

Bytes after the active slot's declared file boundary are abandoned tail and
are ignored on recovery. The next incremental checkpoint truncates that tail
before appending. Temporary compaction files are written beside the database
and never selected by readers.

New databases use format v3. Format-v1 and format-v2 files remain readable and
are upgraded by the next checkpoint, including an explicit checkpoint with no
newer transaction.

At most 32 active segments are allowed. A later checkpoint at that bound
triggers a streaming compaction into one base segment, removes obsolete
mutations and tombstones, and atomically replaces the main file. Normal
checkpoints write only their delta plus bounded metadata; compaction and legacy
migration scan and rewrite the logical database. See `NON_GOALS.md`,
`INVARIANTS.md`, and `FORMAT.md` before building on the package. KitDB remains
experimental and is not production-ready storage.
