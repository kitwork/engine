# Incremental Checkpoint Reader Handoff

## Decision

Keep the narrow append-handle handoff. It removes a measured redundant main-file
open without removing a Sync, changing the storage format, increasing the two
maintenance workers, or adding a public API. The fixed-work Windows comparison
improved in all four measured pairs. This is not stable/release qualification.

## What Changed

Previously an incremental checkpoint opened the main file read/write, appended
and synced the generation, closed that handle, then reopened the path read-only
to construct the next live image. The new path transfers the already-synced
append handle to that image. It remains read/write internally, but main-image
reads use ReadAt and later appends still acquire their own writer handle.

The constructor still verifies the pinned handle's regular-file type and minimum
size. It copies generation/block metadata and directly retains the previous
bounded page cache, instead of allocating a cache that installation discarded.
On construction failure after slot publication, the append handle closes and the
live DB becomes unavailable. Snapshots retain their own separate handles.
Compaction still closes/replaces the main file through its existing path. Restore
uses the same ownership transfer; its disabled page cache remains disabled.

Only three production paths differ: `kitdb/generation_writer.go`,
`kitdb/checkpoint.go`, and `kitdb/restore.go`. One existing torn-slot test now
closes the returned image it intentionally does not install; a new test file
covers ownership, snapshot/cache continuity, pinned-file construction, invalid
files, metadata isolation, close and reopen.

## Measurement

Windows/amd64, Go 1.26.0, GOMAXPROCS=4. Frozen sources start from commit
`4d36264cbd102a162f1b54908df29f53b422080e` plus the same previously reviewed
SQL/projection and Windows WAL fixes. Both sides include the same diagnostic
subregions. The shared workspace HEAD is not qualified by this experiment.
Complete before/after source inventories were revalidated after testing:
11,992 / 11,993 files. No unrelated VM, UI or background changes are included.

The existing fixed-work campaign is unchanged: 16 databases, 1,024 seeded rows
per database, 512-byte payloads, 64 durable one-row writes per database, a
checkpoint every four writes, and independent unpaced readers. Each process
requires 1,024 acknowledged commits, 3,072 point reads, 3,072 index reads, 256
advancing checkpoints, 16 verified reopens and fully drained owners. Read/write
interleaving is not fixed, so physical page/cache work need not be identical.
All timed checkpoints are incremental; full compaction is tested separately.

One warm-up pair is excluded. Four alternating-order pairs remain, with no
outlier removal. Setup, validation and close/reopen are outside workload timing.
The table shows the median of four process summaries; checkpoint/service/queue
rows are medians of per-process medians, not a pooled latency distribution.

| Metric | Before | After |
| --- | ---: | ---: |
| Fixed-work wall time | 3,171.43 ms | 2,305.81 ms |
| Checkpoint request | 157.34 ms | 96.47 ms |
| Maintenance service | 19.92 ms | 12.66 ms |
| Maintenance queue | 136.63 ms | 83.26 ms |

The difference of wall-time medians is **-27.29%**. Paired wall-time changes are
-29.70%, -26.38%, -27.46%, and -29.03%. Service median changes by -36.43%.
Queue and service timings overlap across databases; do not add their sums to
derive wall time. Readers may finish before writers.

Separate diagnostic builds show the reader-construction region median changing
from 7.306 ms to 0.027 ms. Before, its open subregion alone was 7.280 ms;
Stat was 0.022 ms and metadata 0.005 ms. After, Stat was 0.019 ms and metadata
0.005 ms. These inclusive spans contain scheduling time and do not establish
why Windows opens are slow. The append region now includes construction, so
its old/new inclusive durations are not directly comparable. Both traces
retain 256 checkpoint/rotation regions, 1,536 storage.sync regions and 256
WAL-header-sync regions. The Windows post-rename WAL Sync code is unchanged.

## Correctness And Limits

- Normal KitDB/releasegate suite: 914 passing root tests in 14 packages;
  20 skip events remain, including manual campaigns.
- Focused race selection repeated three times: 72 passing root executions,
  no skips, including snapshots, concurrent main reads, checkpoint admission,
  full compaction, restore, fixed work and ownership controls.
- Actual `kitdbfault` selection: 49 passing roots and four helper skips.
  Required parents pass: SQL checkpoint publication crashes, SQL storage-I/O
  matrix, WAL-I/O resource drain, Windows post-rename Sync failure and WAL
  publication crashes. Process crashes are not physical power-loss evidence.
- Vet for KitDB/releasegate and full frozen-source build pass.
- Six 16-database lifetimes per side: File handles stay at each process's
  baseline of 31 through all eight captures, no fixture-file handles are observed
  after close,
  and node/kernel ownership counters drain. One unresolved baseline File
  handle stays unchanged. Threads still grow at Go runtime start functions;
  anonymous Event/timer/wait objects are not individually attributed. This
  is not an absolute no-handle-leak claim or a waiver of prior review flags.
- Allocation totals do **not** demonstrate an improvement: process medians are
  2,667,016,304 bytes before and 2,668,246,780 after; these are total allocated
  bytes, not retained RAM/RSS. Scheduling/cache effects remain in this workload.
- The largest checkpoint sample is 501.74 ms before and 578.28 ms after. Keep
  that after sample. Better central timing does not clear a high-load tail SLA.
- No Linux execution, 24-hour canary, physical power cut, commit, push, power-plan
  change or whole-engine stability claim is included.

## Evidence

Local evidence is under `.artifacts/kitdb-generation-20261024/`: frozen sources,
`sources.json`, `source-revalidation.json`, alternating `r0..r4-*-fixed.json`,
raw traces, filtered regions, census JSONL, test logs, `assessment.json`,
`qualification.json` and `final-index.json`. Older campaign evidence is intact.

`run-initial.ps1` preserves two initial harness mistakes: a planned-step literal
of 23 instead of the actual 21, and the unrecognized `kitdb_faults` tag. Thus the
raw `execution.json` has a false aggregate flag despite 21 successful exit codes.
`post.json` verifies those exits and records a fresh real `kitdbfault` run,
required PASS parents, trace decoding and post-run source revalidation. The
incorrectly tagged ordinary subset is not counted as fault evidence.
`run.ps1` is corrected for a future fresh-directory reproduction.

The fixed workload can be repeated from either frozen source using
`KITDB_FIXED_WORK=1` and a fresh `KITDB_FIXED_REPORT` path:

```text
go test ./kitdb/relational -run ^TestCheckpointFixedWorkCampaign$ -count=1
go test -race ./kitdb -run ^TestAppendedGeneration -count=3
go test -tags kitdbfault ./kitdb/relational -run ^TestSQLStorageIOFaultMatrix$ -count=1
```
