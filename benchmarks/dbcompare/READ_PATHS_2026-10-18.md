# Prefix-Fenced Pages And Read Paths

Date: 2026-10-18. Windows/amd64, Go 1.26.0, local desktop measurements.
Follow-up to [checkpoint optimization](CHECKPOINT_2026-10-18.md).
No release qualification, commit or push is included.

## Evidence And Change

The previous mixed-workload profile still spent most allocation reading and
decoding pages. New isolated read-path benchmarks separated row-layout metadata,
kernel point lookup, native SQL point lookup and a 16-row secondary-index page.
Metadata was cheap in this fixture: 240 B and six allocations per call. It did
not justify adding a transaction-local metadata cache.

The kernel point probe for an unchanged row read 17 pages, 485,957 encoded bytes,
per repeated lookup despite a 256 KiB cache. Incremental pages mixed physical
metadata, row keys and index keys. A row key beyond a delta's changed row range
still fell between the mixed page's first/last keys, requiring payload decoding
to discover absence. Repeating this across segments churned the small cache.

The production change is one writer packing condition: finish the current page
when the next key's first byte changes, in addition to existing byte/record
ceilings. Existing first/last-key directory fences can then reject disjoint
ranges without reading their pages. This is a generic byte-range policy, not
a SQL-namespace parser or a new read cache. Both incremental append and full
compaction use the same writer.

No reader, snapshot ownership, caller-owned copy, cache size, worker count,
admission ceiling, WAL frame, format field, checksum, fsync or publication rule
changes. Old mixed-prefix v3 pages remain valid, and a file can contain both
old and newly packed segments. No existing file is silently rewritten on open.
New checkpoints pack new deltas; full compaction eventually repacks old pages.
An unchanged checkpoint need not rewrite an old file.

## Isolated Read Fixture

1,024 rows with 512-byte payloads, primary/secondary indexes, one base segment
and 16 incremental 32-row updates/checkpoints. Row 900 is unchanged by those
deltas. Query-result cache is off; the shared page cache remains 256 KiB.
Four Go execution threads; three fixed 100-iteration samples per path. SQL
calls include ordinary transaction/snapshot/result work; the kernel probe uses
one pinned snapshot. Setup and checkpoint creation are outside timed subtests.
No benchmark ran alongside our other benchmark/build/test/profile processes.

| Path | Before ns/op range | After ns/op range | Before B/op | After B/op | Before -> after allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: |
| Row-layout metadata | 652-1,647 | 705-756 | 240 | 240 | 6 -> 6 |
| Kernel point, unchanged row | 213,389-304,591 | 439-661 | 650,202-650,212 | 576 | 69 -> 1 |
| Native SQL point | 460,914-646,016 | 71,710-78,345 | 752,385-752,418 | 21,096-21,171 | 221 -> 143 |
| Native SQL index, LIMIT 16 | 2,989,510-3,446,451 | 502,362-588,973 | 6,009,915-6,009,963 | 773,794-773,983 | 1,612 -> 1,068-1,069 |

SQL point allocation fell about 97.2%; index allocation about 87.1% in this
fixture. The kernel probe moved from 17 page reads per lookup to zero additional
page reads after warm-up: unrelated deltas are rejected by directory fences,
and the base row's page now remains in cache. The remaining 576-byte allocation
is the caller-owned encoded value, not a borrowed mutable cache buffer.

This is deliberately an unchanged-row/cache-thrashing diagnostic, not the
distribution of arbitrary point reads. Zero extra page reads is a warm-cache
result, not a cold-I/O claim. Very short Go benchmark timings are descriptive,
not nanosecond SLA evidence. Allocations and explicit page counters are the
stronger result; mixed queries below vary their target rows and buckets.

## Mixed SQL Campaign

Same bounded workload and independent oracle as the
[mixed campaign](MIXED_2026-10-18.md): eight requested seconds plus admitted
operation drain, 1,024 rows per database, four execution threads, cache 256 KiB,
snapshot/write budgets 8 MiB each, at most two maintenance workers. Readers
are paced closed-loop at 10 ms, normal one-row writers at 20 ms, checkpoints
at 100 ms. Noisy DB zero has one unpaced 32-row writer. A new TempDir and fresh
process are used for every case. No automatic checkpoint evaluator or query
result cache is enabled. These are native SQL, not pgwire/HTTP measurements.

The primary sweep interleaved baseline then optimized binary for each case;
no profiler or our build/test/trace work ran alongside these measurements.
The machine was not reserved against unrelated OS/application load. Both
binaries already include the earlier snapshot and checkpoint-count changes.

| Case | Point/s before -> after | Index/s before -> after | Commit/s before -> after | Point/index p99 ms before -> after | Allocated GiB before -> after |
| --- | ---: | ---: | ---: | ---: | ---: |
| balanced_1 | 99.9 -> 100.1 | 99.6 -> 99.9 | 50.0 -> 50.1 | 16/16 -> 16/16 | 0.867 -> 0.719 |
| balanced_4 | 397.1 -> 396.6 | 395.5 -> 396.4 | 199.6 -> 199.7 | 20/20 -> 16/20 | 3.421 -> 2.826 |
| noisy_4 | 389.7 -> 393.2 | 386.0 -> 391.1 | 363.1 -> 413.6 | 25/25 -> 20/20 | 5.965 -> 6.234 |
| balanced_16 | 1,149.1 -> 1,513.3 | 957.0 -> 1,507.3 | 634.8 -> 773.9 | 100/150 -> 25/50 | 16.828 -> 10.726 |
| noisy_16 | 1,107.0 -> 1,410.1 | 920.5 -> 1,379.7 | 649.4 -> 778.9 | 100/150 -> 50/50 | 16.653 -> 10.746 |

Percentiles are histogram upper bounds, not exact values or additive phases.
The one/four-DB normal cases are near pacing ceilings; unchanged throughput
does not show a lack of reduced work. Conversely, noisy_4 total allocation
increased while more write work completed: do not claim lower total memory
allocation in every case. Total allocated bytes are not heap peak or RSS.
Changing workload progress and segment shapes also preclude treating bytes
per acknowledged transaction as a uniform work-normalized metric.

One additional reverse-order pair ran optimized first, then baseline, for each
16-DB case, without profiling:

| Case | Point/s before -> after | Index/s before -> after | Commit/s before -> after | Point/index p99 ms before -> after | Checkpoint p99 ms before -> after |
| --- | ---: | ---: | ---: | ---: | ---: |
| balanced_16 | 1,141.7 -> 1,562.8 | 960.9 -> 1,564.5 | 634.0 -> 789.1 | 100/150 -> 20/20 | 1,000 -> 250 |
| noisy_16 | 1,124.5 -> 1,534.0 | 947.4 -> 1,524.6 | 674.8 -> 869.2 | 100/150 -> 25/25 | 750 -> 300 |

The primary checkpoint p99 buckets were 750 -> 400 ms for balanced_16 and
1,000 -> 750 ms for noisy_16. These local pairs support the direction of the
read-path improvement, not statistical significance, a universal speed ratio,
long-duration fairness or an open-loop tail guarantee. Full-compaction counts
depend on checkpoint progress and are separate from the isolated 17-segment
read fixture. This does not eliminate read amplification in every key range.

## Cost And Remaining Bottleneck

Prefix separation creates more small pages and directory entries. Pages remain
packed contiguously without padding to 64 KiB. Sorted byte keys introduce at
most 255 leading-byte boundaries per segment, but directory metadata can still
matter for many small raw keys with diverse first bytes. A regression fixture
with 255 one-byte keys (excluding reserved catalog namespace 1) stores 2,805
payload bytes and 8,670 directory bytes across 255 pages. This intentionally
exposes the space tradeoff rather than hiding it.

No before/after whole-file size survey for arbitrary application databases or
storage-normalized write benchmark was collected. The page cache budget is
unchanged, but more directory entries can consume memory outside that budget.
Do not call this a smaller-file guarantee or a hard RSS bound. See
[memory-budget notes](../../kitdb/MEMORY_BUDGET.md).

A separate optimized noisy_16 allocation profile captured only the workload
and its admitted-operation drain, excluding fixture setup and reopen checks.
The cumulative after profile was analyzed with the pre-workload profile as
`-base`. It still attributes 78.01% of sampled allocation to page read/decode.
The workload completed more work than earlier profile windows, so absolute
cross-stage profile totals are not a matched speed or memory ratio. Candidate
next work is range/index cursor initialization and unnecessary selected-page
decoding inside the same prefix, before adding private caches or more memory.

## Correctness And Verification

All 15 workload reports (ten primary, four reverse, one diagnostic allocation
profile) passed their independent full row/version/payload and every secondary
index oracle after reopen: 63,703 acknowledged transactions. Commit sync counts
matched ACKs; snapshot/transaction/admission/pending-commit charges drained;
the manager closed with zero leases, managed handles and maintenance jobs.
These facts do not classify all OS handles, prove physical memory reclamation,
or establish long-duration leak freedom.

New regression tests cover four byte ranges including 0 and 255, 35 generations,
tombstones, compaction, cache-disabled unrelated-prefix probes requiring zero
page reads, snapshot visibility and reopen. The immutable historical format-v3
fixture is also opened, appended with new prefix-fenced pages, checked through
an older retained snapshot, and reopened. That fixture was not regenerated.
The directory-bound test checks exact payload/directory bytes without padding.

Completed checks:

- Full kernel/node/relational suites: 37.844s / 23.708s / 107.804s.
- Focused kernel/relational race suites, count=2: 13.332s / 15.950s.
- Tagged SQL checkpoint-publication crash and storage-I/O fault matrices: 11.694s.
- Focused KitDB vet and `go build -mod=readonly ./kitdb/... ./benchmarks/tracephases`.

Host integration was attempted with `TestKitDBDatabaseReleaseGate` but could
not compile the concurrently edited UI package: `ui/layout.go:379` undefined
`suggestion`, `ui/toast.go:236` undefined `containerHosts`, and an unused `path`
import in `ui/compile.go`. The later whole-engine build still failed on the
latter two errors. These files were not changed for this KitDB work; the host
gate and whole-engine build are **not green** in this evidence set. Their logs
are retained. No full UI/browser suite, Linux, release gate, 24-hour canary or
hardware power-loss validation was run.

## Artifacts And Reproduction

Head `e89bd9824396a2684c7669a029e94d9b526f1910`, dirty worktree. No commit/push,
candidate promotion or claim of stability. Raw evidence:

- `.artifacts/kitdb-readpaths-20261018-r1/`: before binary, microbenchmarks, seven mixed JSON/log pairs.
- `.artifacts/kitdb-readpaths-20261018-r2/`: after binary, microbenchmarks, eight mixed/profile JSON/log pairs, profiles and test logs.
- `r2/summary-validated.json`: rehashed raw reports and exact ACK total. The earlier derived `summary.json` incorrectly summed an array's Count; it is superseded, not evidence for the transaction total. Raw workload reports were unchanged.

| Frozen binary | SHA-256 |
| --- | --- |
| r1/read.test.exe | `be9de2f9dbbf00f7cb61a28357a91865510f2dd183a6d58ecd604f89cf80cf88` |
| r2/read.test.exe | `9703e9f24ae95abf3ca9d9c85e782149477178f163ff3fbb34c7d795527a7393` |

The initial prefix test used the reserved catalog byte for ordinary KV data and
failed schema validation before testing the writer. The fixture was corrected,
the failed log retained, and the full suite subsequently passed. It is not a
storage recovery failure. Logs/profiles are local ignored artifacts, not a
clean-candidate qualification.

From `engine/`, use a fresh output directory and build once per implementation:

```powershell
go test -c -o .artifacts/read-new.test.exe ./kitdb/relational
& ./.artifacts/read-new.test.exe '-test.run=^$' '-test.bench=^BenchmarkReadPaths$' '-test.benchmem' '-test.benchtime=100x' '-test.count=3' '-test.cpu=4'
go test -race ./kitdb '-run=TestGenerationPagePrefix|TestGenerationPrefixFences|TestKitDBV1FrozenMainFixture' -count=2
```

Use the [mixed campaign instructions](MIXED_2026-10-18.md) for each of the five
named SQL cases. Profiling is a separate run via the
[windowed profile controls](../tracephases/README.md); clear both profiling
environment variables for latency evidence. Synthetic TempDirs only: no
production database, credentials or arbitrary workload SQL are accepted.

Follow-up: [bounded range reads](RANGE_BOUNDS_2026-10-18.md) push both normalized
range bounds into segment readers, remove discarded reverse initialization,
and record page counters plus mixed SQL repeats. That later evidence also
includes a passing whole-engine build and host database gate; it does not
retroactively change the failed build logs retained for this run.
