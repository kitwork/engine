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
| `shopping_13m_full.kitdb`, before pack | 231.4 ms | stale chunk-v2 layout | missing |
| `shopping_13m_full.kitdb`, after pack | 261 ms | stale chunk-v2 layout | ready, 13,773,074 documents, 119 segments |

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
| Packed BM25, two Windows handles | 129.8 ms | 956.5 ms | 200 / 200 ms | 0.47 MiB | 340.87 MiB | 10.04 MiB | 482.20 MiB |

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

## Packed Search Follow-Up

The exact legacy search generation was adopted into the immutable
`shopping_13m_full.kitdb.search` file with the standalone `pack-search`
command. The operation completed in 4 minutes 35 seconds and reported:

- source transaction 33,793;
- 13,773,074 documents across 119 immutable segments;
- 10,006,262,527 source bytes and a 9,730,791,378-byte packed file;
- zero canonical KROW rows scanned;
- a sampled working set of at least 768.35 MiB during packing (not an exact
  peak because the process was not continuously sampled).

Preflight reserved 248,404,997 bytes (236.90 MiB) for deterministic packed
reader structures. After the representative query warmed it, measured native
reader residency was 192.39 MiB. Neither number includes the file payload,
operating-system page cache, allocator overhead, or per-query working memory.

### Single-Query Comparison

Five benchmark processes with three timed iterations each produced these
ranges. Both modes used the same canonical database and query limits.

| Workload | Legacy directory | Packed file | Observation |
| --- | ---: | ---: | --- |
| name-only, 20 rows | 27.98-30.20 ms | 27.88-31.31 ms | equivalent in this sample |
| all fields, 20 rows | 187.86-196.44 ms | 185.73-194.99 ms | equivalent in this sample |
| all fields + merchant, 120 rows | 210.60-222.02 ms | 224.45-233.63 ms | packed was 6-11% slower |

This does not justify a blanket claim that packed search is faster. Its first
measured value is bounded ownership and one copyable file; latency still
depends heavily on result hydration and selected fields.

### Bounded Posting Read-Ahead

A CPU profile of the packed 120-row query showed 71.99% of samples in the
Windows positioned-read syscall path. Search was issuing a small `ReadAt` for
each posting header and another for its adjacent payload. KROW hydration was
not the bottleneck: only about 30-40 ms of CPU samples across 20 complete
queries were attributed to row decoding.

The retained reader now gives each active posting iterator one bounded 4 KiB
read-ahead window. It checks cancellation before and after each physical read;
an in-progress operating-system call cannot be preempted, but a canceled
payload is not decoded or published. This is transient query memory and is not
included in persistent reader residency. A projected KROW decoder is also
constructed once per SQL query and materializes only result, residual-filter,
and requested-snippet fields while still validating the complete KROW envelope
and checksum.

Three 20-operation samples on the same artifact and host observed:

| Workload | Before | After 4 KiB read-ahead | Change |
| --- | ---: | ---: | ---: |
| name-only, 20 rows | 27.88-31.31 ms | 11.30-12.17 ms | about 57-64% lower |
| all fields, 20 rows | 185.73-194.99 ms | 74.21-80.31 ms | about 57-62% lower |
| all fields + merchant, 120 rows | 224.45-233.63 ms | 110.25-111.36 ms | about 51-53% lower |

For the 120-row case, allocations fell from 79,810 to 74,016-74,020 per
operation. Allocated bytes increased from about 8.66 MB to 9.02 MB because the
bounded read windows trade transient memory bandwidth for fewer Windows
syscalls. Measurements with 2 KiB used about 8.07 MB but took 126.62-128.82 ms;
8 KiB used about 10.66 MB and took 103.45-107.33 ms. The retained 4 KiB choice
is the measured latency/resource compromise rather than the fastest isolated
setting.

### Concurrent Windows Reads

The first packed implementation shared one `os.File` handle among all 119
section readers. Go 1.26's Windows positioned-read path serializes `ReadAt`
calls per handle, so two admitted hot queries could not read segment payloads
in parallel. Three 15-second mixed runs completed only 36-40 hot queries and
all reported a 1,000 ms p50/p95/p99 histogram bucket.

The retained implementation still owns one physical `.search` file and one
decoded reader/cache, but gives its bounded two-query gate two verified handles
to that file on Windows. Four independent mixed observations after this change
were:

| Run | Neighbor total qps | Hot count | Hot p50/p95/p99 | Neighbor read p99 | RSS peak |
| --- | ---: | ---: | ---: | ---: | ---: |
| 1 | 3,459.58 | 90 | 400 / 400 / 400 ms | 3 ms | 696.99 MiB |
| 2 | 3,079.99 | 80 | 400 / 500 / 500 ms | 3 ms | 700.15 MiB |
| 3 | 3,235.21 | 82 | 400 / 400 / 500 ms | 3 ms | 698.73 MiB |
| 4 | 3,218.24 | 84 | 400 / 400 / 400 ms | 3 ms | 698.12 MiB |

The same-code legacy rerun completed 78 hot queries at 400 / 750 / 750 ms and
peaked at 750.87 MiB RSS. These warm-cache observations suggest that the
packed layout now reaches at least comparable concurrent throughput while
retaining less process working set. They do not prove an SLO or a universal
advantage across hardware and operating systems.

After bounded posting read-ahead, three additional 15-second packed-search
observations completed 103, 266, and 258 hot queries. Their hot p50/p99 buckets
were respectively 300/500 ms, 150/150 ms, and 150/150 ms; RSS peaks were
750.20, 697.84, and 738.43 MiB. The wide range is direct evidence of host load,
GC and page-cache sensitivity, so these samples are not collapsed into one
headline throughput claim.

The synthetic public-pgwire mixed benchmark also exercised eight independent
KitDB files at once, including four retained packed readers. Three 5,000-query
runs reported 7,238-8,044 statements/s, exactly four readers/eight Windows
handles, 9.962 MiB conservative reader capacity, and a six-query admission
peak. A deterministic four-database lifecycle test then limited idle residency
to two owners and proved that the protected warm reader plus newest cold reader
survive while the two older readers are closed.

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

Remove-Item Env:KITDB_SHOPPING_SEARCH_BENCHMARK
$env:KITDB_SHOPPING_PACKED_SEARCH_BENCHMARK='D:\path\shopping_13m_full.kitdb'
go test ./kitdb/relational `
  -run '^TestShoppingProductionFleetWorkload/packed-search$' -count=1 -v -timeout 5m
```

`KITDB_SHOPPING_FLEET_DURATION` accepts 2 seconds through 10 minutes and
defaults to 10 seconds. The neighbor-only baseline lasts half that duration,
with a two-second minimum. Without any artifact variable, the canary skips.

## Next Gate

The packed-search adoption, bounded posting reads, cancellation boundary and
multi-database ownership gate are complete. Each Engine
still defaults to zero retained search-reader bytes, admits a packed reader only
after conservative capacity inspection, single-flights concurrent first opens,
and falls back to open/query/close when the configured budget is too small. The
PostgreSQL node accounts that reservation with container-directory bytes and
evicts oldest non-warm idle readers under a separate fleet ceiling.

The next evidence gate is a Linux comparison where one file handle is not
expected to serialize positioned reads, followed by controlled cold-device
measurements. The profile says multi-field posting traversal, not 120-row KROW
hydration, is now the remaining dominant path; any deeper format or WAND change
must target that evidence and preserve exact ranking.

Before calling this production-ready, repeat on a controlled host with cold
device/cache methodology, multiple workload mixes, cancellation and queue
overflow, and enough samples for a real SLO-grade p99.
