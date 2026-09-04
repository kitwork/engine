# KitDB Standalone Relational Profile

This package opens one KitDB file directly. It does not require Kitwork, a
Tenant, a VM, an app folder, or a source-declared `struct()`.

## Embedded Go

```go
database, err := relational.Open("shop.kitdb")
if err != nil {
    return err
}
defer database.Close()

_, err = database.Execute(ctx, `
    CREATE TABLE products (
        merchant TEXT NOT NULL,
        id INTEGER NOT NULL,
        title TEXT NOT NULL SEARCHABLE WEIGHT 5,
        description TEXT SEARCHABLE,
        PRIMARY KEY (merchant, id)
    )
`)
if err != nil {
    return err
}

_, err = database.Execute(ctx,
    `INSERT INTO products (merchant, id, title) VALUES ($1, $2, $3)`,
    "shopee", int64(42), "Keyboard",
)
if err != nil {
    return err
}

result, err := database.Execute(ctx,
    `SELECT title FROM products WHERE merchant = $1 AND id = $2`,
    "shopee", int64(42),
)
```

An embedded host that already owns the kernel file lifecycle can borrow the
same handle instead of reopening it:

```go
database, err := relational.Attach(kernelDatabase, relational.Options{})
if err != nil {
    return err
}
defer database.Close() // closes the facade, not kernelDatabase

result, err := database.ExecutePlan(ctx, typedPlan, parameters...)
```

`ExecutePlan` is the direct pure-Go frontend. It accepts a fresh
`kitdb/sql.ParsedStatement`, so a compiler such as Kitwork can skip SQL text,
the lexer, the parser, JSON transport and every wire protocol. SQL execution
parses into the same plan and then enters the same relational implementation;
it is not a second engine. A plan is single-use for concurrent execution because
binding may attach snapshot-local function metadata to its expression nodes.

`Checkpoint`, `Close`, and a later `Open` use the ordinary KitDB durability and
recovery path. `Describe` returns SELECT result metadata without reading rows.

## PostgreSQL Wire

Set a local password and run the direct-file server:

```powershell
$env:KITDB_TOKEN = "local-development-secret"
go run ./cmd/kitdbpg `
  -file ./shop.kitdb `
  -database shop `
  -listen 127.0.0.1:5433 `
  -retain-history
```

Connect with a PostgreSQL client:

```text
postgres://kitdb:local-development-secret@127.0.0.1:5433/shop?sslmode=disable
```

The logical database name may omit `.kitdb`. This listener serves exactly one
file; it has no Kitwork maintenance database or tenant routing.

To serve several independent KitDB files through one process, use standalone
node mode instead:

```powershell
$env:KITDB_TOKEN = "local-development-secret"
go run ./cmd/kitdbpg `
  -root ./databases `
  -maintenance-database kitdb `
  -listen 127.0.0.1:5433 `
  -max-open-databases 64 `
  -max-page-cache-bytes 268435456 `
  -database-page-cache-bytes 1048576
```

Connect first to the read-only virtual maintenance database and inspect the
current discovery snapshot:

```text
postgres://kitdb:local-development-secret@127.0.0.1:5433/kitdb?sslmode=disable
```

```sql
SELECT datname FROM pg_catalog.pg_database ORDER BY datname;
```

Then open a new connection whose startup database is the selected logical name:

```text
postgres://kitdb:local-development-secret@127.0.0.1:5433/shop?sslmode=disable
```

PostgreSQL has no `USE database` command: selecting another KitDB means opening
another connection. A regular immediate child named `shop.kitdb` is exposed as
`shop`; `shop.kitdb` remains an accepted connection alias. New non-hidden
`.kitdb` files appear without a node restart. Discovery is metadata-only and
engines open lazily, share one relational owner per file, and evict idle owners
under the configured handle and page-cache budgets.

The first standalone-node profile intentionally uses one listener credential
for every discovered database, refuses symlinks and nested discovery, and does
not implement SQL database creation or deletion. It is loopback cleartext only.
Exactly one process may own the served files.

## Ranked Search

Searchability is durable Schema IR, not an application-only hint:

```sql
CREATE TABLE products (
    merchant TEXT NOT NULL,
    id INTEGER NOT NULL,
    name TEXT NOT NULL SEARCHABLE WEIGHT 5,
    description TEXT SEARCHABLE WEIGHT 2,
    brand TEXT SEARCHABLE WEIGHT 2,
    PRIMARY KEY (merchant, id)
);

ALTER TABLE products
ALTER COLUMN description SET SEARCHABLE WEIGHT 3;
```

The standalone embedded and PostgreSQL-wire paths execute the same ranked plan:

```sql
SELECT merchant, id, name, _score, _snippet, _cursor
FROM products
WHERE * SEARCH $1 AND merchant = $2
ORDER BY _score DESC
LIMIT 120;

SELECT merchant, id, name, _score, _cursor
FROM products
WHERE * SEARCH $1 AND merchant = $2
ORDER BY _score DESC
LIMIT 120 AFTER $3;
```

Use `field SEARCH value` for one field, `(field, ...) SEARCH value` for an
explicit field set, and `* SEARCH value` for every searchable field. Bare
`SEARCH value` is the all-field compatibility spelling. `LIKE` remains ordinary
row-pattern matching. `_score`, `_snippet`, and `_cursor` are virtual columns.

The search store is an immutable, rebuildable projection beside the KitDB file;
the rowstore remains authoritative. Its durable watermark includes the source
database identity, transaction, boundary checksum, row layout, and search
schema. Retained history allows bounded mutation catch-up after restart. Without
a usable history range, correctness is preserved by rebuilding from a fixed
row snapshot rather than accepting a stale index. Enable `-retain-history`
before production ingest when incremental catch-up is required.

One query defaults to at most 10,000 returned search rows and 50,000 inspected
candidates. Server flags can raise those budgets up to hard ceilings of 100,000
results and 1,000,000 candidates. Residual filters page through ranked hits until
the requested result is proven complete or the candidate budget is exhausted;
budget exhaustion is an error, never silent truncation. `AFTER` cursors are
checksummed and bound to the exact source transaction, index generation, search
schema, query, and residual predicate, so stale or replayed-in-another-query
cursors fail closed.

The initial profile accepts only `_score DESC`, one SEARCH joined to residual
filters by `AND`, and autocommit execution. SEARCH does not yet compose with an
explicit transaction, JOIN, GROUP BY/HAVING, DISTINCT, aggregate projections,
phrase/prefix search-after, or arbitrary ordering.

## Current SQL Contract

The bounded standalone profile supports:

- catalog-owned `CREATE/DROP TABLE`, one single or composite primary key,
  single/composite unique constraints, ordinary secondary indexes, and
  dependency-safe metadata-only `ALTER TABLE` add/drop/rename operations plus
  integer `SET PARTITION BY HASH/RANGE` and `DROP PARTITIONING` policies;
- PostgreSQL-familiar and KitDB-native scalar types, `CHOICE`, defaults,
  not-null, immediate `CHECK`, and immediate foreign keys with
  `RESTRICT`/`NO ACTION` semantics;
- atomic multi-row `INSERT`, expression-based `UPDATE`, guarded `DELETE`, bound
  `$N` or `?` parameters, constraint/index maintenance, statement savepoints,
  `RETURNING`, unknown-tag preservation, and explicit mutation ceilings;
- embedded fixed-snapshot transactions with read-your-writes, rollback and
  optimistic conflict detection; PostgreSQL `BEGIN`/`COMMIT`/`ROLLBACK` maps to
  the same transaction path;
- SQL-precedence predicates with parentheses, `AND`/`OR`/`NOT`, comparisons,
  `IS NULL`, `IN`, `BETWEEN`, `LIKE`, and `ILIKE` using SQL three-valued logic;
- bounded scalar projection and assignment expressions with checked integer
  arithmetic, text concatenation, and `COALESCE`, `NULLIF`, `LOWER`, `UPPER`,
  `TRIM`, `LENGTH`, `ABS`, and `ROUND`;
- catalog-owned pure SQL functions through `CREATE [OR REPLACE] FUNCTION` and
  `DROP FUNCTION`, including scalar SELECT without FROM (see the profile below);
- `DISTINCT`, forward/reverse `ORDER BY`, `LIMIT`, `OFFSET`, streaming
  `COUNT`/`SUM`/`AVG`/`MIN`/`MAX`, multi-field `GROUP BY`, alias-aware `HAVING`,
  bounded equality `INNER`/`LEFT JOIN` chains across at most eight sources, and
  up to 16 exact-type `UNION ALL` branches with one global ordering/page tail;
- non-recursive `WITH` queries and aliased derived tables, materialized on the
  same transaction snapshot under query-wide row, byte, field, count, and
  nesting bounds;
- primary/unique point lookup, ordered secondary-index equality/range scans,
  durable row statistics, `ANALYZE`, planning-only `EXPLAIN`, and bounded
  execution-backed `EXPLAIN ANALYZE`;
- durable weighted searchable fields, ranked single/multi/all-field SEARCH,
  `_score`/`_snippet`/`_cursor`, bounded residual filtering, and exact
  search-after pagination;
- exact canonical `DECIMAL` and constrained `NUMERIC(p,s)` storage, equality,
  uniqueness, ordering, checks, casts, arithmetic, pure SQL functions, and
  `SUM`/`AVG`/`MIN`/`MAX`, without routing exact values through `float64`;
- exact `DATE`, `TIME(p)`, `TIMESTAMP(p)`, `TIMESTAMPTZ(p)` and `INTERVAL`
  values with microsecond precision, UTC normalization, typed arithmetic,
  `DATE_TRUNC`/`DATE_PART`, PostgreSQL wire values and temporal index ranges;
- PostgreSQL protocol discovery through `information_schema` and the bounded
  `pg_database`, `pg_namespace`, `pg_class`, `pg_attribute`, `pg_type`,
  `pg_index`, `pg_constraint`, `pg_tables`, and `pg_indexes` virtual catalogs.

## Bounded Index Pages

An ordered secondary index can seek to a leading equality prefix followed by
one range field instead of hydrating every row in the prefix:

```sql
CREATE INDEX products_merchant_id ON products (merchant, id);

EXPLAIN SELECT id, name, price, merchant
FROM products
WHERE merchant = 7 AND id >= 500000
ORDER BY id
LIMIT 100;
```

The selected index detail includes `equality_prefix=1 range=id
direction=forward order=index`. Both bounds (`<`, `<=`, `>`, `>=`), bound
parameters, strongest intersected bounds and reverse order are supported on
the first non-equality indexed field. This reuses the existing index codec
and active physical generation; no index rebuild or file migration is needed.

Every SQL predicate is still checked against the same transaction snapshot.
`OFFSET` counts matching rows, not raw index entries. Once an ordinary ordered
or unordered page has enough matching rows, execution stops without hydrating
another SQL row; `LIMIT 0` still validates the query but need not read rows.
This is not a promise of zero iterator prefetch or zero additional page I/O.

Range pushdown is conservative: it does not jump over a missing leading index
field, use a partial index, or treat DECIMAL's textual encoding as numeric
order. Unsupported shapes retain residual filtering/scanning. DISTINCT,
aggregates, uncovered sorting and no-LIMIT overflow checks keep their existing
semantics. Large OFFSETs, low-selectivity residual filters and uncached random
row reads can still be expensive; this is not a general cost-based optimizer.

`EXPLAIN ANALYZE` executes its SELECT exactly once on the ordinary fixed
snapshot executor, then discards the data rows and reports the plan plus actual
path, result-row count, and monotonic planning/execution nanoseconds:

```sql
EXPLAIN ANALYZE
SELECT COUNT(*), SUM(duration)
FROM clicks
WHERE created_at >= '2026-08-01';
```

KROW plans report exact logical work observed by that execution: visible index
entries traversed, point-lookup attempts, candidate rows decoded, and rows that
passed the residual predicate. They also report KROW data/index page accesses,
successful page reads/bytes/records decoded, KitDB LRU hits/misses/bypasses,
immutable generation entries consumed, and WAL-overlay entries examined.
Predicate-matched rows are counted before DISTINCT, OFFSET, LIMIT, or final
projection; `result_rows` is the number actually returned.

These storage counters cover the selected KROW executor nodes, not planning
catalog reads, KCOL/search sidecar I/O, or hardware I/O. A `read` means KitDB
successfully issued and decoded one main-file page; the operating system may
have served it from its own page cache. Point lookups use the KitDB LRU. Range
cursors intentionally bypass that cache to avoid evicting hot lookup pages, so
`accessed = hits + misses + bypasses` while a scan can have reads and zero
misses. `generation_entries` includes duplicates, tombstones and two-layer
merge lookahead, and can therefore exceed logical `rows.scanned`.

KROW batch plans report the same kernel page/cache/physical evidence alongside
their decoded batch and row counts. KCOL batch plans instead report
scanned/skipped chunks, blocks and rows answered from exact metadata, groups,
and any all-or-nothing KROW fallback reason. `kcol_directory` reports blocks
and rows rejected from checksummed partition metadata before a block read.
`kcol_io` separately reports successful block-header and requested-column-
payload `ReadAt` calls and bytes. Directory-rejected blocks read neither;
header-rejected or metadata-answered blocks read a header but no payload. These
counts exclude projection container/schema-open reads and cannot distinguish
operating-system cache hits from hardware I/O.

Kernel observation is enabled only by `EXPLAIN ANALYZE`; ordinary SELECT
retains the unobserved KROW cursor fast path. KCOL already produces a scan report
per block, so its I/O fields add no extra read. Embedded callers retain the typed
`Result.Execution`; PostgreSQL clients receive stable `id`, `operation`,
`detail` rows. These timings describe one bounded execution, not a benchmark.
Plain `EXPLAIN` never executes rows.

## Pure SQL Functions

This is a bounded standalone feature, not a PL/pgSQL runtime or trigger engine:

```sql
CREATE FUNCTION normalize_sku(s TEXT)
RETURNS TEXT
LANGUAGE SQL
RETURN upper(trim(s));

SELECT normalize_sku(' ab-42 ') AS sku;

SELECT id, normalize_sku(sku) AS normalized
FROM products
WHERE normalize_sku(sku) = 'AB-42'
ORDER BY normalize_sku(sku)
LIMIT 20;

UPDATE products SET sku = normalize_sku(sku) WHERE id = 42;

SELECT routine_name, data_type
FROM information_schema.routines
WHERE routine_schema = 'public';

DROP FUNCTION normalize_sku(TEXT);
```

Definitions are stored as versioned, checksummed-by-WAL catalog metadata, with
a SHA-256 definition hash. They survive WAL replay, checkpoints and verified
backup, and do not appear in the table list. Each transaction resolves calls
against its own catalog snapshot. Concurrent replacement/drop does not change
its captured function body. DDL itself is autocommit-only, like other standalone
schema changes. A failed data statement rolls back its staged writes.

The first profile has these explicit boundaries:

- At most 1024 definitions per database, 64 KiB per encoded definition, within
  the shared catalog byte budget. Bodies have at most 256 expression nodes and
  depth 24; a statement has at most 64 user-function call sites and a function
  at most 16 named parameters.
- TEXT, legacy INTEGER, full signed BIGINT, DOUBLE PRECISION and BOOLEAN
  signatures. Existing catalog kind `integer` retains its admission range of
  `[-(2^53-1), 2^53-1]`. Newly declared BIGINT/INT8 uses distinct kind `bigint`;
  an old function signature is not silently rewritten by a parser upgrade.
- Pure expressions over arguments, literals, arithmetic/comparisons/booleans,
  concatenation and the reviewed scalar built-ins. No table reads, host Go
  callbacks, filesystem/network access, clock, random, loops, recursion or
  calls to other stored functions. SQL-level nested calls are allowed.
- NULL is passed to the body, not automatically short-circuited for the whole
  function. COALESCE evaluates only through its first non-NULL argument.
- Function text arguments, intermediate results and return values are capped
  at 1 MiB. These are structural/size limits, not a hard CPU-time sandbox or a
  zero-allocation/vectorized execution guarantee.
- One case-insensitive, unqualified name per database, no overloading.
  Replacement must keep parameter names/types and return type unchanged;
  built-in and compatibility names cannot be shadowed.
- Calls work in single-table scalar SELECT, WHERE, ORDER BY, UPDATE assignments
  and DELETE predicates. INSERT expressions, RETURNING expressions, JOIN,
  aggregate/HAVING/SEARCH composition, DEFAULT/CHECK/generated expressions,
  qualified function names, procedural bodies and triggers are not promoted.
- PostgreSQL discovery exposes stored functions through the supported columns
  of `information_schema.routines` and `pg_catalog.pg_proc`. This does not
  claim complete routine introspection or a separate function permission model;
  DDL follows the connection's existing read-only/write authorization.

Catalog function records extend the logical metadata format. Older builds
without the function catalog decoder reject that catalog; do not downgrade a
file containing functions. No main-file/WAL envelope change or extra sidecar
is introduced. Kitwork's older SQL/ORM adapter has not gained a function API.

Verification: `go test ./kitdb/sql ./kitdb/relational ./kitdb -run
'TestSQLFunction|TestParseSQLFunction|TestCatalogFunction' -count=1`.

## Remaining Boundaries

An opt-in [file projection experiment](PROJECTIONS.md) adds one `.analytics`
and one `.search` snapshot next to the source file, with typed RAM batches,
transaction freshness checks and explicit refresh. It is not a change to the
default storage layout or a claim of complete columnar SQL support.

Partition policy changes are catalog-only and do not rewrite KROW during DDL:

```sql
ALTER TABLE clicks SET PARTITION BY RANGE (user_id);
ALTER TABLE clicks DROP PARTITIONING;
```

An existing analytics generation becomes stale and queries fall back to KROW
until an explicit `RefreshAnalytics` atomically publishes summaries for the new
policy. RANGE refreshes additionally order projected rows inside each bounded
8,192-row KROW extent before writing 1,024-row KCOL blocks, which narrows block
statistics without moving rows across extent boundaries. This is analytical
chunk routing and local KCOL clustering, not PostgreSQL child tables, physical
KROW sharding, a global sort, or automatic background repartitioning. HASH does
not yet cluster individual KCOL blocks. A compact optional table-level block
directory lets both strategies reject impossible blocks before reading their
headers. Older or budget-limited projections retain the same answers and use
the existing header-statistics path.

The independent SQL surface can inspect that exact eligibility without
starting a build:

```sql
PRAGMA analytics_status(clicks);
```

It returns one bounded row containing source/current transaction watermarks,
layout and partition identity, generation/chunk/row counts, byte accounting,
the predicted `kcol-batch` or `krow-batch` path, and a path-free reason. The
same statement works through a read-only PostgreSQL connection. `ready` means
the bounded manifest/header contract matches the query snapshot; it is not a
full payload scan or an implicit refresh. See the projection profile for exact
states, verification boundaries and resource limits.

With `BatchAggregates` (KROW) or `ExperimentalProjections` (fresh KCOL), integer,
boolean and explicitly analytical text GROUP BY keys can use bounded typed
batches, including composite keys, NULL groups, numeric aggregates and
post-aggregation HAVING/order/paging. Text policy belongs to standalone Schema
IR, not Kitwork:

```sql
CREATE TABLE visits (
  id BIGINT PRIMARY KEY,
  user_id BIGINT,
  utm TEXT ANALYTICS,
  duration BIGINT
);
```

Kitwork's equivalent field modifier is `utm: text().analytics()`. Numeric and
boolean fields retain their existing projection eligibility. For example, on a
table with integer `user_id` and `duration`:

```sql
SELECT user_id, COUNT(*) AS visits, SUM(duration) AS total
FROM clicks
GROUP BY user_id
HAVING visits > 10
ORDER BY total DESC
LIMIT 50;
```

`ANALYTICS` is a compact KitDB catalog extension; ordinary SELECT/GROUP BY syntax
does not change. Query execution reports the actual batch path
and number of groups. KCOL still needs an explicit refresh and an exact source
snapshot match. Unsupported types/shapes and uncommitted transaction writes
retain scalar/index execution. Group-count and accounted-memory limits fail
explicitly, never truncate groups to satisfy LIMIT. See the projection profile
for exact support and resource limits.

A narrower text-group path can use an existing ordered secondary index without
building KCOL or decoding KROW. `index-only-group` is selected only when the
GROUP BY fields are the leading index fields, the index is complete and
non-partial, there is no predicate/search, and every output is a grouped field
or optional `COUNT(*)`. It streams contiguous key runs, includes
transaction-overlay index mutations, and retains the same group-count, 16 MiB
state, query-memory, HAVING/order/paging, and cancellation boundaries. All
other shapes fall back to the established batch or scalar executor.

`index-only-aggregate` handles the wider covering case. The planner considers
every complete non-partial ordered index, prefers equality prefixes and the
first contiguous range, then chooses the narrowest equally selective covering
key. It is admitted only when that one key contains every field referenced by
WHERE, GROUP BY, and the aggregate outputs. The executor rechecks any complete
residual predicate from decoded key components, preserves NULL semantics and
the existing exact integer aggregate contract, and supports `COUNT`, `SUM`,
`AVG`, `MIN`, and `MAX` without KROW point lookups. Simple top-level AND
conjuncts may bound the scan even when a covered `IN`, OR, function, or other
expression remains authoritative. Missing, partial, or unready coverage falls
back rather than returning an approximate answer.

Within a CTE/derived-table query, operations that retain variable-cardinality
intermediate state share its 32 MiB materialization ceiling. Computed
`ORDER BY`/`DISTINCT`, scalar and batch `GROUP BY`, indexed JOIN, and `UNION ALL`
reserve retained rows, keys, accumulators, intermediate environments, and
branch references incrementally. Bounded Top-N sorting accounts only retained
candidates and replacements, while JOIN releases per-source scratch before
advancing. `EXPLAIN ANALYZE` exposes retained and peak materialization bytes.
These paths do not spill to disk; outside materialization, their existing
documented row/state limits still apply.

Physical-table SELECT derives one required-tag set from its projection,
predicate, equality conditions, and ordering. The binary KROW decoder validates
the complete row envelope, field framing, and checksum, but materializes only
those values; its tag directory is built once per query. Buffered SELECT also
retains only output and ordering fields rather than cloning an entire wide row.
The full decoder remains authoritative for CRUD rewrites so unknown durable tags
are preserved. This changes neither KROW nor WAL format.

Unordered queries can stop early at `LIMIT`; unsupported or over-budget syntax
fails rather than returning a silently truncated or reinterpreted result.

`UNION ALL` composes up to 16 SELECT branches on one transaction snapshot.
Branches must return the same number of exact logical types and type modifiers;
the first branch owns result names. One trailing `ORDER BY` may reference those
names or one-based positions, followed by global `LIMIT`/`OFFSET`. An unordered
bounded query pushes its global row need into branches, while ordered execution
retains at most the configured result budget before sorting. Ranked `SEARCH`,
sequence calls, parenthesized branch-local ordering, duplicate-eliminating
`UNION`, `INTERSECT`, and `EXCEPT` remain outside this first set-operation slice.

Non-recursive common table expressions and derived tables use the familiar
PostgreSQL forms without becoming durable tables:

```sql
WITH expensive AS (
    SELECT merchant, id, title, price
    FROM products
    WHERE price >= $1
), labeled(item_id, label, cost) AS (
    SELECT id, UPPER(title), price FROM expensive
)
SELECT item_id, label, cost
FROM labeled
ORDER BY item_id DESC
LIMIT 20;

SELECT item_id, label
FROM (
    SELECT id AS item_id, title AS label FROM products
) AS selected
WHERE item_id >= $1;
```

Every branch observes the enclosing transaction's one immutable snapshot and
its read-your-writes overlay. CTEs become visible in declaration order and may
reference earlier siblings. Materialization is capped across the whole query
by the configured result-row limit, a 32 MiB accounted-memory ceiling, 16 CTEs,
128 fields per materialized relation, and eight nesting levels. Duplicate
output names require an explicit unique CTE column list. Unordered,
non-distinct row projections and scalar expressions are admitted one row at a
time directly from the snapshot or preceding relation, before allocating the
retained row map. This avoids constructing a second complete `Result.Rows`
copy for the common pipeline shape. Buffered ordering/distinct, scalar or batch
grouping, physical indexed JOIN, and `UNION ALL` all reserve their
variable-cardinality working state before retaining it. The final SELECT over a
CTE/derived relation shares that same query-wide ledger, and transient JOIN
scratch is released per source row. These shapes are memory-bounded but not
spillable or fully streaming.

The retained 13.77-million-row shopping workload and the evidence for keeping
spill out of this stage are recorded in
[`SHOPPING_13M_MEMORY_2026-09-04.md`](../../benchmarks/dbcompare/SHOPPING_13M_MEMORY_2026-09-04.md).

`Describe`, prepared
PostgreSQL statements, `EXPLAIN`, and `EXPLAIN ANALYZE` use the same plan.
Recursive/forward references, `WITH RECURSIVE`, SEARCH materialization, and
JOIN whose source or target is a materialized relation fail explicitly. CTE
bodies may still use supported physical-table JOINs, aggregates, expressions,
and `UNION ALL`.

An indexed JOIN chain may add at most seven tables. Every added table must join
one equality field to an earlier source and must expose that target field as a
primary, unique, or leading ordinary index; execution refuses a repeated target
full scan. One query is capped at 10,000 base-source rows, 20,000 candidate
pairs, 64 projected columns, eight ordering fields, and the configured result
budget. This remains an index nested-loop strategy, not a claim that KitDB has
a cost-based hash/merge join optimizer.

The standalone profile does not claim PostgreSQL parity. Primary-key updates,
savepoints exposed through SQL, deferred constraints, recursive/correlated
subqueries, cascading foreign-key actions, duplicate-eliminating or non-union
set operations, window functions,
non-equality/right/full/cross joins, hash/merge join planning, aggregate
expressions over joins,
COPY, TLS, SCRAM, and a complete PostgreSQL system catalog remain
outside this package's current contract. Some of those capabilities exist in
Kitwork adapters, but they are not standalone guarantees until promoted here.

The PostgreSQL listener is cleartext and loopback-only. Do not expose it to an
untrusted network.

## Format Authority

Standalone SQL and Kitwork `struct()` publish the same Schema IR and use the
same row/key format. `work/kitdb_standalone_compatibility_test.go` verifies both
directions. KROW v2 value IDs are explicit durable constants; changing their
numbers is a file-format break.

### Exact Integer Profiles

New standalone declarations preserve PostgreSQL's signed integer widths:
`SMALLINT`/`INT2` use 16 bits, `INTEGER`/`INT`/`INT4` use 32 bits, and
`BIGINT`/`INT8` use 64 bits. The contract covers primary/composite/unique/
secondary keys, predicates, GROUP BY, MIN/MAX, and pure SQL function
arguments/returns. Ordered keys use the explicit integer scalar tag described
in `kitdb/FORMAT.md`, not float64. Overflowing literals, assignments and checked
arithmetic fail instead of wrapping or being retried as floating-point values.

```sql
CREATE TABLE counters (
  id INTEGER PRIMARY KEY,
  shard SMALLINT,
  amount BIGINT
);
INSERT INTO counters (id, amount)
VALUES (2147483647, 9223372036854775807);
SELECT id, amount FROM counters WHERE id = 2147483647;
SELECT SUM(amount), AVG(amount) FROM counters;
```

`SUM(SMALLINT)` and `SUM(INTEGER)` widen to BIGINT. `SUM(BIGINT)` returns exact
NUMERIC/decimal text, widening its accumulator when int64 addition overflows.
AVG over any exact integer shares that exact sum and rounds to 16 fractional
decimal digits, half away from zero, with canonical trailing-zero removal. This
is not PostgreSQL's complete numeric precision/scale policy. Scalar, KROW batch
and KCOL batch share the accumulator. NULLs are ignored, and empty/all-NULL
SUM/AVG return NULL. The legacy durable kind `integer` retains its prior result
contract.

Use Go int64 (or a decimal integer string) and PostgreSQL int8 parameters to
preserve all input digits. A float64 input may already have lost digits before
the engine sees it. Accepting BIGINT does not promote every numeric operator to
complete PostgreSQL parity.

### Exact NUMERIC Profile

`DECIMAL`/`NUMERIC` without modifiers retains the existing unbounded canonical
text contract. `NUMERIC(p,s)` and `DECIMAL(p,s)` persist precision and scale in
Schema IR v4, with `1 <= p <= 1000` and `0 <= s <= p`. Assignment rounds to the
declared scale half away from zero and then rejects precision overflow. KROW
stores one canonical value (`1.20` is stored as `1.2`); PostgreSQL text and
binary NUMERIC results apply the declared scale and typmod at the wire boundary.

```sql
CREATE TABLE ledger (
  id BIGINT PRIMARY KEY,
  amount NUMERIC(18,2) NOT NULL
);

INSERT INTO ledger (id, amount) VALUES (1, 1.235);
SELECT amount, amount + 0.10, ROUND(amount / 3, 4) FROM ledger;
SELECT SUM(amount), AVG(amount) FROM ledger;
```

Numeric literals, exponent notation, parameters, standard `CAST(... AS
NUMERIC(p,s))`, unary arithmetic, `+`, `-`, `*`, `/`, `%`, `ABS`, `ROUND`, pure
SQL function arguments/returns and aggregates use `math/big.Int` coefficients.
Division and AVG round to at most 16 fractional digits, half away from zero.
Scalar and row-batch execution share this contract; KCOL deliberately falls
back to the exact scalar path because no decimal vector encoding is published.
NaN/infinity, negative scale, PostgreSQL's full result-typmod derivation rules,
and dependency-safe `ALTER TYPE` rewrites remain outside this profile.

Kitwork's current fluent `struct()` adapter still publishes Schema IR v2 and an
unconstrained decimal/legacy datetime/text contract. It fails closed when asked
to own standalone Schema IR v4 constrained NUMERIC, v5 exact-temporal, v6
constrained-character, or v7 exact-UUID tables;
standalone SQL and PostgreSQL wire are authoritative until that adapter
preserves the same modifiers and value semantics.

Legacy catalogs remain unchanged, including older SQL integer declarations that
persisted as `integer`. There is no automatic key rewrite or implicit type
migration. The Kitwork VM adapter reads the exact smallint/int32 key format, but
explicitly refuses `bigint` until it has an exact int64 value bridge rather than
rounding it to a JS Number. All three widths are available in standalone
embedded SQL and its PostgreSQL listener.

See [PostgreSQL compatibility work](POSTGRES_COMPATIBILITY.md) for remaining
types, functions and sequences. No all-types/production-parity claim is made.

### Exact Temporal Profile

New SQL declarations preserve five distinct temporal meanings in Schema IR v5:

```sql
CREATE TABLE events (
  id BIGINT PRIMARY KEY,
  on_day DATE NOT NULL DEFAULT CURRENT_DATE,
  at_time TIME(3) NOT NULL DEFAULT CURRENT_TIME,
  local_at TIMESTAMP(6) NOT NULL DEFAULT LOCALTIMESTAMP,
  occurred_at TIMESTAMPTZ(6) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  elapsed INTERVAL NOT NULL
);

CREATE INDEX events_occurred_idx ON events (occurred_at, id);

SELECT DATE_TRUNC('month', occurred_at), DATE_PART('hour', occurred_at)
FROM events
WHERE occurred_at >= TIMESTAMPTZ '2026-08-01T00:00:00+07:00'
ORDER BY occurred_at, id;
```

Precision is 0..6 and rounds half up to the declared microsecond boundary.
`TIMESTAMP` is zone-less and rejects offsets. `TIMESTAMPTZ` is normalized to UTC;
the bounded standalone session currently uses UTC when an input has no offset.
`INTERVAL` keeps month, day and microsecond components separate. Date plus an
integer returns a date, date/timestamp plus an interval follows calendar-aware
arithmetic, and subtracting compatible timestamps returns an interval.

KROW/WAL continue to contain a single canonical string. Exact execution values
exist only while evaluating an expression; PostgreSQL text/binary forms are wire
projections. DATE/TIME/TIMESTAMP/TIMESTAMPTZ may participate in ordered indexes,
including typed-literal range seeks. INTERVAL primary, unique and secondary keys
fail closed because PostgreSQL interval comparison semantics do not match a raw
canonical-string key order.

This profile does not claim BC/infinity, leap seconds, `TIME WITH TIME ZONE`, a
named time-zone/DST database, all PostgreSQL interval spellings, temporal
aggregates or dependency-safe temporal `ALTER TYPE` rewrites. KCOL v3 adds
explicit dictionary text beside integer, float and boolean vectors, but it does
not reinterpret canonical temporal strings as time vectors. Temporal filtering,
functions and arithmetic therefore use the exact scalar/KROW or ordered-index
paths.

### Exact VARCHAR and CHAR Profile

New constrained character declarations persist their Unicode-character length
in Schema IR v6:

```sql
CREATE TABLE labels (
  id BIGINT PRIMARY KEY,
  slug VARCHAR(64) NOT NULL UNIQUE,
  country_code CHAR(2) NOT NULL
);

INSERT INTO labels VALUES (1, 'ca-phe', 'VN');
SELECT slug, LENGTH(country_code) FROM labels WHERE country_code = 'VN';
```

`VARCHAR(n)` keeps trailing spaces but rejects more than n characters.
`CHAR(n)` stores a value padded with ASCII spaces to exactly n characters;
ordinary comparisons ignore that trailing padding, while `LIKE` does not.
Length counts Unicode code points, not UTF-8 bytes. Assignment permits excess
characters only when all of them are spaces, in which case they are truncated.
An explicit `CAST(... AS VARCHAR(n)|CHAR(n))` truncates by definition. `CHAR`
without n means `CHAR(1)` and unconstrained `VARCHAR` remains available.

KROW and WAL still contain one canonical UTF-8 value. Primary, unique and
secondary indexes use that value, and grouped/HAVING execution retains the
modifier. PostgreSQL clients see varchar OID 1043 or bpchar OID 1042 plus the
standard n+4 typmod and `information_schema.columns.character_maximum_length`.
Foreign keys may connect exact `CHAR(n)` fields with different n because lookup
values are normalized to the destination width in both directions. Mixing an
exact CHAR field with non-padding TEXT/VARCHAR in one foreign key fails closed;
its trailing-space equality cannot be represented by one deterministic lookup
key without a separate operator-class index.
This profile does not claim collations, locale-aware ordering, grapheme-cluster
limits, pattern operator classes, or dependency-safe character `ALTER TYPE`.

### Exact UUID Profile

New standalone UUID declarations persist the exact profile in Schema IR v7:

```sql
CREATE TABLE sessions (
  id UUID PRIMARY KEY,
  account_id UUID NOT NULL,
  token UUID NOT NULL UNIQUE
);

INSERT INTO sessions (id, account_id, token)
VALUES ('A0EEBC999C0B4EF8BB6D6BB9BD380A11',
        '{01234567-89ab-cdef-0123-456789abcdef}',
        UUID 'ffffffff-ffff-ffff-ffff-ffffffffffff');
```

Input accepts PostgreSQL's documented upper-case, braced and flexible-hyphen
spellings and always becomes lower-case 8-4-4-4-12 text. No UUID version is
rejected; an omitted UUID value continues to use KitDB's UUIDv4 convenience
generator. `CAST(... AS UUID)`, typed UUID literals, predicates, ordered keys,
unique constraints, grouping and pure function arguments/returns all share the
same canonicalizer. `LIKE` requires an explicit `CAST(... AS TEXT)`.

KROW/WAL still store one canonical string. PostgreSQL discovery exposes native
UUID OID 2950, fixed size 16 and the exact 16-byte binary parameter/result
format. PostgreSQL migration maps source UUID columns exactly. Foreign keys and
field-to-field joins do not mix exact UUID with KITID or legacy UUID text
without an explicit cast, because those values do not share one key contract.
Pre-v7 UUID fields remain relaxed text and continue to advertise text metadata;
there is no implicit rewrite of old values or indexes.

## Sequences

Standalone embedded SQL and PostgreSQL wire share durable typed sequences:

```sql
CREATE SEQUENCE order_ids AS BIGINT START WITH 1 INCREMENT BY 1 CACHE 32;
SELECT nextval('order_ids');
SELECT currval('order_ids'), lastval();

BEGIN;
SELECT nextval('order_ids');
ROLLBACK;
SELECT nextval('order_ids'); -- 3: rollback does not reclaim 2

SELECT setval('order_ids', 100, false); -- next nextval returns 100
ALTER SEQUENCE order_ids RESTART WITH 200;
SELECT nextval('order_ids'); -- 200
DROP SEQUENCE order_ids;
```

Use a dedicated connection for session-local currval/lastval, not an arbitrary
connection from a client pool. Embedded Go callers use `engine.NewSession()`
and `session.BeginTransaction(ctx, options)` to preserve state across calls and
rollback. `engine.Execute()` is intentionally stateless across calls.
Obtain an ID with scalar SELECT and pass the returned int64 as an INSERT
parameter, or declare a sequence-backed default as below. Allocating a number
never inserts a row by itself.

The current profile is explicit rather than full PostgreSQL equivalence:

- CREATE supports `AS SMALLINT`, `AS INTEGER` and `AS BIGINT`; omitted `AS`
  defaults to BIGINT. Nonzero INCREMENT, START, MINVALUE/MAXVALUE,
  NO MINVALUE/MAXVALUE, CYCLE/NO CYCLE, CACHE 1..4096 and OWNED BY NONE are
  supported. Bounds and arithmetic are checked against the declared width.
  There are at most 1024
  definitions per database and 1024 remembered sequence identities per session.
- DROP supports IF EXISTS and RESTRICT. ALTER supports IF EXISTS and RESTART
  [WITH value]. All sequence DDL requires autocommit. RESTART is a data-conflict
  boundary but does not change the definition's START value or session currval.
- Functions currently require direct scalar SELECT projections, without FROM,
  nesting, other expressions or clauses. Arguments are literals/parameters.
  Multiple calls execute left to right; runtime failure may leave earlier
  reservations consumed. NULL arguments yield NULL without reserving.
- nextval/setval commit independently, so rollback can leave gaps. setval(false)
  does not change currval; setval(true) does. lastval follows the most recently
  used nextval sequence. State is not persisted across connection lifetimes.
- Describe and catalog discovery do not allocate IDs. Discovery includes
  information_schema.sequences, pg_sequence and pg_class relkind='S'. This is
  bounded metadata compatibility, not the complete PostgreSQL catalogs/roles.
- Names are exact KitDB catalog names, optionally qualified with public. There
  is no general search_path, regclass/OID argument or PostgreSQL case-folding
  promise. Read-only authentication and read-only transactions reject writes.
- CACHE 1 syncs every value. CACHE N first syncs a durable high-watermark, then
  serves the remaining values from one O(1) in-memory lease under the database
  allocator lock. Closing/crashing discards unserved values; reopen, backup,
  replica and PITR resume after the high-watermark, never inside the old lease.
  Errors with uncertain durability require reopen/recovery, not blind retries.
- General INSERT expressions, privileges per sequence, ALTER bounds/type and
  ALTER CACHE are not implemented. Unsupported syntax fails explicitly. Pure SQL
  functions cannot call these stateful functions or shadow their reserved names.

Sequence definitions and counters are included in the normal WAL/checkpoint,
backup and transaction-time restore boundary. A clone/restore, explicit reset
or CYCLE can reuse numbers relative to another branch or external system. A
sequence is not a globally unique or gapless identity service.

### Sequence-Backed Defaults and Identity

```sql
CREATE TABLE orders (
  id INTEGER GENERATED ALWAYS AS IDENTITY (CACHE 32) PRIMARY KEY,
  title TEXT NOT NULL
);
INSERT INTO orders(title) VALUES ('First order') RETURNING id;
INSERT INTO orders(id, title) VALUES (DEFAULT, 'Second order') RETURNING id;
SELECT currval('orders_id_seq');

-- Explicit imports are permitted only with this override for ALWAYS identity.
INSERT INTO orders(id, title) OVERRIDING SYSTEM VALUE VALUES (1000, 'Imported');

CREATE TABLE tickets (id SERIAL PRIMARY KEY, title TEXT);
INSERT INTO tickets DEFAULT VALUES RETURNING id;

CREATE SEQUENCE shared_ids START 500;
CREATE TABLE events (id BIGINT PRIMARY KEY DEFAULT nextval('shared_ids'));
INSERT INTO events DEFAULT VALUES RETURNING id;
```

- SMALLSERIAL/SERIAL2, SERIAL/SERIAL4 and BIGSERIAL/SERIAL8 use SMALLINT,
  INTEGER and BIGINT allocators respectively. IDENTITY supports those same three
  field widths. Generated bounds are 1 through the type maximum. SERIAL and
  identity default to CACHE 1; identity accepts an explicit `(CACHE 1..4096)`.
  Generated columns are NOT NULL, not implicitly UNIQUE; declare a primary key
  or unique constraint when required. Explicit NULL is not DEFAULT.
- ALWAYS rejects explicit values unless OVERRIDING SYSTEM VALUE is present.
  BY DEFAULT and BIGSERIAL accept explicit values. OVERRIDING USER VALUE ignores
  supplied identity values and allocates fresh ones; it does not override an
  ordinary default or BIGSERIAL. Explicit values never advance the counter.
- DEFAULT nextval('name') binds an existing sequence by stable ID at CREATE.
  Field and sequence must have the same exact integer width. Dropping/recreating
  a name cannot redirect that binding. Only this literal-name default is
  supported, not arbitrary volatile expressions or VALUES(nextval(...)).
  DEFAULT also works for literal defaults.
- UPDATE non-primary fields SET field=DEFAULT evaluates a fresh default per
  matching row. ALWAYS identity rejects other assignments. Existing primary-key
  UPDATE restrictions remain, including SET primary_identity=DEFAULT.
- Table and automatically owned sequences publish atomically. Names default to
  table_column_seq with bounded collision suffixes. Table/column rename retains
  ownership by stable struct ID/field tag and keeps the original sequence name.
  Dropping an owner removes its sequence atomically. A referenced sequence cannot
  be dropped directly; external defaults prevent dropping its owner until those
  references are removed. An ordinary DEFAULT does not own its sequence.
- Allocated values survive statement failure, savepoint rollback, transaction
  rollback and automatic conflict retries; none promises gapless numbering.
  A retry may consume additional IDs. Session currval/lastval reflects successful
  reservations even when their row statement fails. Describe does not allocate.
- Each lease has one synced WAL commit, followed separately by row commits.
  CACHE 1 therefore remains the durability-first default; larger explicit
  caches trade bounded crash gaps for fewer Syncs, without risking duplicates.
- Schema IR version 3 gates these bindings. Ordinary new tables keep version 2;
  existing file layouts and schema hashes are not rewritten. Old SQL readers
  reject version 3 instead of ignoring its default/ownership semantics.

Identity options other than CACHE, ALTER ADD/SET/DROP IDENTITY or sequence
defaults, ownership reassignment, DROP CASCADE and pg_get_serial_sequence are
not implemented. The Kitwork VM bridge exposes smallint/int32 fields but not
BIGINT-backed fields. The standalone embedded API and PostgreSQL listener expose
all widths, including information_schema.columns identity fields, pg_attribute
identity flags, pg_sequence and sequence default discovery.

The kernel benchmark `BenchmarkSequenceCache` is intentionally allocator-only.
On the development Windows machine (256 calls, three samples), CACHE 1 measured
336-345 us/op and 1 sync/op; CACHE 32 measured 14-18 us/op and 0.03125 sync/op;
CACHE 128 measured 4.9-6.7 us/op and 0.007812 sync/op. These numbers demonstrate
the allocator's Sync reduction, not a hardware guarantee. The corresponding
128-row `BenchmarkIdentityInsertCache` measured 776-830 us and 2 syncs/row for
CACHE 1, 445-457 us and 1.031 syncs/row for CACHE 32, and 431-493 us and 1.008
syncs/row for CACHE 128. Row durability therefore becomes the remaining floor;
CACHE 32 is the balanced explicit throughput profile in this measurement, while
the non-surprising default remains CACHE 1.
