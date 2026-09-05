# Posting traversal / Varint experiment: retained 13M shopping

Date: 2026-09-05. Base engine: `b80133f3471639c26e21fd5dc8a64b34cffaec07`.
Worktree branch: `codex/search-posting-traversal`.

## Decision

**Do not promote the performance prototype.** The 16-P results suggest a
possible multi-field gain, but the 2-P runs do not reproduce it consistently.
The host was noisy, sample count is small, and several controls regressed.
Neither a speedup nor the size of a regression can be established reliably.
We stopped instead of sampling until a favorable result appeared.

The final production diff keeps only two malformed-payload safety checks.
It does not retain the custom lower-bound search, seed leap, or one-byte
Varint fast paths. No format, BM25 formula, cache budget, WAL, dependency,
or concurrency policy changes.

Retained work: a six-query standalone KitDB benchmark, ranked-output
fingerprints, source-transaction checks, version/position/advance regression
tests, a document-wise multi-field AND oracle, and decoder fuzz coverage.
This is not a new RC or evidence that stable 1.0 is ready.

## Scope and procedure

- Retained fixture: `shopping_13m_full.kitdb`, the previously verified
  13,773,074-row shopping dataset. This experiment did not reimport or recount it.
- Source transaction: 33793 throughout; main file 30,054,427,583 bytes,
  packed search file 9,730,791,378 bytes.
- Pure-Go standalone relational API; no Kitwork VM, PostgreSQL server, or HTTP.
- Windows/amd64, Intel Core i7-11850H, Go 1.26.0; GOMAXPROCS 16 and 2.
  These are runtime P limits, **not** counts of simultaneous search requests.
- Existing immutable packed projection with a 256 MiB reader-cache budget.
  Reported capacity 236.9 MiB; residency 76.8-192.4 MiB depending on query.
- Open/EXPLAIN/warm query precede timing. The hot snapshot and union-frequency
  cache are reused. Open time and cold-disk latency are not measured.
- The kernel is opened in replica mode to forbid commits. Packed queries never
  rebuild on demand. The harness requires `search-snapshot` execution.
- Each subbenchmark hashes the warm and final timed ranked rows, including
  scores, outside the timer; it also verifies LastTransaction is unchanged.
  The harness does not hash every timed iteration.
- All 123 logged benchmark samples agreed on each query's fingerprint across
  variants and used transaction 33793. This is differential evidence, not proof
  of relevance quality or exhaustiveness for every possible query.
- Preliminary runs targeted 1s per subbenchmark; confirmation targeted 2s.
  GOMAXPROCS=16 confirmation order: baseline, Varint-only, candidate, candidate,
  baseline, Varint-only, Varint-only, candidate, baseline.
  GOMAXPROCS=2: baseline, candidate, candidate, baseline, baseline, candidate.
- Other applications remained running. CPU limits did not eliminate variance.
  No cold-cache reset, dedicated-host isolation, multi-client p95, Linux runtime,
  or concurrent-write claim.

Queries select `merchant, id, name, _score`, order by `_score DESC`.
Name and all-fields use `ban phim logitech`; coffee uses `highlands coffee`;
common uses the one-term `coffee`. Merchant variants filter
`merchant = 'shopee'` and limit to 120, others limit to 20.

## What was tested

1. Traversal-only: replace callback binary search inside Advance with an
   inlinable edge-aware lower bound; on an AND mismatch, advance the lead to
   the other term's next document instead of just lead+1.
2. Wrapper decoder: one/two-byte Varint helper. Compiler diagnostics showed
   it exceeded the inline budget; it was not selected.
3. Candidate (`inline` in logs): traversal changes plus one-byte decodes
   directly at gap, frequency, and position call sites; wider or malformed
   values still use encoding/binary.Uvarint.
4. Varint-only control: direct one-byte sites without traversal changes.
   Its 16-P confirmation also did not demonstrate a reliable gain.

The prototype kept the existing payload/header validation, exact BM25
accumulation order and tie order. Its patch is preserved locally at
`.artifacts/posting-candidate.patch`, outside the release/source path.

## Profile, before changes

The baseline CPU profile sampled 7.99 CPU seconds over 7.90 wall seconds.
This profiled run is not a latency comparison:

| Frame | Flat | Cumulative |
| --- | ---: | ---: |
| encoding/binary.Uvarint | 17.02% | 17.02% |
| postingIterator.decodeBlock | 14.89% | 38.30% |
| postingIterator.Advance | - | 75.84% |
| multiPostingUnion.Advance | - | 77.97% |
| sort.Search | 1.88% | - |

Windows syscall time appears under runtime.cgocall (45.56% flat); this is
not evidence that the search engine uses a native FTS library. readAt accounts
for 39.17% cumulative. Posting reads/headers still matter, so the finding is
not "I/O is gone"; hydration/identifier materialization is no longer the
dominant target in this profile. Cumulative percentages overlap.

## Confirmation measurements

### GOMAXPROCS=16

Median (min-max), milliseconds/op; three runs per variant, not query p95.

| Query | Baseline | Candidate |
| --- | ---: | ---: |
| name-20 | 11.51 (10.69-24.33) | 12.36 (9.40-15.35) |
| all-fields-20 | 131.10 (102.36-204.29) | 75.80 (72.19-124.54) |
| all-fields-merchant-120 | 149.52 (87.49-279.75) | 86.45 (81.11-90.91) |
| coffee-20 | 12.31 (9.02-14.45) | 9.82 (9.74-10.66) |
| coffee-merchant-120 | 20.46 (12.58-21.09) | 15.49 (13.15-22.89) |
| common-20 | 14.66 (7.22-14.66) | 11.59 (6.67-12.53) |

### GOMAXPROCS=2

Median (min-max), milliseconds/op; three runs per variant, not query p95.

| Query | Baseline | Candidate |
| --- | ---: | ---: |
| name-20 | 14.25 (12.94-16.65) | 16.11 (13.31-29.74) |
| all-fields-20 | 116.96 (115.15-207.25) | 124.31 (101.14-133.28) |
| all-fields-merchant-120 | 139.10 (84.06-146.58) | 142.48 (91.70-180.19) |
| coffee-20 | 14.61 (13.71-19.90) | 15.62 (9.85-31.79) |
| coffee-merchant-120 | 23.31 (22.39-29.96) | 21.61 (12.72-44.15) |
| common-20 | 16.93 (14.26-20.45) | 21.78 (10.00-24.46) |

Per-operation allocated bytes remained approximately unchanged: 2.81 MB for
name, 7.98 MB for all-fields, 8.90 MB with merchant, 2.21 MB for coffee,
3.04 MB with merchant, 1.94 MB for common. These are **allocations per
operation, not resident RAM or whole-process RSS**.

## Full sample ledger

All optimization-trial measurements, including unfavorable screening runs and the
profiled run, are below in ms/op. `combined` is the rejected wrapper decoder;
`inline` is the final speed prototype, not the later safety-only fix.
Different phases are not pooled into one speedup percentage.

| Log file (under `.artifacts/`) | CPU | Name | All fields | + Merchant | Coffee | Coffee + Merchant | Common |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| posting-baseline-initial.txt | 16 | 11.329 | 69.547 | 85.284 | 8.695 | 13.884 | 7.596 |
| posting-baseline-profile.txt | 16 | - | 79.458 | - | - | - | - |
| posting-combined-initial.txt | 16 | 12.420 | 70.762 | 75.039 | 9.660 | 12.527 | 7.969 |
| posting-cpu2-0-baseline.txt | 2 | 14.252 | 115.152 | 84.064 | 14.606 | 22.393 | 14.263 |
| posting-cpu2-1-inline.txt | 2 | 16.114 | 101.145 | 91.702 | 15.619 | 21.613 | 21.776 |
| posting-cpu2-2-inline.txt | 2 | 29.744 | 133.279 | 180.193 | 31.791 | 44.151 | 24.463 |
| posting-cpu2-3-baseline.txt | 2 | 16.645 | 207.253 | 139.095 | 13.711 | 29.958 | 16.928 |
| posting-cpu2-4-baseline.txt | 2 | 12.936 | 116.957 | 146.582 | 19.896 | 23.311 | 20.450 |
| posting-cpu2-5-inline.txt | 2 | 13.310 | 124.306 | 142.484 | 9.847 | 12.716 | 9.999 |
| posting-final-0-baseline.txt | 16 | 11.511 | 131.097 | 149.516 | 12.307 | 21.095 | 14.660 |
| posting-final-1-varint-only.txt | 16 | 15.045 | 161.442 | 230.765 | 36.919 | 26.224 | 12.001 |
| posting-final-2-inline.txt | 16 | 12.359 | 72.187 | 86.454 | 9.816 | 15.494 | 11.587 |
| posting-final-3-inline.txt | 16 | 15.345 | 124.538 | 90.914 | 10.659 | 22.891 | 12.528 |
| posting-final-4-baseline.txt | 16 | 24.327 | 204.292 | 279.755 | 14.454 | 20.457 | 14.661 |
| posting-final-5-varint-only.txt | 16 | 16.405 | 90.098 | 214.936 | 18.013 | 31.345 | 29.979 |
| posting-final-6-varint-only.txt | 16 | 17.278 | 172.033 | 181.939 | 14.369 | 13.301 | 6.957 |
| posting-final-7-inline.txt | 16 | 9.397 | 75.797 | 81.114 | 9.743 | 13.149 | 6.667 |
| posting-final-8-baseline.txt | 16 | 10.689 | 102.362 | 87.485 | 9.018 | 12.583 | 7.217 |
| posting-inline-initial.txt | 16 | 40.294 | 204.281 | 329.597 | 29.935 | 32.697 | 26.827 |
| posting-pair-13b12271-baseline.txt | 16 | - | 93.523 | - | - | 13.936 | - |
| posting-pair-a3382867-inline.txt | 16 | - | 64.939 | - | - | 14.132 | - |
| posting-pair-d073c5d9-baseline.txt | 16 | - | 72.190 | - | - | 11.407 | - |
| posting-pair-fe7bcc6a-inline.txt | 16 | - | 66.353 | - | - | 11.366 | - |
| posting-traversal-initial.txt | 16 | 11.099 | 68.542 | 90.661 | 8.084 | 11.851 | 7.126 |

## Ranked-result fingerprints

SHA-256 of JSON-encoded ordered rows, at source transaction 33793:

| Query | Rows | Digest |
| --- | ---: | --- |
| name-20 | 20 | `a0e307deb85892879b6346a0b5f11136bcb559963ac47f114c1b517fc8cf9ff3` |
| all-fields-20 | 20 | `c74a2c40cb184a207d6eea04c05216b8fa61ba2aadef474e1c00dca1aa9526af` |
| all-fields-merchant-120 | 120 | `2c7007fb7cacd0266a24d94baaa74256263adeb4312796c680503e4b9c1dce54` |
| coffee-20 | 20 | `e011d067a5649e67998713955879bbb2d2315076ba1549f57267b349763334c7` |
| coffee-merchant-120 | 120 | `468f2a8cd020d09cf5cb79443d007c76411734652660db9b86a5a05c51e22026` |
| common-20 | 20 | `7ff750ba687d616c0debb2d81c9c530de9d24f5b090b4dbbbcb5cc138e677054` |

## Safety findings from the decoder audit

Neither finding was introduced by the speed prototype. Both were reproduced
after removing it, on the baseline decoder:

- A CRC-valid payload can encode a document beyond the block's declared last
  document. With field norms present, the old decoder indexed that document
  before the final header check and panicked. It now rejects the escaped
  document before the norm lookup.
- A positional payload can declare more positions than its remaining bytes
  could encode. The old decoder allocated from the untrusted frequency before
  detecting truncation. The reproducer requested 262,144 positions with only
  one position byte available. It now rejects that impossible count first.
  Every unsigned Varint consumes at least one byte; valid encodings are unchanged.

Regression tests failed before each fix (panic and oversized allocation),
then passed. The fuzz target recomputes CRCs to reach decoder logic; the final
target also requests decoded positions, rather than testing only skipping.
This is bounded corruption testing, not exhaustive verification of all formats.

## Reproduce

From the engine worktree in PowerShell, point the environment variable at the
verified fixture; do not point it at a concurrently served database.

```powershell
$env:KITDB_SHOPPING_PACKED_SEARCH_BENCHMARK = '<verified-copy>/shopping_13m_full.kitdb'
$env:GOFLAGS = '-buildvcs=false'
go test -c ./kitdb/relational -o .artifacts/posting-current.test.exe
& ./.artifacts/posting-current.test.exe '-test.run=^$' '-test.bench=^BenchmarkShoppingProductionPackedSearch$' '-test.benchmem' '-test.benchtime=2s' '-test.count=1' '-test.cpu=2' '-test.timeout=5m'
go test ./search ./kitdb/relational -count=1 -timeout=10m
go test -race ./search ./kitdb/relational -count=1 -timeout=15m
go test ./search -run '^$' -fuzz '^FuzzPostingPayloadAgainstScalar$' -fuzztime=30s -parallel=2 -timeout=2m
```

Baseline and prototype comparison binaries/logs remain local under
`.artifacts/posting-*`; rebuild a fresh candidate separately from the release
artifact. Existing v1.0.0-rc.1 binaries and checksums were not replaced.

## Verification

- Full search and relational tests: passed after the safety guards.
- Prototype scalar-agreement fuzz run: passed, 18,029 executions in 30 seconds.
- Full search and relational race tests: passed (19.218s / 200.537s).
- Final decoder tests and positional scalar-agreement fuzz: passed, 297,889
  executions over the 30s fuzz budget, two workers. This bounded run is not
  a long-duration fuzz campaign.
- `go vet ./search ./kitdb/relational`: passed.
- `CGO_ENABLED=0 go build -o .artifacts/kitdb-posting-checked.exe ./cmd/kitdb`:
  passed. This is a development executable, not a signed/versioned RC.
- Safety-only final smoke: all six queries passed with exactly the same
  ranked-output fingerprints as the baseline and source transaction 33793.
  Log: `.artifacts/posting-checked-smoke.txt`. One timed iteration per query
  checks correctness, not the performance impact of the safety guards.
- `git diff --check` and gofmt checks: passed. No full release gate, Linux
  runtime test, or new release artifact was run/published for this experiment.

## Next optimization gate

Use a controlled host and the unchanged six-query/digest harness. Before
trying a new format, measure decoded postings/blocks, advance distance,
payload/header reads and allocations, so an improvement in deterministic
work can be separated from host variance. Any later optimization must preserve
ranked results, filtering, pagination and malformed-data handling, and must
win on representative 13M end-to-end queries without worsening the small-node
resource budget. This experiment supplies no justified "<15 ms at 13M" claim.
