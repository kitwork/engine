# Shopping 13M Query-Memory Evidence: 2026-09-04

This report measures the independent `kitdb/relational` engine against the
retained shopping artifact. It is evidence for deciding whether buffered SQL
needs disk spill now. It is not a PostgreSQL comparison, a cold-device trace,
or a p95/p99 production claim.

The later [fleet workload](SHOPPING_13M_FLEET_2026-09-04.md) adds bounded
pgwire concurrency, warm RSS and noisy-neighbor p50/p95/p99. Keep its method
and claims separate from the serial measurements in this report.

The later [dictionary text KCOL experiment](TEXT_ANALYTICS_13M_2026-09-04.md)
adds `merchant` as one narrow analytical field and records exact PostgreSQL
result parity plus scalar/KROW-batch/KCOL measurements.

## Dataset And Method

- Windows/amd64, Intel i7-11850H, Go 1.26.
- 13,773,074 `shopping` rows with 30 wide fields.
- Canonical KROW: 30,054,427,583 bytes.
- RANGE(category) analytics projection: 2,243,383,847 bytes.
- Serial benchmark samples use one warm Engine unless stated otherwise.
- `B/op` is cumulative Go allocation per query, not live or peak RSS.
- The source artifact was not migrated or rewritten. Normal read-only
  open/close housekeeping was allowed.

The production benchmark entry points are opt-in:

```powershell
$env:KITDB_SHOPPING_BENCHMARK='D:\path\shopping-copy.kitdb'
go test ./kitdb/relational -run '^$' `
  -bench '^BenchmarkShoppingProduction(Analytics|Rows|Materialization|ConcurrentAnalytics)$' `
  -benchmem

$env:KITDB_SHOPPING_SEARCH_BENCHMARK='D:\path\shopping-search-copy.kitdb'
go test ./kitdb/relational -run '^$' `
  -bench '^BenchmarkShoppingProductionSearch$' -benchmem
```

Every accelerated benchmark asserts its actual execution path. A stale KCOL
projection cannot be reported as KCOL, and concurrent analytics fails if any
operation falls back from `kcol-batch`.

## Analytics

The KCOL rows below use three samples of three iterations after warmup. The
index-only follow-up uses three one-iteration runs, each preceded by the same
execution-backed observation on a newly opened Engine.

| Query | Observed time/op | Cumulative allocation | Audited work |
| --- | ---: | ---: | --- |
| Exact `COUNT(*)` | 0.148-0.187 ms | 63,544 B | 13,773,074 rows from metadata |
| `category = 100636` COUNT/SUM/AVG | 98.9-137.0 ms | 243-249 KiB | 3,121,152 scanned; 10,651,922 skipped |
| `category = 4459` SUM/AVG/MAX | 85.1-94.3 ms | 164-170 KiB | 1,649,664 scanned; 12,123,410 skipped |
| High `id` range COUNT/SUM/AVG | 218.6-240.8 ms | about 151 KiB | 4,450,578 scanned; 9,322,496 skipped |
| GROUP BY integer `category` | 0.867-1.322 s | about 4.86 MiB | 13,773,074 scanned; 5,699 groups |
| GROUP BY text `merchant` from ordered index | 2.520-2.603 s | 122,576-122,992 B | 13,773,074 index entries; 1,678,250,545 page bytes; 0 KROW rows; 3 groups |
| Lazada covering COUNT/MIN/MAX | 90.4-105.7 ms | 12.66 MiB | 109,430 index entries; 13,465,252 page bytes; 0 KROW rows |
| Lazada covering COUNT/SUM/AVG/MIN/MAX | 108.4-120.1 ms | 16.00 MiB | 109,430 index entries; 13,465,252 page bytes; 0 KROW rows |

A separate cold-process `GROUP BY merchant` used the scalar text path. It took
214.722 seconds, read 397,741 KROW pages / 26,648,913,678 bytes, and produced
only three groups. Group cardinality and retained memory were tiny; decoding
wide KROW values was the cost. Disk spill cannot improve this shape.

The implemented `index-only-group` path recognizes a complete, non-partial
secondary index whose leading fields exactly match `GROUP BY`, when output is
limited to those fields plus `COUNT(*)` and there is no predicate or search.
It counts contiguous ordered key runs without fetching a row value. On this
13-segment artifact that is 82.5-85.2 times faster than the measured scalar
path while avoiding a second KCOL copy. A callback-scoped kernel key scan
reuses one checksummed page buffer per immutable segment; before that iterator
work, the same query took 12.44 seconds and allocated 8.82 GB cumulatively.
The scalar sample was a cold process while the index samples follow an
`EXPLAIN ANALYZE` warmup, so 82.5-85.2x is the observed gap rather than a
controlled cold-to-cold claim. The audited KitDB read volume still falls from
26.65 GB of wide KROW pages to 1.68 GB of index pages.

The follow-up `index-only-aggregate` planner considers all ready complete
indexes rather than inheriting the row planner's first choice. It uses equality
prefixes and one contiguous range, then admits execution only if the selected
key covers every WHERE, GROUP BY, and aggregate field. The retained Lazada
query used `merchant = 'lazada' AND id >= 0`; both versions returned exact SQL
results with no row fetch. `COUNT/SUM/AVG/MIN/MAX` includes the exact BIGINT
accumulators, which explains its additional cumulative allocation. These are
three one-iteration warm-process samples, not a latency distribution or a claim
that an index is always preferable to KCOL.

## Selective OLTP

The same-code before/after comparison measures projection-aware KROW decode.
The decoder now builds its tag directory once per query and decodes only fields
used by projection, filtering, and ordering.

| Query | Before | After | Allocation before | Allocation after |
| --- | ---: | ---: | ---: | ---: |
| Composite primary lookup | 0.155-0.187 ms | 0.145-0.172 ms | 88,537 B | 84,928 B |
| Ordered primary page, 100 rows | 1.405-1.673 ms | 0.543-0.588 ms | about 1.75 MiB | 450,232 B |
| Merchant-prefix ordered page, 100 rows | 1.840-1.968 ms | 0.691-0.777 ms | about 1.75 MiB | 463,560 B |

The page paths remain `index-scan`, scan exactly 100 logical rows, and read
five to six main-file pages. The optimization changes neither SQL results nor
the durable KROW format.

## Materialized CTE

Workload:

```sql
WITH sample AS (
  SELECT merchant, id, price
  FROM shopping
  WHERE merchant = 'shopee'
  ORDER BY merchant, id
  LIMIT 10000
)
SELECT COUNT(*), SUM(price) FROM sample;
```

Before projection narrowing, the query failed the 32 MiB admission gate because
the buffered executor retained every field of each source row. Narrowing source
state to output/order fields made it pass. Reusing the projected decoder then
reduced execution from 126.8-163.1 ms to 53.8-55.8 ms and cumulative allocation
from about 195.85 MiB to 60.56 MiB.

`EXPLAIN ANALYZE` reports 10,000 materialized rows, 1,980,225 retained bytes,
and a 4,590,225-byte peak. The peak is 13.7% of the 32 MiB query budget.

## Search

The search run used the retained exact-watermark directory projection beside
the original shopping copy. It did not rebuild or pack the newer `.search`
snapshot container. Three warm iterations per sample observed:

| Query | Observed time/op | Cumulative allocation |
| --- | ---: | ---: |
| `name SEARCH`, top 20 | 33.4-35.3 ms | about 2.11 MiB |
| `* SEARCH`, top 20 | 223.4-308.0 ms | about 6.07 MiB |
| `* SEARCH` plus merchant residual, top 120 | 288.8-348.2 ms | about 8.47 MiB |

A separate process cold/open observation was materially slower. Search index
and reader lifecycle therefore need an explicit warm policy before a service
latency claim. Search candidate/ranking/hydration pressure is independent of
SQL materialization spill.

## Concurrent Analytics

One bounded run distributed eight total category-aggregate operations through
Go's parallel benchmark workers. GOMAXPROCS 1/2/4/8 reported approximately
69.5/83.0/100.2/96.5 ms per operation with 158-169 KiB allocated per operation.
Every operation remained on `kcol-batch`. These are throughput averages over a
small sample, not per-request tail latency. They prove that the Engine's two-scan
admission remains bounded instead of opening one scan per caller.

## Spill Decision

Do not add disk spill yet.

1. The real materialized CTE peaks at 4.59 MiB under a 32 MiB bound.
2. Integer grouping has 5,699 groups and stays within the 16 MiB group-state
   budget.
3. The former 214.7-second outlier has only three groups and no materialization
   pressure. Index-only grouping reduces its count-only form to 2.52-2.60
   seconds. Dictionary KCOL now serves the non-covering `SUM(price)` form in
   0.633 s/op on the warm local benchmark.
4. Existing result/group ceilings already reject unbounded output explicitly.

The measured priority is:

1. Keep extending index-only aggregation only where a covering index preserves
   exact SQL semantics; the equality/range aggregate path is implemented, while
   page-run summaries still require measurement before entering the format.
2. Extend projection-aware decode to every scalar/JOIN path that still decodes
   unused fields.
3. Keep dictionary-encoded text KCOL explicit and narrow; the 13M `merchant`
   experiment is implemented and measured separately.
4. Preserve warm projection handles and measure process/node p50, p95, p99 and
   cancellation under mixed tenants.
5. Add spill only after a valid production query still reaches the 32 MiB
   materialization or 16 MiB group-state gate after projection pushdown. A spill
   design must have per-query temporary quotas, cancellation, cleanup after
   process death, and no role in canonical durability.

The core lesson is to remove unnecessary representation work before adding a
second storage path: spill is a resource escape hatch, not a query optimizer.
