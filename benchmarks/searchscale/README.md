# Kitwork search scale benchmarks

This isolated module compares Kitwork's segment/index engines, record-per-term engine, Bleve, and
Turso's Tantivy-backed FTS without adding their dependencies to the production engine module.

## Workload

- 1,000,000 generated products
- Approximately 63 tokens per product across title and body
- Top 20 results with hot-cache P50/P95 latency
- Implicit AND semantics for every query term
- Vietnamese diacritic folding, so `ao` matches `áo`
- Windows AMD64, Go 1.26, 21 measured samples after warmup

Turso's default tokenizer does not fold Vietnamese diacritics. Its benchmark therefore stores
Kitwork-normalized `search_title` and `search_body` projections and normalizes the query before
passing it to Tantivy.

## Turso native build

The platform DLL shipped by `turso-go-platform-libs v0.7.2` does not include the FTS Cargo feature.
Build the ABI-compatible DLL from the same Turso tag:

```powershell
$source = "$env:TEMP\kitwork-turso-fts-v0.7.2"
git clone --depth 1 --branch v0.7.2 https://github.com/tursodatabase/turso.git $source
git -C $source apply D:\project\kitwork\engine\benchmarks\searchscale\turso-v0.7.2-fts.patch
cargo build --manifest-path "$source\Cargo.toml" --profile lib-release --package turso_sync_sdk_kit --features fts
```

Run the Turso benchmark:

```powershell
go test -tags turso_scale -run "^TestTursoScale$" -count=1 -v -timeout 40m -args `
  -kitwork-turso-library="$source\target\lib-release\turso_sync_sdk_kit.dll" `
  -kitwork-turso-sizes=1000000 -kitwork-turso-samples=21
```

Run Bleve from this module:

```powershell
go test -tags bleve_scale -run "^TestBleveScale$" -count=1 -v -timeout 30m -args `
  -kitwork-bleve-sizes=1000000 -kitwork-bleve-samples=21 -kitwork-bleve-batch=5000
```

Run the custom engine from `engine/`:

```powershell
go test -tags scale ./capabilities/collection -run "^TestFTSScale$" -count=1 -v -timeout 30m -args `
  -kitwork-scale-sizes=1000000 -kitwork-scale-samples=21
```

Run immutable Segment V1 from `engine/`:

```powershell
go test -tags scale ./search -run "^TestSegmentScale$" -count=1 -v -timeout 30m -args `
  -kitwork-segment-sizes=1000000 -kitwork-segment-samples=21
```

Run the immutable multi-segment index with ten 100,000-document segments from `engine/`:

```powershell
go test -tags scale ./search -run "^TestIndexScale$" -count=1 -v -timeout 30m -args `
  -kitwork-segment-sizes=1000000 -kitwork-segment-samples=21 `
  -kitwork-index-segment-documents=100000
```

Run the update/delete/compact/GC lifecycle workload:

```powershell
go test -tags scale ./search -run "^TestIndexLifecycleScale$" -count=1 -v -timeout 30m -args `
  -kitwork-segment-sizes=1000000 -kitwork-index-segment-documents=100000
```

## Results on 2026-08-20 and 2026-08-21

All latency values are hot-cache P50 milliseconds.

| Engine | Build | Persisted size | Peak build RSS | RSS after build | Exact SKU | 1 common term | 2 terms | 4 terms | Unaccented 5 terms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Turso + Tantivy | 126.54 s | 1232.37 MiB | 1799.29 MiB | 115.28 MiB | 0.713 | 2.148 | 4.643 | 7.176 | 8.081 |
| Kitwork Index V1, 10 segments | 37.17 s | 192.67 MiB | 205.27 MiB | 59.74 MiB | 0.059 | 7.545 | 30.593 | 64.603 | 66.005 |
| Kitwork Segment V1 | 42.60 s | 192.64 MiB | 1308.63 MiB | 912.43 MiB | 0.014 | 8.272 | 34.769 | 55.288 | 61.041 |
| Bleve | 35.75 s | 580.32 MiB | 483.82 MiB | 35.97 MiB | 0.028 | 18.464 | 44.696 | 84.183 | 97.643 |
| Kitwork record-per-term | 50.44 s | 639.88 MiB | 4220.30 MiB | 3763.43 MiB | 1.069 | 10.139 | 38.661 | 60.707 | 70.721 |

The storage and build columns are not perfectly equivalent:

- Turso includes the relational rows, normalized projections, and Tantivy index in one database.
- Bleve stores its search index and stored display fields; a separate source database is not counted.
- Segment V1 and Index V1 store their search index and external identifiers, but not the title/body
  display projection. Their size is therefore not directly comparable to Bleve or Turso.
- The custom benchmark stores the search projection; its Markdown or relational source is not counted.
- Turso's total includes 63.45 seconds inserting rows, 35.20 seconds creating the FTS index, and
  27.89 seconds running `OPTIMIZE INDEX`.
- The custom RSS result is measured after rebuilding in the same process. Go retained much of the
  rebuild address space; a query-only process would likely report a lower resident set.
- Segment V1 has the same-process RSS caveat. Its hot queries allocated 5.74-31.35 KiB per
  operation, while its single-segment builder allocated 11.73 GiB cumulatively during the
  one-million-document build.
- Index V1 used ten 100,000-document segments. It allocated 11.59 GiB cumulatively while building,
  but bounded live state reduced peak build RSS from 1308.63 MiB to 205.27 MiB and peak Go heap
  from 1159.69 MiB to 143.40 MiB. Its hot queries allocated 7.96-32.08 KiB per operation.
- Index V1 computes exact index-wide BM25 statistics. Its fan-out improved the measured one- and
  two-term P50 values, while four- and five-term P50 increased. These are measured outcomes, not a
  claim that segmentation always improves query latency.

The one-million-row table predates Manifest V2 identifier/deletion sidecars and must not be used as
their storage measurement. A 2026-08-21 regression with 100,000 products in ten 10,000-document
segments measured 31,583 build docs/s, 21.22 MiB current snapshot bytes, and hot P50 latency of
0.057 ms exact SKU, 1.072 ms one common term, 3.199 ms two terms, and 6.174 ms four terms.

The lifecycle scale test then updated 500 IDs and deleted 500 IDs. Mutation plus commit took
1.215 s (823 operations/s), direct posting compaction from 11 segments to one took 7.875 s,
the compacted live snapshot was 19.19 MiB, and generation-aware GC reclaimed 19.44 MiB in 27 ms.
This is a 100,000-row regression datapoint, not a projection for ten million rows.
