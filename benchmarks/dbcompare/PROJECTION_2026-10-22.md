# Query-Bound Projection: Windows, 2026-10-22

## Qualification Is Blocked

The full `go test ./kitdb/...` run **failed**. The kernel test
`TestCheckpointContextDeadlineAndConcurrentOwner` encountered
`publish rotated WAL: rename ...: Access is denied.` while building
`fullCheckpointFixture`, before the deadline/concurrency test body. That
fixture commits and checkpoints kernel key/value data; it does not execute
relational projection. The file-handle owner and root cause are **unknown**.
The failure resembles earlier Windows WAL-publication evidence, but a common
error message does not establish a common cause.

The original failure is retained, with no whole-suite retry, timeout increase,
production WAL retry, or attribution to antivirus. The independent passing
checks below do not override it. This candidate is **not release-qualified**
and is not a stability claim. No application database was used.

## Implementation

Previously, each projected cell looked up its field in the schema and resolved
the field kind again. `bindProjection` now creates a query-owned immutable
descriptor containing the canonical name and a small conversion mode. The
ordinary SELECT, row-stream, materialized SELECT, and catalog-view paths use
that descriptor. There is no global plan cache or extra persistent state.

Output labels, aliases, duplicate columns, wildcard order, NULLs, exact temporal
text, scalar conversion fallbacks, and JSON decoding rules are preserved. The
tests deliberately retain the old case-sensitive JSON-kind fallback rather
than silently changing type semantics during a performance change. Other
expression/aggregate callers still resolve their kinds through `readField`;
they share the conversion helper but are not claimed as optimized.

On amd64 the descriptor uses 24 bytes per output field instead of the old
16-byte name entry. Streaming also creates a transient retained-name slice
during setup. Neither cost is hidden; no streaming performance gain is claimed.
Storage layout, WAL publication, snapshot ownership, and public APIs are unchanged.

## Measurement

Before and after are frozen local sources based on commit
`3ae270052df8a3a347f811599a24f677d0bf8b00` plus the previously recorded scoped
overlays, not clean commits and not the current shared HEAD. The after source
adds seven production-file deltas and one regression-test file. The exact
baseline SQL executable from the preceding page-latency campaign was reused;
its SHA-256 is
`f2a7fa5836f0fc32880a0afc6a98d9913813636bd72d92c052539e2b058bd0b9`.
Source manifests and both executable hashes are retained locally.

The unchanged `BenchmarkFixedReadWork` uses 1,024 rows, four output columns,
a 512-byte payload, and 17 immutable generations. Each operation checks all
returned values against its independent model. Point and index queries follow
the same seeded 64-step schedule; a scan returns all 1,024 rows. This is native
SQL with result materialization and validation, not pgwire, KitSQL HTTP, or an
isolated projection-only microbenchmark.

Eight adjacent pairs use AB, BA, BA, AB, repeated: 16 sequential processes,
three SQL cases per process, 48 retained samples. Each case requests 2 seconds,
GOMAXPROCS 4. This is timed calibration, not the benchmark comment's fixed
`128x` complete-cycle mode; the last schedule cycle may be partial. Timings
include result validation. Separate 3-second CPU profiles follow all unprofiled
runs, and their timings are excluded from the comparison.

Windows/amd64, Intel i7-11850H, Go 1.26.0. No own build/test/profile process
overlapped the unprofiled runs. Other work was active in the shared repository;
unrelated UI/example commits advanced HEAD during this campaign. The frozen
measured sources were hash-verified unchanged. Other machine activity, CPU
frequency, and OS cache were not controlled. No power-plan change or keep-awake
request was made.

| Case | Before median us/op | After median us/op | Median paired change | After slower pairs |
| --- | ---: | ---: | ---: | ---: |
| Point | 137.542 | 120.6725 | -1.36% | 4/8 |
| Index, up to 16 rows | 524.982 | 551.0675 | -1.23% | 4/8 |
| Scan, 1,024 rows | 3,019.2115 | 2,806.471 | -13.09% | 3/8 |

Positive paired changes mean slower. Paired changes are medians of adjacent
after/before ratios, not ratios of the displayed medians. These are benchmark
average operation costs, not production request percentiles. Point/index have
no clear speed signal. Scan improved in five pairs, but the three slower pairs
were each over 5% slower; its paired geometric mean change is -8.48%. No samples
were trimmed. No confidence interval or general latency non-regression is claimed.

| Case | Before B/op | After B/op | Before allocs/op | After allocs/op |
| --- | ---: | ---: | ---: | ---: |
| Point | 40,183.5 | 40,218.5 | 144 | 144 |
| Index | 487,368 | 486,712 | 506 | 470 |
| Scan | 2,278,687.5 | 2,213,281.5 | 17,209 | 13,117 |

These are medians of eight samples. Point allocated bytes rise about 35 B/op
(0.087%), consistent with a larger setup descriptor, although incidental
allocations prevent assigning every byte to it. Index saves 36 allocations;
scan saves 4,092 allocations and about 65,406 B/op (2.87%). B/op is cumulative
Go allocation, not peak RAM, RSS, or a cross-database memory comparison.

Separate CPU profiles show `projectRow` cumulative sampled CPU at 15.99%
before and 2.98% after. Its after listing no longer calls `FieldByName` or
`LookupKind` per cell. `LookupKind` remains elsewhere (12.09% cumulative in the
after profile). Standard benchmark profiles include setup/calibration; these
sample shares corroborate the removed work, not a normalized speedup formula.

## Verification

- New compatibility, binding-ownership, and SQL-path tests: three repetitions,
  all nine root executions pass. The conversion oracle is a test-only copy of
  the pre-change read rules, covering all registered kinds and representative
  valid/invalid values. SQL tests check independent expected rows, labels,
  streaming agreement, and materializing fallbacks.
- Full `kitdb/...`: 882 root tests passed, one root failed as described above;
  12 packages passed and the root kernel package failed. There are 17 skip
  events, including opt-in campaigns. The relational package passed.
- Selected relational race coverage: 13 root tests passed, no skips.
- `go vet ./kitdb/...` and `go build ./...`: passed on the frozen after source.
  This is not a full engine test-suite pass.
- Independent lifecycle contract gate: normal and race steps passed all 11
  required entries per step, no skips. This narrower gate does not include or
  clear the failed checkpoint-admission fixture.

## Evidence And Next Step

Local evidence lives in `.artifacts/kitdb-projection-20261022/`:
`before.json`, `after/source.json`, original test JSONL, `initial-failure.json`,
gate report/logs, 16 raw benchmark logs, `runs.json`, separate CPU profiles and
listings, `assessment.json`, and the final hash index. The assessment records
concurrent shared-HEAD changes separately from the frozen measured source.

Keep this as an uncommitted projection candidate. Investigate Windows WAL
publication/handle ownership next, without masking the failure with retries;
then rerun qualification on an explicitly frozen candidate. Neither these
allocation results nor the narrower gate certify SQL/pgwire, Linux, a 24-hour
canary, or production stability. No commit or push was made by this task.
