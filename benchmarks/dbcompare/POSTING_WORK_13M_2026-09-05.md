# Deterministic posting work: 13M shopping

Date: 2026-09-05. Branch: `codex/search-posting-traversal`, based on
`b80133f3471639c26e21fd5dc8a64b34cffaec07`, including the two decoder guards
from the [previous experiment](POSTING_TRAVERSAL_13M_2026-09-05.md).

## Result

We now measure work independently of wall-clock noise. The diagnostic
`searchwork` build counted six standalone KitDB queries against the retained
13,773,074-row shopping fixture, source transaction 33793.

For `* SEARCH 'ban phim logitech'`, LIMIT 20, ordered by score:

- 54,290 block headers inspected; 43,762 blocks skipped by document bounds.
- 10,528 blocks decoded, containing 1,289,524 postings.
- 5,654,921 successful Varint decodes, including 3,075,873 position deltas.
  Positions are 54.39% of integer decodes, **not** 54.39% of execution time.
- 6,066,521 payload bytes decoded; 7,803,801 logical header/payload bytes
  requested. The iterator's read-ahead issued 9,813 ReaderAt requests for
  37,387,562 bytes (4.79x logical bytes). These are not measured disk bytes
  or syscalls; the underlying reader and OS may satisfy requests from cache.
- 13,117 seed candidates, 4,358 all-term matches before ranking/prefix filtering,
  38,280 term-field score evaluations. Postings are not unique products;
  one product can contribute across multiple query terms and fields.

The rejected lead-jump prototype reduces Advance calls from 82,834 to 78,718
(4.97%) and field-score evaluations from 38,280 to 36,940 (3.50%), but **does
not change any block, posting, Varint, payload byte or ReaderAt count on any
of these six queries**. It was removed again. Diagnostic-run durations are
not latency comparisons and were not used to promote it.

## Instrumentation design

- Hooks compile to no-ops unless `-tags searchwork` is selected.
- `search.ObserveWork(ctx)` / `WorkObserver.Snapshot()` are diagnostic-only
  APIs, not normal-build public API or new SQL syntax.
- A fixed-size atomic counter array is owned by the caller's observer.
  No global counters, per-term map, identifiers, product text or unbounded history.
- A context-local phase separates exact cross-field document-frequency (DF)
  traversal from ranking. Child contexts preserve cancellation. Reused iterators
  detach the old observer on reset. Independent observers cannot affect each other.
- An observer may collect concurrent searches safely. A snapshot taken while
  work is still running is not an atomic transaction across all counters; final
  snapshots are taken after the query returns.
- Successful block decodes and decoded posting counts are distinct from successful
  integer decodes. A malformed block can consume integers before failing; the
  exact conservation checks below apply to successful fixture queries.
- Width buckets count 1 through 10 bytes. Position Varints are counted even when
  positions are not retained for phrase evaluation.
- Multi* counters currently describe the multi-field AND ranker. They must not be
  described as universal candidate/score counters for OR, phrase or prefix queries.
- Default builds retain no observer state in an iterator: the hook field has size
  zero. Go 1.26 Windows/amd64 disassembly has no work/atomic calls in the inspected
  hot methods; text sizes are unchanged: Advance 800, decodeBlock 3456, readAt
  640, searchMultiCandidates 3328 bytes. This is a local compiler check, not a
  universal performance guarantee for all toolchains.

## Measurements

Queries select `merchant, id, name, _score`, ordered by score. Logitech workloads
use `ban phim logitech`; coffee workloads use `highlands coffee`; common uses
`coffee`. Merchant variants add `merchant = 'shopee'`, LIMIT 120; others LIMIT 20.
The SQL name-only query also reaches the generalized multi-field ranker with
one selected field; it is not the search package's specialized Field API path.

Windows/amd64, Intel i7-11850H, Go 1.26.0. The 256 MiB packed-reader budget and
search limits are unchanged. Each workload opens a new replica-mode kernel,
executes one fresh-reader query, three warm observed repeats, then an unobserved
control query. Warm counters repeat exactly. Ranked-row fingerprints agree with
the previous baseline, including BM25 scores and order. LastTransaction remains
33793. No reimport, source rewrite, projection rebuild or format migration.

"Fresh" means cold per-reader/DF caches, not a cold disk/OS cache.

### Warm ranking work

| Query | Headers | Decoded blocks | Postings | Varints | Position Varints | ReaderAt calls | Requested ReaderAt bytes |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| name-20 | 7156 | 2740 | 335762 | 1046156 | 374632 | 976 | 3145349 |
| all-fields-20 | 54290 | 10528 | 1289524 | 5654921 | 3075873 | 9813 | 37387562 |
| all-fields-merchant-120 | 54290 | 10528 | 1289524 | 5654921 | 3075873 | 9813 | 37387562 |
| coffee-20 | 557 | 548 | 25748 | 89229 | 37733 | 501 | 143028 |
| coffee-merchant-120 | 605 | 589 | 28099 | 97156 | 40958 | 540 | 158438 |
| common-20 | 517 | 517 | 36828 | 128192 | 54536 | 392 | 178436 |

### Fresh-reader DF cost

This is additional work before ranking; each warm repeat has zero DF traversal.

| Query | DF postings decoded | DF position Varints | DF Advance calls | Warm DF postings |
| --- | ---: | ---: | ---: | ---: |
| name-20 | 978418 | 1087358 | 978775 | 0 |
| all-fields-20 | 7499137 | 23016170 | 20036941 | 0 |
| all-fields-merchant-120 | 7499137 | 23016170 | 20036941 | 0 |
| coffee-20 | 37744 | 56059 | 91560 | 0 |
| coffee-merchant-120 | 37744 | 56059 | 91560 | 0 |
| common-20 | 36828 | 54536 | 90119 | 0 |

For the logitech multi-field query, a fresh reader scans 7,499,137 postings
and decodes 23,016,170 position deltas just to establish exact union DF.
The bounded cache already avoids repeating that work while the reader stays
warm. This is relevant to cold-tenant policy; it is not an excuse to keep all
tenants warm without a resource budget.

### Lead-jump work comparison

Same queries, hashes, fixture and tracing. No latency claim.

| Query | Advance before | Advance after | Field scores before | Field scores after | Change in decoded postings |
| --- | ---: | ---: | ---: | ---: | ---: |
| name-20 | 23773 | 21313 | 15058 | 13823 | 0 |
| all-fields-20 | 82834 | 78718 | 38280 | 36940 | 0 |
| all-fields-merchant-120 | 82834 | 78718 | 38280 | 36940 | 0 |
| coffee-20 | 2864 | 2848 | 1721 | 1719 | 0 |
| coffee-merchant-120 | 2942 | 2926 | 1748 | 1746 | 0 |
| common-20 | 90119 | 90119 | 36828 | 36828 | 0 |

Cold DF counts and all ranked-output hashes are also unchanged. Early ranking
and merchant-prefix rejection counts remain unchanged, as expected for the
same exact results and unchanged scoring order.

Raw counter snapshots are in [the JSON ledger](POSTING_WORK_13M_2026-09-05.json).
Local tool logs: `.artifacts/posting-work-13m.txt` and
`.artifacts/posting-work-leap-13m.txt`. The latter contains the experimental
lead jump, not the retained production traversal.

## Tests and reproducibility

```powershell
$env:KITDB_SHOPPING_PACKED_SEARCH_BENCHMARK = '<verified-copy>/shopping_13m_full.kitdb'
go test -tags searchwork ./kitdb/relational -run '^TestShoppingProductionPackedSearchWork$' -v -count=1 -timeout=8m
```

Use an idle verified copy, never a concurrently served source. This fixture is
opt-in and skips without the environment variable. Timing benchmarks remain
separate and should be built **without** the diagnostic tag.

Tests cover exact posting/block/byte totals, read-ahead vs skipped blocks,
position decoding without retention, iterator reuse, cancellation, nested
observer isolation, and concurrent query aggregation. The production fixture
checks ranked hashes and deterministic warm repeats, plus:

- Varint count = twice decoded postings + position Varints.
- Sum of width-bucket bytes = decoded payload bytes.
- Headers = decoded blocks + target skips + score skips.
- Logical read requests = read-ahead hits + underlying ReaderAt requests.

Verification completed:

- Normal search/relational suites: passed (7.513s / 27.159s).
- Diagnostic search/relational suites: passed (7.378s / 27.988s).
- `go test -race -tags searchwork ./search ./kitdb/relational`: passed
  (14.409s / 98.250s), including shared and isolated observers.
- `go vet` for both normal and diagnostic packages: passed.
- Pure-Go standalone `cmd/kitdb` build: passed with `CGO_ENABLED=0`, output
  `.artifacts/kitdb-work-checked.exe`, not a new release artifact.
- Final diagnostic fixture: passed; all twelve cold/warm snapshots exactly
  match the baseline ledger, including hashes and transaction 33793. Its warm
  counts repeated identically across three observed queries per workload.
- Normal-build six-query fixture smoke: passed; hashes match the diagnostic
  and previous baseline. This single-iteration smoke is not speedup evidence.
- Final logs: `.artifacts/posting-work-final-13m.txt` and
  `.artifacts/posting-work-off-smoke.txt`.
- `git diff --check`: passed. No Linux runtime run, new RC, or stable release
  claim is made here; database contents and search format are unchanged.

## What this changes about the next optimization

Do not start by hand-unrolling every Varint or adding more concurrency.
The current interleaved posting layout forces non-phrase queries to parse
position deltas to locate the next posting. A more useful next experiment
is a versioned, independently bounded/checksummed positional substream so
ordinary term/BM25 queries can avoid decoding it while phrase queries retain
exact semantics. Such a format change needs reader compatibility, corruption
tests, rebuild/rollout policy, and uninstrumented end-to-end benchmarks before
adoption. No new format is introduced here.

Separately measure skip-aware read-ahead or a compact block directory: 80.61%
of inspected logitech headers lead to skipped payloads, but 4 KiB read-ahead
can still fetch bytes from those skipped ranges. Fewer bytes may mean more
calls, so that tradeoff also needs normal-build benchmarks. Never remove CRC
or bounds checking just to lower counters.
