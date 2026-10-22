# Page Latency Follow-Up: Windows, 2026-10-22

## Decision

Keep the previously tested positions representation **B** as an allocation
candidate with an explicit CPU tradeoff, not as a latency-qualified release.
Reject the attempted shared-bounds refinement **C**: fewer checks in the
generated code did not make its row-only benchmark faster. Production was
restored byte-for-byte to B. This iteration adds benchmark/fixture tests and
evidence, but leaves no new production-code delta.

The prior +14.45% reused-read and +2.95% SQL-scan median changes did not repeat
consistently in the new paired runs. This does not establish the cause of those
earlier results or prove universal non-regression. Row-view-only access has a
repeatable slower signal, while decode plus row consumption improved in seven
of eight pairs. Retaining B is therefore not based only on attractive B/op.
It is a measured space/time tradeoff requiring further latency qualification.

## A/B Comparison

- **A:** old pointer-bearing row metadata, two byte-slice headers per row.
- **B:** private integer positions in a page-owned buffer, from
  [the allocation follow-up](PAGE_ROWS_2026-10-22.md).
- Both compile the identical `BenchmarkPageLatency` body. A tiny test-only
  access shim maps each representation to its real row/key access; disassembly
  and profiles confirm the row shim and B's row method are inlined at the
  inspected call site.
- CPU fixture: 128 sorted records, 384-byte live values, every eleventh record
  a tombstone. Independent full row/key/value/deletion checks run before/after
  each sample; a digest or exact seek-position check consumes timed results.
- CPU phases: checksum control, decode into reused metadata, visit decoded
  rows, seek keys, and decode plus visit. These allocate zero bytes per operation.
- Warm I/O control and reused read/checksum/decode use the unchanged original
  51,328-byte all-put page fixture. Integrated SQL scan uses the unchanged
  1,024-row, 17-generation fixture with per-operation result validation.

Eight adjacent trial pairs use AB, BA, BA, AB, repeated. Each trial launches
separate CPU, I/O and SQL benchmark processes: 48 unprofiled processes total,
one sample per case/process. CPU/I/O samples request 1 second, SQL 2 seconds,
GOMAXPROCS 4. All 128 samples are retained. No own build, test or profile process
overlaps these measurements. CPU profiles are collected afterward.

Windows/amd64, Intel i7-11850H, Go 1.26.0. CPU affinity/frequency and the power
plan were not changed; this is not a dedicated, controlled performance host.

| Case | A median us/op | B median us/op | Median paired change | B slower pairs |
| --- | ---: | ---: | ---: | ---: |
| Checksum control | 1.668 | 1.687 | +6.63% | 5/8 |
| Decode reuse | 1.436 | 1.3255 | -9.43% | 3/8 |
| Row visit | 1.818 | 2.1175 | +25.37% | 6/8 |
| Key seek | 0.06087 | 0.06124 | +9.45% | 5/8 |
| Decode + visit | 4.346 | 3.964 | -9.66% | 1/8 |
| File read control | 3.8815 | 4.3675 | +10.25% | 6/8 |
| Reused read/checksum/decode | 7.547 | 8.0185 | +1.49% | 4/8 |
| Native SQL scan | 3,661.4545 | 3,393.1945 | -10.88% | 2/8 |

Positive changes are slower. The paired column is the median of eight B/A
ratios minus one, not the ratio of the displayed medians. Each timing is an
average operation cost within a process; the displayed median is not request
p50/p95 latency. Ranges, geometric means and every pair are in `assessment.json`.
The ratios are descriptive, not confidence intervals or an automatic release gate.

Material outliers remain: A SQL scan in `r12-before` is 25.73045 ms/op, and B
row visit in `r13-after` is 4.976 us/op. Nothing was trimmed or rerun until green.
Controls also drifted. Do not normalize other timings by the checksum control,
multiply unrelated phase ratios, or attribute the complete row-only difference
to one instruction. The combined phase must be measured, not inferred by adding
separate decode and visit medians.

## CPU Attribution And Rejected C

Separate standard benchmark CPU profiles put B's inlined `rowPage.row` at
9.78% of sampled decode-plus-visit CPU. Its row access contains an index check
and four slice-bound checks at the inspected Go 1.26 Windows call site. This
identifies a real reconstruction cost but does not convert that percentage
into a normalized per-query comparison with A's profile.

C first clipped the data length to the validated row end, then constructed
value and key views. This preserved bounds safety, backing storage, lengths,
capacities, metadata and the disk format. The inspected call site retained
the index check and emitted three slice-bound checks instead of four.

Four additional B/C pairs used BC, CB, CB, BC, with no concurrent own
build/test/profile work. C passed the focused data/corruption/ownership tests,
but the performance probe rejected it:

| Case | B median us/op | C median us/op | Median paired change | C slower pairs |
| --- | ---: | ---: | ---: | ---: |
| Checksum control | 1.6775 | 1.7495 | +5.39% | 3/4 |
| Decode reuse | 1.308 | 1.217 | -12.29% | 1/4 |
| Row visit | 2.1295 | 2.398 | +12.80% | 4/4 |
| Key seek | 0.054795 | 0.058375 | +9.84% | 3/4 |
| Decode + visit | 3.7025 | 3.684 | +3.53% | 2/4 |
| Native SQL scan | 3,634.6185 | 3,685.151 | -7.80% | 1/4 |

Fewer branches alone are not sufficient evidence of a faster implementation.
C is not retained. Its source, Go overlay, binaries, disassembly and all 48
samples remain under `refine/`; they are not relabeled as B results. B/C results
are a separate cohort and must not be multiplied into an invented A/C result.

SQL-scan profiles still show projection/decoding, predicates, allocation/GC and
Windows file/syscall work. They do not justify changing those subsystems in
this iteration. Standard benchmark CPU profiles include setup/calibration,
particularly SQL fixture construction; profiled timings are excluded from tables.

## Source And Verification

Local evidence: `.artifacts/kitdb-page-latency-20261022/` contains separate
A/B source manifests and binaries, fixture tests, 128 A/B samples, four CPU
profiles, attribution/disassembly, 48 B/C samples and the rejected C overlay.
Both snapshots use prior local candidate
`3ae270052df8a3a347f811599a24f677d0bf8b00` plus explicit hashed overlays. They
are not clean committed releases. The A/B benchmark body hash is identical;
only the documented access shim differs to accommodate the two private types.

The final source is B, not C: `kitdb/page_cache.go` was restored to SHA-256
`b0cf0b4a9f986e3bc59082eeea09f7acd349703738f96796cae42f840a29ed4f`.
Earlier allocation and lifecycle reports remain historical evidence, not new
results from this latency campaign. Final verification records distinguish
tests executed now from that previously passing broader matrix.

Final verification on restored B passed: full kernel package (212 root test/
fuzz-seed passes, no skips), three repeats of four selected page/fixture tests
under race (12 root passes, no skips), `go vet ./kitdb/...`, and integration
`go build ./...` on the frozen source. The fixture also passed three repeats
on each A/B source. The broader `kitdb/...` normal suite and lifecycle gate
from the allocation iteration were not rerun or relabeled as this campaign's
new results. This is not an all-engine test or full-suite race run.

No new production behavior, dependency, cache, format, WAL change, commit,
push, Linux run, 24-hour canary or stable qualification is introduced. The
earlier unresolved WAL-publication failure remains unresolved. The next
performance decision needs explicit workload priorities, including cached
row/key access versus decode-heavy reads; it must not assume one representation
wins every component simply because it allocates less.
