# Shopping 13M Dictionary Text Analytics Evidence: 2026-09-04

This report records an opt-in production-data experiment for KitDB KCOL v3
dictionary text. It compares exact results and measured work, but it is not a
same-hardware PostgreSQL shootout or a production p95/p99 claim.

## Dataset And Change

- Independent benchmark copy: 13,773,074 `shopping` rows and 30 wide fields.
- Canonical KROW file: 30,054,427,583 bytes. KROW and WAL formats were not
  rewritten.
- Existing policy: `RANGE(category)` plus a complete `(merchant,id)` index.
- Change under test:

```sql
ALTER TABLE shopping ALTER COLUMN merchant SET ANALYTICS;
```

- Previous numeric/boolean `.analytics`: 2,243,383,847 bytes.
- KCOL v3 with narrow dictionary `merchant`: 2,299,997,650 bytes.
- Added storage: 56,613,803 bytes (about 54.0 MiB or 2.52%).

The explicit analytics refresh captured source transaction 33,796, decoded
13,773,075 rows across both tables, built 1,683 chunks, and published a clean
rewrite with zero obsolete bytes. It wrote 2,300,001,746 bytes in about 40
seconds on the local machine. `PRAGMA analytics_status(shopping)` then reported
`ready`, exact source/current transaction 33,796, 13,773,074 rows, 1,682 table
chunks, chunk version 6, and layout `kcol-v3+chunk-v6-range`.

## Exact Query

```sql
SELECT merchant, COUNT(*) AS products, SUM(price) AS value
FROM shopping
GROUP BY merchant
ORDER BY merchant;
```

PostgreSQL and KitDB returned the same values:

| merchant | products | value |
| --- | ---: | ---: |
| `lazada` | 109,430 | 41,744,802,019 |
| `shopee` | 11,023,195 | 3,700,897,439,849 |
| `tiki` | 2,640,449 | 1,843,697,531,843 |

The count-only shape can use the ordered `(merchant,id)` index. Adding
`SUM(price)` deliberately makes that index non-covering and lets the same query
exercise KROW batch or narrow KCOL without dropping a useful production index.

## Observed KitDB Work

| Path | Observation | Logical rows | Storage read | Cumulative allocation |
| --- | ---: | ---: | ---: | ---: |
| Scalar KROW, one independent process | 188.985 s execution | 13,773,074 | 26,648,913,678 page bytes | not sampled |
| KROW batch, one independent process | 30.592 s execution | 13,773,074 | 26,648,913,678 page bytes | not sampled |
| KROW batch, warm-process 3x benchmark | 53.300 s/op | 13,773,074 | 26,648,913,678 page bytes | 58,027,298,712 B/op |
| KCOL, first independent open | 2.849 s execution | 13,773,074 | 191,715,736 KCOL bytes | not sampled |
| KCOL, warm-process 3x benchmark | 0.633 s/op | 13,773,074 | 191,715,736 KCOL bytes | 2,437,072 B/op |

The formal warm-process samples make KCOL about 84 times faster than KROW batch
for this shape. The independent first-open samples make KCOL about 10.7 times
faster than KROW batch and 66 times faster than scalar KROW. More importantly,
the audited engine read volume falls about 139 times, from 26.65 GB of wide row
pages to 191.7 MB of two narrow column payloads plus headers.

The KROW numbers vary materially with host/device state even though each path
uses one Engine. `B/op` is cumulative allocation, not peak RSS. These results
therefore support the representation decision; they do not define a latency
SLO.

## PostgreSQL Baseline

The read-only source is PostgreSQL 12.15 on a remote host. Preflight reported
an approximately 28.08 GB `shopping` table and 13,777,664 estimated rows. Three
timed executions averaged 37.195 s/op. The client allocated 2,864 B/op, which
does not measure PostgreSQL server memory or I/O.

The remote PostgreSQL result is useful operational context, not an
apples-to-apples engine comparison: server CPU, storage, cache, network,
statistics, and parallel-worker settings differ from the local KitDB machine.
The current PostgreSQL rows nevertheless produced the exact same aggregate
values as the retained KitDB snapshot.

## Reproduce

The benchmarks are opt-in and assert the selected execution path and three
result groups:

```powershell
$env:KITDB_SHOPPING_BENCHMARK='D:\path\shopping-copy.kitdb'

go test ./kitdb/relational -run '^$' `
  -bench '^BenchmarkShoppingProductionAnalytics/group-merchant-kcol-value$' `
  -benchtime=3x -count=1

go test ./kitdb/relational -run '^$' `
  -bench '^BenchmarkShoppingProductionKROWTextAnalytics$' `
  -benchtime=3x -count=1
```

PostgreSQL accepts a read-only URL only through the environment. A case-sensitive
database override avoids changing or printing the stored credential:

```powershell
$env:KITDB_SHOPPING_POSTGRES_BENCHMARK=$env:KITDB_MIGRATE_PG_URL
$env:KITDB_SHOPPING_POSTGRES_DATABASE='KitDB'

go test ./kitdb/migrate/postgres -run '^$' `
  -bench '^BenchmarkShoppingProductionPostgresTextAnalytics$' `
  -benchtime=3x -count=1
```

## Decision

Keep KROW as the single transaction authority and keep text KCOL explicit and
narrow. For a field already covered by an ordered index, the planner should use
that index when it answers the complete query. When an aggregate needs another
column such as `price`, a 54 MiB dictionary projection avoids decoding the 30
GiB canonical row representation. This is stronger evidence for the current
KROW plus derived KCOL architecture than for replacing canonical storage with
PAX or automatically duplicating every text field.
