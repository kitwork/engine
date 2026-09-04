# Shopping 13M Fleet Workload: 2026-09-04

This report measures one large read-only KitDB database beside four mutable
disposable KitDB databases through one PostgreSQL-wire query scheduler. It is
the first retained KitDB measurement of query admission, warm projection
residency and noisy-neighbor latency on the 13,773,074-row shopping dataset.

It is not a production SLO, an OS-cold storage trace, or evidence for one
thousand simultaneously hot databases. The operating-system page cache was
not flushed. `reader cold` below means the first execution after opening a new
KitDB Engine in a new Go test process.

## Dataset And Method

- Windows/amd64, Intel i7-11850H, Go 1.26.
- Shopping KROW: 13,773,074 rows, 30 fields, 30,054,427,583 bytes.
- Analytics source transaction: 33,796.
- Search source transaction: 33,793.
- Analytics used the verified `shopping_13m_partition.kitdb.analytics`
  snapshot and asserted the `kcol-batch` execution path.
- Search used the retained exact-watermark directory projection beside
  `shopping_13m_full.kitdb` and asserted a ranked-search plan and 20 results.
- Four disposable neighbor databases each held 4,096 rows. Each had one
  worker issuing 75% composite-primary reads and 25% durable point updates.
- The hot shopping database had two read-only workers.
- One pgwire server admitted at most four queries globally and two per
  authenticated database, with bounded global and per-database queues.
- Each mode ran in a separate process: 7.5 seconds of neighbor-only baseline,
  then 15 seconds with the hot shopping workers enabled.
- Source transaction IDs were checked after the workload and did not change.
- RSS is Windows working set sampled every 25 ms. Heap is Go `HeapAlloc`.
- Latency quantiles are bounded-histogram upper bounds, not interpolated
  values. The finite hot samples make p99 directional rather than an SLO.

The `.analytics` file beside `shopping_13m_full.kitdb` was also checked. The
current reader rejected its chunk manifest, safely fell back to `krow-batch`,
and read 26,648,913,678 KROW bytes in about 28 seconds. It was excluded rather
than mislabeled as KCOL. The verified partition copy completed the same
category query through `kcol-batch` in 89.6 ms in a one-iteration preflight.

## Projection Admission Follow-Up

The standalone `kitdb projections DATABASE` command was subsequently measured
against both 13,773,074-row artifacts. These are single warm-cache observations
including process startup, database open, inspection and JSON output, not a
latency distribution:

| Artifact | Wall time | Analytics result | Search result |
| --- | ---: | --- | --- |
| `shopping_13m_partition.kitdb` | 310.0 ms | ready, 13,773,074 rows, 1,682 chunks | missing |
| `shopping_13m_full.kitdb` | 231.4 ms | stale chunk-v2 layout | missing |

The preflight read canonical catalog/layout metadata plus projection container
and fixed headers; it did not open a KROW row cursor. Opening the stale artifact
with `require-ready` was rejected in 223.6 ms, before the aggregate could enter
the 26.65 GB KROW fallback. `validate` intentionally permits stale/missing
projections because those states have a correct fallback; use `require-ready`
when a service must fail admission rather than accept that fallback cost.

## Reader Warm-Up

| Mode | Open | Reader cold | Direct warm p50/p99 | Heap before open | Heap after warm | RSS before open | RSS after warm |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| KCOL analytics | 194.9 ms | 113.8 ms | 100 / 100 ms | 0.51 MiB | 155.51 MiB | 10.10 MiB | 239.89 MiB |
| BM25 search | 185.3 ms | 1,153.2 ms | 200 / 200 ms | 0.51 MiB | 386.24 MiB | 10.31 MiB | 491.10 MiB |

The search warm set is materially larger than the narrow analytics warm set.
This is direct evidence that `warm: true` cannot be a fleet-wide default:
search readers need an explicit budget and idle eviction even when query
latency benefits from keeping them resident.

## Mixed Results

### KCOL Hot Tenant

| Window | Total qps | Point read p50/p95/p99 | Update p50/p95/p99 | Hot count | Hot p50/p95/p99 | Queue peak | RSS peak |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Baseline | 4,651 | 1 / 2 / 3 ms | 2 / 3 / 4 ms | 0 | - | 0 | 306.86 MiB |
| Mixed | 2,360 | 2 / 4 / 5 ms | 3 / 4 / 6 ms | 94 | 400 / 400 / 400 ms | 2 | 306.86 MiB |

The point-read p99 upper bound moved from 3 to 5 ms, a 1.67x ratio. The mixed
window accumulated 17.67 seconds of admission wait across 35,476 operations;
there were no rejections, wait timeouts or failed statements.

### BM25 Hot Tenant

| Window | Total qps | Point read p50/p95/p99 | Update p50/p95/p99 | Hot count | Hot p50/p95/p99 | Queue peak | RSS peak |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Baseline | 4,189 | 1 / 2 / 3 ms | 2 / 3 / 4 ms | 0 | - | 0 | 749.18 MiB |
| Mixed | 2,197 | 2 / 4 / 5 ms | 3 / 4 / 6 ms | 76 | 400 / 750 / 750 ms | 2 | 749.18 MiB |

The point-read p99 upper bound again moved from 3 to 5 ms. The mixed window
accumulated 18.27 seconds of admission wait across 33,794 operations; there
were no rejections, wait timeouts or failed statements.

## Interpretation

1. The scheduler bounded concurrency exactly as configured. Peak active work
   never exceeded four and a single hot database never received more than two
   execution slots.
2. Neighbor tail latency remained in single-digit milliseconds while 13M-row
   analytics or search ran. The scheduler therefore protects latency better
   than an unbounded goroutine-per-query design.
3. Aggregate neighbor throughput fell by roughly half. This is expected, not
   free isolation: two hot workers consumed roughly half of four global slots.
   Admission weights and workload-specific concurrency remain policy choices.
4. Search residency is the immediate fleet concern. A warmed 13M search
   reader used roughly 491 MiB RSS before neighbor fixtures and the complete
   process peaked near 749 MiB. Thousands of such warm tenants are impossible
   on a small node; cold tenants must retain near-zero process residency.
5. KCOL residency was lower but not free. The warmed analytical reader reached
   roughly 240 MiB RSS before neighbors. Narrower projections, bounded reader
   caches and explicit warm sets remain required.
6. The hot p95/p99 values include both execution and fair queueing behind
   neighbor statements. They should not be compared directly with the earlier
   serial in-process query timings.

The evidence supports the current direction: keep KROW authoritative, keep
KCOL/search derived and rebuildable, and make the node own admission plus warm
residency. It does not support warming every projection or increasing
concurrency merely because Go can spawn more goroutines.

## Reproduce

Run each mode in its own process so its memory baseline is not inherited from
the previous projection:

```powershell
$env:KITDB_SHOPPING_FLEET_DURATION='15s'
$env:KITDB_SHOPPING_BENCHMARK='D:\path\shopping_13m_partition.kitdb'
go test ./kitdb/relational `
  -run '^TestShoppingProductionFleetWorkload/analytics$' -count=1 -v -timeout 5m

Remove-Item Env:KITDB_SHOPPING_BENCHMARK
$env:KITDB_SHOPPING_SEARCH_BENCHMARK='D:\path\shopping_13m_full.kitdb'
go test ./kitdb/relational `
  -run '^TestShoppingProductionFleetWorkload/search$' -count=1 -v -timeout 5m
```

`KITDB_SHOPPING_FLEET_DURATION` accepts 2 seconds through 10 minutes and
defaults to 10 seconds. The neighbor-only baseline lasts half that duration,
with a two-second minimum. Without either artifact variable, the canary skips.

## Next Gate

The packed-search follow-up is now implemented in the standalone engine. Each
Engine defaults to zero retained search-reader bytes, admits a packed reader
only after a conservative capacity inspection, single-flights concurrent first
opens, and falls back to open/query/close when the configured budget is too
small. The PostgreSQL node accounts that reservation with container-directory
bytes and evicts oldest non-warm idle readers under a separate fleet ceiling.
Synthetic 4,096-row measurements and retained tests are documented in
`kitdb/relational/PROJECTIONS.md`.

This does not retroactively change the 13M search result above: that canary uses
the legacy directory projection, and no equivalent 13M packed `.search` artifact
was available for an honest rerun. The next evidence gate is therefore to build
that artifact, record its inspected capacity versus measured RSS, and repeat the
same mixed workload with one, several, and over-budget idle search databases.

Before calling this production-ready, repeat on a controlled host with cold
device/cache methodology, multiple workload mixes, cancellation and queue
overflow, and enough samples for a real SLO-grade p99.
