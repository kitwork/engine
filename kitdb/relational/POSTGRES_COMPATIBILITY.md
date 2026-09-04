# PostgreSQL Familiarity, KitDB Ownership

KitDB is an independent Go database. PostgreSQL-compatible SQL, types and wire
metadata are user-facing contracts, not a dependency on PostgreSQL or Kitwork.
The schema registry and durable catalog are authoritative; Kitwork struct()
must adapt to that contract without limiting the standalone value domain.

## Promotion Rule

A name in the parser is not a completed type. A promotion needs admission and
NULL rules, canonical round trips, comparisons/casts, keys and constraints,
expressions/functions, aggregate semantics, wire metadata, and recovery tests.
Vector acceleration is optional, but must return the same result as scalar
execution. Unsupported operations must fail explicitly, not silently lose
digits. Logical type changes must not reinterpret existing durable key bytes.

## Exact PostgreSQL Integer Family

Implemented in this iteration:

- New SMALLINT/INT2, INTEGER/INT/INT4 and BIGINT/INT8 declarations persist as
  `smallint`, `int32` and `bigint`. They enforce signed 16/32/64-bit ranges and
  expose PostgreSQL int2/int4/int8 wire metadata respectively.
- Field-aware signed integer key tag 6 for row identity, composite keys,
  unique constraints, FK lookups and ordered secondary index bounds.
- Exact integer comparisons, including the int/float boundary at 2^53/2^63;
  range overflow fails closed. Integer literals infer int4 and then int8.
- Width-aware pure SQL function parameters/returns and checked +, -, *, %, unary
  minus and ABS; overflow leaves data statements atomic.
- SUM(int2/int4) widens to int8. SUM(int8) and AVG of every exact integer use
  decimal results in scalar/KROW/KCOL/grouped execution. AVG has a documented
  16 fractional digit policy; it is not arbitrary precision division.
- Legacy `integer` keys/catalogs retain their frozen 53-bit contract. The
  Kitwork Number bridge supports int2/int4 exactly and rejects int8 rather than
  silently rounding it.

The regression suite covers every int2/int4 boundary, adjacent values above
2^53, both int64 extrema, unique and FK enforcement, index ranges/order,
transaction rollback, defaults, scalar/batch/columnar agreement, typed sequence
reopen, PG wire parameters/results, hard-exit WAL replay, checkpoint reopen and
a verified backup anchor. Process termination is not evidence against dishonest
fsync or physical power-loss behavior.

## Exact NUMERIC / DECIMAL Milestone

Constrained `NUMERIC(p,s)` and `DECIMAL(p,s)` now persist in Schema IR v4. The
initial bounded profile accepts precision 1..1000 and scale 0..precision. Values
round to the declared scale half away from zero before precision admission;
overflow fails before publication. Unconstrained legacy DECIMAL remains
compatible and canonical.

Numeric literals, scientific notation, parameters, standard CAST, unary and
binary arithmetic, ABS, ROUND, pure SQL functions, predicates, keys,
constraints, MIN/MAX and exact SUM/AVG operate on arbitrary-width integer
coefficients rather than float64. Division and AVG use a documented 16-digit
fractional result ceiling. Grouped decimal aggregate memory is explicitly
bounded. KCOL has no decimal vector format and therefore takes the exact scalar
fallback instead of changing results.

PostgreSQL discovery exposes `numeric_precision`, `numeric_scale` and
`pg_attribute.atttypmod`. Text results restore the declared trailing scale while
KROW remains canonical; binary parameters and results use PostgreSQL's base-10000
NUMERIC format. PostgreSQL import preserves supported modifiers and rejects
extended profiles such as negative scale instead of silently weakening them.

Regression evidence covers positive/negative rounding boundaries, precision
overflow, very large values, exponent literals, exact functions/arithmetic,
grouped aggregates/HAVING, metadata, text/binary wire values, migration mapping,
WAL reopen and the complete KitDB package suite. This is not a claim of every
PostgreSQL NUMERIC coercion or result-typmod rule.

## Exact Temporal Milestone

Schema IR v5 distinguishes `DATE`, `TIME(p)`, `TIMESTAMP(p)`,
`TIMESTAMPTZ(p)` and `INTERVAL`; the legacy relaxed `datetime` kind remains
readable and is not reinterpreted. Temporal precision is explicit and bounded
to 0..6 microsecond digits. KROW and WAL still store one canonical UTF-8 value:
no second temporal representation or physical-format migration was introduced.

`TIMESTAMPTZ` input requires or assumes the standalone session's documented UTC
zone and is stored canonically in UTC. `TIMESTAMP` rejects an explicit offset.
`INTERVAL` retains independent month, day and microsecond components so calendar
arithmetic does not flatten a month into a fixed number of seconds. The bounded
expression profile includes typed literals, casts, clock defaults, date/integer
and temporal/interval arithmetic, `DATE_TRUNC`, and `DATE_PART`. Date, time and
timestamp indexes support ordered equality/range seeks; interval indexes are
rejected until their ordering/equality contract can be made fully PostgreSQL
compatible.

PostgreSQL discovery exposes distinct OIDs, temporal typmods and
`datetime_precision`. Text output follows familiar PostgreSQL spelling, while
binary parameters/results use PostgreSQL's date, time, timestamp and interval
layouts. PostgreSQL migration preserves supported precision and fails closed on
unsupported temporal modifiers. Regression evidence covers canonicalization,
rounding, UTC normalization, arithmetic, planner range seeks, metadata,
text/binary wire values, migration, WAL reopen and overflow boundaries.

This is deliberately not full PostgreSQL calendrical behavior. BC/infinity,
leap seconds, `TIME WITH TIME ZONE`, named/session time-zone databases, DST-aware
calendar arithmetic, every interval input style, temporal aggregates and
dependency-safe temporal `ALTER TYPE` remain outside the promoted profile.
KCOL v2 has no temporal vector encoding, so exact temporal work stays on scalar
KROW or ordered-index paths instead of silently weakening its semantics.

## Exact VARCHAR / CHAR Milestone

Schema IR v6 preserves constrained `VARCHAR(n)` and `CHAR(n)` declarations for
1..10,485,760 Unicode characters. `CHAR` without a modifier authors `CHAR(1)`;
unmodified `VARCHAR` remains unbounded. Assignment rejects an overlength value
unless every excess character is an ASCII space, while an explicit cast
truncates to the requested length. `CHAR(n)` is stored blank-padded, compares
without trailing padding, and retains physical padding for `LIKE`, matching the
bounded PostgreSQL contract.

The same canonical string drives primary, unique and ordered secondary keys,
functions, casts, concatenation, LENGTH, grouping, HAVING and ordering. No text
sidecar or second row representation is introduced. PostgreSQL discovery now
exposes varchar/bpchar OIDs, `character_maximum_length`, and the length-plus-four
typmod in row descriptions and `pg_attribute`; binary parameters/results remain
the PostgreSQL varlena payload. PostgreSQL migration preserves supported
modifiers and fails closed on malformed or out-of-range declarations.

Regression evidence covers multi-byte UTF-8 length, implicit rejection,
space-only truncation, explicit casts, blank padding, comparison versus LIKE,
unique/index access, cross-width CHAR foreign-key normalization, grouped
execution, metadata, binary wire values, migration, WAL reopen and legacy
unbounded schemas. Exact CHAR-to-nonpadding-text foreign keys fail closed because
their equality cannot use one canonical lookup key. Collation, locale-aware ordering,
grapheme-cluster length, pattern indexes and dependency-safe `ALTER TYPE`
rewrites remain outside this milestone.

## Exact UUID Milestone

Schema IR v7 distinguishes newly authored exact UUID fields from legacy UUID
aliases that previously admitted arbitrary text. Exact input accepts
PostgreSQL-compatible upper-case, braces and flexible hyphens, then stores one
lower-case 8-4-4-4-12 value. Every 128-bit UUID is valid regardless of UUID
version; KitDB's omitted-value convenience generator emits UUIDv4.

The canonical value drives casts, typed literals, predicates, primary/unique/
ordered secondary keys, grouping, pure function boundaries and foreign-key
lookups. Equivalent spellings therefore cannot bypass uniqueness or produce
different index keys. Exact UUID-to-KITID or exact-to-legacy field comparisons
fail closed unless the query explicitly casts to text. `LIKE` likewise needs
an explicit text cast.

PostgreSQL discovery exposes `uuid`, OID 2950 and fixed size 16. Text parameters
canonicalize through the same parser; binary parameters/results use the exact
16-byte PostgreSQL layout. Source UUID columns migrate as an exact type. KROW,
WAL and row-key envelopes remain unchanged, and pre-v7 legacy UUID catalogs
keep their old text admission and text wire metadata. Regression evidence
covers alternate input spellings, malformed values, unique and secondary
lookups, FK compatibility, generated values, typed literals/casts, catalog,
text/binary wire, migration and reopen.

## Remaining Work

| Area | Current boundary | Next promotion |
| --- | --- | --- |
| Integer casts/migration | New declarations have exact int2/int4/int8 widths; legacy `integer` remains frozen | Reviewed cross-width ALTER/cast policy and dependency-safe key rebuilds |
| REAL / DOUBLE | Shared float family, finite values only | Explicit width/coercion/NaN/infinity policy |
| NUMERIC(p,s) | Exact bounded profile, p<=1000, non-negative scale, 16-digit division/AVG, PG text/binary wire | Negative scale, special values, complete PG coercion/result-typmod derivation, dependency-safe ALTER |
| Text | Exact VARCHAR(n)/CHAR(n) lengths, Unicode-character admission, CHAR padding/comparison, casts, keys, grouping, catalog typmods and binary wire | Collations, locale-aware ordering, grapheme-cluster policy, pattern indexes and dependency-safe ALTER TYPE |
| Time | Distinct DATE/TIME/TIMESTAMP/TIMESTAMPTZ/INTERVAL, precision 0..6, UTC session, PG text/binary wire, typed arithmetic/functions and ordered scalar indexes | Named/session time zones and DST, BC/infinity/leap seconds, TIMETZ, wider interval grammar/aggregates, dependency-safe ALTER TYPE and reviewed interval indexing |
| UUID | Exact v7 canonical form, typed literals/casts, keys/FKs, migration and native PG text/binary wire; pre-v7 aliases stay relaxed text | UUIDv7 generator, explicit generation functions and dependency-safe legacy-to-exact migration |
| JSON / JSONB | Existing JSON value support | Distinct operators/equality/canonicalization and indexing contract |
| BYTEA / arrays | Existing bytes and generic array family | Wire and typed-array round trips/operators |
| Named enum / INET / CIDR | Partial KitDB-native related kinds | Explicit SQL types, operators and catalog discovery |
| Functions | Catalog-owned pure SQL scalar functions | Wider type signatures and reviewed text/time/JSON/numeric built-ins; dependency tracking |
| Sequences | Durable typed allocator, CACHE 1..4096 leases, scalar session functions, DEFAULT nextval, all SERIAL widths and integer IDENTITY, atomic ownership/drop and autocommit DDL | Wider expressions, ALTER ownership/defaults/cache/type and transactional DDL |

## Sequence Milestone

The standalone implementation now persists sequence definitions in the core
catalog and binary counters in the same database/WAL. No Kitwork runtime,
sidecar database, native library or extra file is required. The explicit first
profile supports SMALLINT/INTEGER/BIGINT bounds, increment/start, CYCLE,
CACHE 1..4096, and OWNED BY NONE.
CREATE/DROP/ALTER RESTART are autocommit-only, like the current table DDL profile.

Direct scalar SELECT projections support nextval, currval, setval and lastval,
including bound parameters and NULL arguments. A PostgreSQL connection owns
its session state; embedded callers use Engine.NewSession(). Describe never
reserves a value. Read-only sessions/transactions reject nextval and setval.
Sequence IDs, not names, key session state so drop/recreate cannot inherit it.

Lease high-watermarks survive row rollback and are acknowledged only after WAL
Sync. Values inside a published lease need no additional counter transaction.
The commit writer checks optimistic row conflicts atomically, excluding only
kernel-owned nextval/setval counter commits. Row/catalog writes, raw counter
writes and ALTER RESTART still invalidate stale writing transactions. The WAL
transaction ID and durable history include every durable lease publication.
Unused lease values are lost on close/crash; restore and replicas resume beyond
the published high-watermark, so gaps grow but duplicate allocation does not.

Tests cover concurrent allocation, grouped conditional-commit conflicts,
int64 boundaries/CYCLE, session isolation, read-only rejection, rollback,
prepared PostgreSQL wire calls, catalog discovery, blocked/failing Sync,
hard-process WAL replay, reopen, backup and restore to a transaction boundary.
These tests do not certify physical power-loss behavior or high-load throughput.

See [sequence usage and limits](README.md#sequences) and the upstream
[PostgreSQL sequence semantics](https://www.postgresql.org/docs/current/functions-sequence.html)
for the non-gapless/session-local model being adopted.

## Identity and Default Milestone

CREATE TABLE now binds DEFAULT nextval('name'), SMALLSERIAL/SERIAL/BIGSERIAL and
SMALLINT/INTEGER/BIGINT GENERATED ALWAYS/BY DEFAULT AS IDENTITY to the same
typed allocator. Bindings use
stable sequence IDs in schema IR version 3; ordinary schemas remain version 2.
Table and owned sequence creation/drop publish atomically. The kernel validates
the final dependency graph, including writes by other frontends. Rename preserves
owner IDs/tags; direct sequence drop and externally referenced owner drop fail.

Omitted values and DEFAULT reserve at execution; explicit NULL is not DEFAULT.
ALWAYS/BY DEFAULT and OVERRIDING SYSTEM/USER VALUE have distinct admission rules.
UPDATE DEFAULT works on non-primary fields. Reservations remain consumed on
constraint failure, savepoint/transaction rollback and conflict retries. Describe
is side-effect free, and read-only writes cannot reserve a value.

Tests cover concurrent INSERT, prepared PostgreSQL INSERT/RETURNING, discovery,
statement/savepoint atomicity, owner rename/drop, failed CREATE without orphan
sequences, hard exit at create/reservation/row/drop publication, replay and backup.
This is neither a throughput benchmark nor physical power-loss certification.

Remaining gates: arbitrary volatile expressions; ALTER defaults/identity/options;
ownership reassignment; transactional DDL; privileges; ALTER CACHE;
pg_get_serial_sequence. Primary-key UPDATE remains unsupported. CACHE 1 requires
one synced commit per generated value, separate from the row commit; explicit
CACHE N amortizes that commit over N values. Identity supports CACHE as its only
inline sequence option; every SERIAL spelling retains CACHE 1.
A past restore or explicit reset can reuse numbers relative to external systems;
no global uniqueness across restore branches is promised.

PL/pgSQL, arbitrary Go callbacks, filesystem/network function access, triggers,
extensions and a full PostgreSQL catalog are not implicitly approved by adding
ordinary functions/types/sequences.
