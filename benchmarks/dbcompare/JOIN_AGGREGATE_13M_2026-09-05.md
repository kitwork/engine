# Standalone composite JOIN + aggregate qualification

Date: 2026-09-05. Windows/amd64, Go 1.26.0, pure-Go standalone binary,
PostgreSQL wire on loopback, SQL read-only. This is development-tree evidence,
not a clean-commit release qualification or a PostgreSQL comparison.

## Real shopping database

`TestShoppingProductionJoinAggregate` uses `KITDB_JOIN_BENCHMARK_DSN`; the DSN
and source identifiers are deliberately absent from this report. No source
DDL, writes, migration, or index rebuild was performed.

- Source: 13,773,074 rows.
- Workload: 100 ordered `shopee` rows, self-joined on `(merchant, id)`, with
  COUNT(*), COUNT(price), SUM, AVG, MIN, MAX and output LIMIT 1.
- Independent reference: client-side aggregation of those 100 source rows.
  Counts agree exactly; floating-point aggregates agree within relative
  tolerance 1e-10 (absolute tolerance 1e-10 near zero).
- First three query executions: 3.237, 3.209, 3.234 ms.
- Repeated run: 3.761, 3.251, 3.322 ms.
- Final binary, including the lookup-coercion guard: 3.558, 3.254, 3.249 ms.
- EXPLAIN ANALYZE: 100 source-index entries, 200 point lookups, 200 rows
  scanned, 100 joined matches, one group, 15,150 accounted peak working bytes.
- Source access: `shopping_merchant_id_idx`; target: complete primary lookup.
- Broad merchant JOIN: rejected at the 10,000-source-row bound after
  433.907 ms (304.166 ms on the final binary), without a partial COUNT result.
  The connection remained usable.

These are selective indexed reads against a large database, not aggregation
over 13 million joined rows. The reference read warms the relevant data;
these timings are neither cold-cache measurements nor p95/p99 estimates.
Accounted executor bytes are not process RSS, total allocations, or page cache.

```sql
SELECT COUNT(*) AS matched, COUNT(b.price) AS priced,
       SUM(b.price) AS total_price, AVG(b.price) AS mean_price,
       MIN(b.price) AS low, MAX(b.price) AS high
FROM shopping a
JOIN shopping b ON a.merchant = b.merchant AND a.id = b.id
WHERE a.merchant = 'shopee' AND a.id >= $1 AND a.id <= $2
LIMIT 1;
```

## Correctness and remaining boundaries

Disposable standalone tests cover composite primary, unique, and secondary
access; partial-prefix residuals; repeated/conflicting ON fields; NULL outer
extension; exact DECIMAL/BIGINT sums; multi-source grouping; HAVING and final
paging; CTE/UNION ALL composition; transaction snapshots, own writes, rollback;
prepared PostgreSQL queries; cancellation and memory/group/input/candidate
ceilings. Exact 10,000-input/20,000-candidate boundaries succeed; exceeding
either yields an error, never a truncated aggregate. Ambiguous output ordering
requires distinct aliases instead of silently choosing a field.

A final correctness review added a red/green regression for lossy JOIN lookup
coercion: NUMERIC scale, TIME precision, and VARCHAR trailing-space truncation
must not make unequal source/target values match. All three tests failed before
the lookup guard and passed after it. This affects old single-field JOINs too.

Verification chronology:

- `kitdb-verify` passed on Windows in 87.755 seconds. The retained report is
  `.artifacts/kitdb-join-aggregate-verify.json`; it correctly marks a dirty
  development tree. This gate predates the final lookup-coercion guard.
- After that guard: full `go test -race ./kitdb/relational ./kitdb/sql ./search`
  passed (83.229, 1.246, 10.579 seconds respectively), followed by the final
  real-shopping wire check above. Focused red/green regression, static analysis
  and the standalone CGO-disabled build also passed.
- Existing search + aggregate wire behavior was rechecked on the preceding
  JOIN build: all 378 matches across three merchants agreed with the reference.
  The final full search/relational race suites include those regression tests.

No public tag, stable version, distribution upload, or clean-commit promotion
was performed. The local test listener remains SQL read-only.

The executor is still bounded indexed nested-loop plus KROW hydration. No
hash/merge join, KCOL JOIN execution, disk spill, SEARCH + JOIN, or unrestricted
full-dataset JOIN is claimed. No durable format changed. Stable 1.0 still needs
same-clean-commit Windows/Linux qualification, the 24-hour deployment canary,
independent recovery/upgrade/rollback evidence and reviewed distribution terms.
