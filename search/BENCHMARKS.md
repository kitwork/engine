# Search benchmark record

These measurements are reproducible engineering snapshots, not latency
guarantees. They were collected on 2026-08-21 on Windows/amd64 with an Intel
Core i7-11850H. The scale fixture uses Vietnamese product text with an average
of 63 analyzed tokens per document and a Top-20 query limit.

## Ten million documents

The index contains 10,000,000 documents in 100 immutable segments of 100,000
documents each. Its measured size is 2,304.57 MiB.

The first run had block-level pruning but still visited every segment. The
second run added term-level MaxScore rejection against the index-wide Top-K
heap, allowing a complete non-competitive segment to be skipped before loading
norms or posting iterators.

| Query | Block-only p50 | Block-only p95 | Block + segment p50 | Block + segment p95 |
| --- | ---: | ---: | ---: | ---: |
| Exact SKU | 1.166 ms | 1.271 ms | 0.984 ms | 1.102 ms |
| One common term | 83.445 ms | 108.590 ms | 1.744 ms | 1.948 ms |
| Two terms | 121.149 ms | 138.685 ms | 1.835 ms | 2.381 ms |
| Four terms | 78.820 ms | 91.727 ms | 2.225 ms | 3.614 ms |
| Vietnamese unaccented | 90.287 ms | 98.384 ms | 3.688 ms | 4.014 ms |

The final run built at 13,826 documents/second in 12m03s. Peak build RSS was
1,400.88 MiB and peak Go heap was 1,294.56 MiB. The 1,269.70 MiB RSS observed
during queries is whole-process resident memory immediately after the bulk
build, including retained Go arenas and filesystem cache; it is not per-query
memory. Measured allocations were 16.69-61.54 KiB per query.

```text
go test -tags scale ./search -run '^TestIndexScale/10000000$' -v -timeout 30m -args -kitwork-segment-sizes=10000000 -kitwork-segment-samples=5 -kitwork-index-segment-documents=100000
```

## One million documents

With 100 segments of 10,000 documents, the index measured 230.98 MiB, peak
build RSS was 171.72 MiB, and the highest query p95 was 4.074 ms. This topology
isolates the 100-segment fan-out used by the ten-million-document run while
keeping the fixture quick enough for more frequent scale checks.

```text
go test -tags scale ./search -run '^TestIndexScale/1000000$' -v -timeout 30m -args -kitwork-segment-sizes=1000000 -kitwork-segment-samples=5 -kitwork-index-segment-documents=10000
```

## Streaming replacement memory

Measured on 2026-08-22, this compares the ordinary append batch (`Add` then
`Commit`) with `BeginReplacement` on the same 63-token product fixture. Both
runs produce byte-identical index sizes and use the same segment topology.
Replacement does not retain the ordinary batch's global identifier map; commit
instead performs a buffered k-way merge over the sorted `.ki` sidecars.

| Documents | Segments | Path | Ingest/build | Commit | Peak Go heap | Peak RSS | Index |
| ---: | ---: | --- | ---: | ---: | ---: | ---: | ---: |
| 100,000 | 20 | Add + Commit | 3.525 s | included | 19.65 MiB | 68.54 MiB | 23.15 MiB |
| 100,000 | 20 | Streaming replacement | 3.260 s | 112 ms | 11.37 MiB | 58.73 MiB | 23.15 MiB |
| 1,000,000 | 100 | Add + Commit | 40.336 s | included | 149.17 MiB | 203.00 MiB | 230.98 MiB |
| 1,000,000 | 100 | Streaming replacement | 43.380 s | 487 ms | 21.13 MiB | 69.52 MiB | 230.98 MiB |

At one million documents, streaming replacement reduced peak Go heap by about
86% and peak RSS by about 66%. Total build time increased by about 9% in that
run because duplicate validation rereads the 20-byte-per-document identifier
sidecars. The important scaling result is that the replacement's global ID
tracking stayed empty: peak heap remained tied to the 10,000-document builder,
100 fixed 32 KiB merge buffers, and normal runtime/file metadata rather than a
one-million-entry map.

```text
go test -tags scale ./search -run '^TestReplacementScale$' -v -count=1 -timeout 10m -args -kitwork-segment-sizes=1000000 -kitwork-index-segment-documents=10000

go test -tags scale ./search -run '^TestIndexScale$' -v -count=1 -timeout 10m -args -kitwork-segment-sizes=1000000 -kitwork-index-segment-documents=10000 -kitwork-segment-samples=5
```

### Manager streaming overhead

An adjacent 100,000-document A/B run used the same 20-by-5,000 segment
topology. The managed path includes document ownership cloning, queue and byte
admission, one writer-actor handoff per document, lifecycle counters, and
automatic snapshot publication.

| Path | Ingest | Throughput | Commit | Peak Go heap | Peak RSS | Allocated |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Low-level `Replacement` | 3.373 s | 29,644 docs/s | 88 ms | 11.34 MiB | 58.67 MiB | 1.21 GiB |
| `Manager` `ManagedReplacement` | 3.743 s | 26,716 docs/s | 100 ms | 11.78 MiB | 59.14 MiB | 1.35 GiB |

The ownership layer cost about 9.9% ingest throughput and 11.6% cumulative
allocation in this run, while adding only 0.44 MiB peak Go heap and 0.47 MiB
peak RSS. This keeps the safety layer visible rather than claiming it is free,
but its resident-memory shape remains tied to the bounded segment builder.

```text
go test -tags scale ./search -run '^TestManagedReplacementScale$/100000$' -v -count=1 -timeout 10m -args -kitwork-segment-sizes=100000 -kitwork-index-segment-documents=5000

go test -tags scale ./search -run '^TestReplacementScale$/100000$' -v -count=1 -timeout 10m -args -kitwork-segment-sizes=100000 -kitwork-index-segment-documents=5000
```

## Multi-field exact AND

Measured on 2026-08-22 with 100,000 documents in ten 10,000-document
segments. `title` and `body` use separate BM25 norms and boosts. The common
query matches one term in each field for every document; the selective query
adds a body term present in 1% of documents.

"Cold DF" clears only the bounded exact union-frequency cache before each
operation. Segment files and the operating-system page cache remain warm, so
this is not a cold-disk claim. "Warm DF" represents repeated terms on one
immutable snapshot.

| Query | DF state | Latency | Allocated | Allocations |
| --- | --- | ---: | ---: | ---: |
| `ao cotton` | Warm | 0.206-0.211 ms | 98.59 KiB | 246 |
| `ao cotton` | Cold | 10.945-11.327 ms | 441.83-442.28 KiB | 1,866-1,867 |
| `ao limited` | Warm | 0.305-0.353 ms | 99.02 KiB | 260 |
| `ao limited` | Cold | 5.685-5.729 ms | 417.88 KiB | 1,100 |

The index was 8.027 MiB and built at 82,046 documents/second in this run.
Before bounded union-frequency caching and multi-field MaxScore early exit, the
same benchmark measured 24-33 ms for the common query and 10-11 ms for the
selective query. A separate single-field four-term fixture measured
0.548-0.657 ms, but it is not an apples-to-apples query comparison.

```text
go test ./search -run '^$' -bench '^BenchmarkIndexMultiFieldSearch$' -benchtime=5x -count=3
```

## Multi-tenant concurrent load

This service-level fixture contains 100,000 products split evenly across eight
tenant indexes. Each subtest receives an independent clone of the same 23.08
MiB seed, starts 20 background adds/second in aggregate, and enables automatic
compaction plus generation-aware garbage collection. Search traffic is an even
mix of exact SKU, one-term, two-term, four-term, and unaccented Vietnamese
queries. The host used `GOMAXPROCS=16`; the default therefore admitted 16 active
searches globally and eight per tenant.

Latency percentiles below are fixed-histogram upper bounds. They include
manager admission and are deliberately less precise than the single-query
benchmarks above.

| Clients | Throughput | Queue p95 | Execution p95 | Total p95 | Max waiting | Errors / overloads | Allocated/search |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 100 | 5,383 qps | <= 50 ms | <= 10 ms | <= 50 ms | 93 | 0 / 0 | 23.06 KiB |
| 1,000 | 8,729 qps | <= 250 ms | <= 5 ms | <= 250 ms | 994 | 0 / 0 | 22.42 KiB |

Both runs completed 64 background writes while searches were active. Peak
whole-process RSS was 74.76 MiB for 100 clients and 73.72 MiB for 1,000 clients.
Every tenant made progress and final document counts matched committed writes.

An adjacent A/B run used one independent seed clone per admission setting with
1,000 clients and the same write/maintenance load:

| Global active slots | Throughput | Execution p95 | Total p95 | Go CPU cores used |
| ---: | ---: | ---: | ---: | ---: |
| 16 | 12,175 qps | <= 5 ms | <= 250 ms | 11.75 |
| 32 | 8,188 qps | <= 25 ms | <= 250 ms | 13.36 |
| 64 | 6,179 qps | <= 50 ms | <= 500 ms | 13.22 |

This is why the manager default follows `GOMAXPROCS` rather than multiplying it
by four. More active goroutines increased CPU contention and tail latency on
this workload. Hosts with cold storage or materially different queries should
rerun the matrix and override `MaxConcurrentSearches` rather than treating 16
as a universal constant.

```text
go test -tags scale ./search -run '^TestManagerConcurrentLoad$' -v -count=1 -timeout 15m -args -kitwork-manager-load-tenants=8 -kitwork-manager-load-documents-per-tenant=12500 '-kitwork-manager-load-concurrency=100,1000' -kitwork-manager-load-search-slots=0 -kitwork-manager-load-duration=3s -kitwork-manager-load-writes-per-second=20

go test -tags scale ./search -run '^TestManagerConcurrentLoad$' -v -count=1 -timeout 15m -args -kitwork-manager-load-tenants=8 -kitwork-manager-load-documents-per-tenant=12500 -kitwork-manager-load-concurrency=1000 '-kitwork-manager-load-search-slots=16,32,64' -kitwork-manager-load-duration=3s -kitwork-manager-load-writes-per-second=20
```

These are hot-cache-oriented, closed-loop synthetic measurements on 100,000
total documents. They prove bounded behavior and snapshot correctness under
1,000 concurrent clients; they do not prove the same latency for 10 million
documents per tenant, a cold-cache fleet, or 1,000 simultaneous queries that
all reach the engine without upstream connection/rate limits.

## Block work comparison

The focused 100,000-document benchmark compares the same Segment V2 and exact
BM25 results with pruning enabled and disabled.

| Mode | Latency | Decoded lead blocks | Pruned lead blocks | Allocations |
| --- | ---: | ---: | ---: | ---: |
| Conjunctive Block-Max | 2.167 ms/op | 1 | 781 | 36,759 B/op |
| Exhaustive | 11.173 ms/op | 782 | 0 | 61,776 B/op |

Wall-clock values can vary. Default tests therefore enforce result equality
against exhaustive scoring and deterministic decoded/pruned work bounds rather
than asserting machine-dependent durations.

```text
go test ./search -run '^$' -bench '^BenchmarkBlockMaxConjunctive$' -benchmem -benchtime=1s
```
