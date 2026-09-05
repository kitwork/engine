# Search aggregates: standalone KitDB smoke evidence

Date: 2026-09-05. Windows, pure-Go standalone PostgreSQL-wire listener, local
shopping_13m_full.kitdb (13,773,074-row dataset). SQL writes remained disabled;
no projection refresh, migration, or storage-format change was performed.

The JSONL companion records three grouped search executions and an independent
reference obtained by exhausting ranked search rows and aggregating their
prices in the client. All 378 matches were consumed, not a truncated Top-K
sample. Count, priced count, sum, average, minimum, and maximum agree; numeric
reference comparisons allow relative tolerance 1e-10 for floating-point price.
The separate unit suite verifies integer/decimal results exactly above 2^53.

| Merchant | Matches | Sum(price) | Average(price) | Minimum | Maximum |
| --- | ---: | ---: | ---: | ---: | ---: |
| shopee | 259 | 64236747 | 248018.3281853282 | 3000 | 4500000 |
| tiki | 118 | 46014100 | 389950 | 38000 | 4425000 |
| lazada | 1 | 1000 | 1000 | 1000 | 1000 |

Grouped query elapsed times: 78.146 ms on the first request after server
restart, then 35.551 and 29.283 ms. These are individual client-side local
measurements, not p95, throughput, cold-disk, or million-match evidence.
The first request is not an OS-cache-cold experiment.

EXPLAIN ANALYZE with HAVING and LIMIT 2 reports 378 hydrated/matched rows,
3 groups, 378 source point lookups, and 2953 accounted peak working bytes.
That byte figure excludes index readers, caches, decoder transients, Go runtime,
and process RSS. The path is search-aggregate-snapshot: postings plus projected
KROW reads, not KCOL. The single COUNT(*) fast path remains unranked and does
not hydrate rows unless a residual filter needs them.

Validation: full search, relational, SQL parser and standalone CLI package tests;
full search/relational race suites; focused final EXPLAIN/search-wire tests;
go vet and CGO_ENABLED=0 standalone build. Regression tests cover NULL/empty
inputs, exact arithmetic, multi-column grouping, alias HAVING, output pagination,
group/memory budgets, failed/canceled traversals, pinned source snapshots,
update/delete/reopen and fail-closed stale packed projections.

This evidence does not establish stable-release readiness or general aggregate
expression, JOIN, CTE, explicit-transaction, or spill support for SEARCH.
