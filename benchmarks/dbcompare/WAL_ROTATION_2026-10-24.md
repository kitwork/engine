# WAL Rotation Writer Handoff

## Decision

Keep the scoped writer handoff. Six fixed-work Windows pairs improve wall time
without removing a Sync, increasing the two maintenance workers, changing the
storage format, or adding a public API. Overall checkpoint-request tail summaries
improve, but service-tail summaries do not. This does not clear a tail SLA or
qualify KitDB as stable.

## Diagnosis

New diagnostic-only regions separate temp creation, permissions, header write,
header Sync, close, publication open/rename/Sync/close, active-writer close and
the final writer open. The before profile has 256 rotations with medians:

- Rotation: 11.489 ms; final writer open alone: 6.790 ms.
- Temp creation: 0.488 ms; chmod: 0.017 ms; header write: 0.102 ms.
- Header Sync: 0.960 ms; staging close: 0.103 ms.
- Publication open: 0.099 ms; rename: 1.067 ms; post-rename Sync: 0.927 ms;
  publication close: 0.163 ms; active-writer close: 0.079 ms.

These are inclusive elapsed regions from a separate trace build, not exclusive
CPU/kernel time. Do not add medians or attribute the slow open to antivirus,
NTFS or a device without separate evidence.

## Change

`prepareWALFile` shares header preparation and transfers its successfully synced
file to the caller. Ordinary WAL creation retains the existing close-before-
publication interface. Rotation instead keeps a rename-compatible prepared
writer, publishes through the existing helper, syncs the directory as before,
then installs that same writer at the exact header boundary. Every failure
before installation closes it. Publication uncertainty still makes the DB
unavailable; a known pre-publication failure still restores the previous WAL.

On Windows, the standard `os.CreateTemp` reserves an empty private file. While
that reservation still pins the name, a normal buffered read/write handle is
acquired with read/write/delete sharing. The reservation closes before header
writing begins. The retained writer is **not** the publication helper's
write-through handle: that separate handle still performs the post-rename Sync.
There are still three Windows opens; their ordering/lifetimes change, rather
than an open simply disappearing. The new preparation has one extra transient
handle during acquisition, bounded by concurrent rotations.

Delete sharing allows the held writer to follow rename, as documented by
[CreateFileW](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-createfilew).
It remains on the active writer after publication. The separate exclusive
database writer lock is unchanged and tested after rotation; arbitrary external
file replacement is not made supported. Real no-delete-share target readers
still deny publication. Unsupported-API fallback and denial classification are
unchanged. The raw `os.File` name remains its creation-time staging name.

Other platforms retain the prepared standard-library file through rename.
No non-Windows execution is claimed here.

## Fixed Work

Both frozen sources include the previously reviewed generation-reader handoff
and the same SQL/projection/WAL fixes, based on
`4d36264cbd102a162f1b54908df29f53b422080e` plus scoped overlays. The three
production differences are `wal.go`, `publish_wal_windows.go`, and
`publish_wal_other.go`. Two new test files and two additional SQL fault subcases
complete the six-path source difference. Benchmark implementation, dependencies,
fixtures, Sync policy and maintenance limits are identical. Full inventories
were revalidated after tests: 11,993 before / 11,995 after files. This does not
qualify the changing shared workspace HEAD.

Windows/amd64, Go 1.26.0, GOMAXPROCS=4. The unchanged fixed-work test uses 16
databases with 1,024 seeded rows and 512-byte payloads each. Every process must
complete 1,024 acknowledged one-row commits, 3,072 point reads, 3,072 index reads,
256 advancing checkpoints and 16 verified reopens, with all owners drained.
Readers are independent and unpaced; interleaving and physical cache/page work
are not fixed. All timed checkpoints are incremental. Setup, validation and
close/reopen are outside workload timing.

One warm-up pair is excluded by policy; all six alternating-order measured
pairs remain. Quantiles use nearest rank within each process's 256 checkpoints.
The table summarizes six process results by their median, not pooled quantiles.
The largest-sample row is the maximum over all 1,536 timed checkpoints per side.

| Metric | Before | After |
| --- | ---: | ---: |
| Fixed-work wall time | 1,980.01 ms | 1,507.84 ms |
| Checkpoint request median | 84.20 ms | 41.82 ms |
| Checkpoint request p95 | 275.79 ms | 255.59 ms |
| Checkpoint request p99 | 314.22 ms | 281.53 ms |
| Largest checkpoint sample | 428.30 ms | 323.14 ms |
| Service median | 11.09 ms | 6.56 ms |
| Service p95 | 41.99 ms | 43.17 ms |
| Service p99 | 61.64 ms | 63.71 ms |

Wall-time medians differ by **-23.85%**. Paired changes are -24.79%, -22.48%,
-20.78%, -20.53%, -25.12%, and -32.23%. Checkpoint-request p95/p99 summaries
change by -7.32%/-10.40%. Round 4 request p95/p99 get worse (+5.04%/+1.05%);
rounds 2 and 4 have worse maxima. No such samples are removed.

Service p95/p99 summaries are **+2.81%/+3.35%**, with overlapping sample ranges.
Do not claim an established service-tail improvement or statistical significance.
The before worst sample has queue/service of 300.43/101.71 ms; after worst has
259.79/61.93 ms. Coarse phases do not exhaust QPC request timing, and phases
overlap across databases; their aggregate sums are not wall time.

The after trace has rotation median 4.976 ms and prepared-writer median 1.925 ms.
There is no final writer-open region. Temp-create regions now include acquiring
the retained Windows handle; staging-close work moves inside preparation, so
these phase boundaries are not individually identical. Both traces retain 256
header Syncs, 256 publication Syncs, 256 directory-sync regions and 1,536 other
storage.sync regions. Timing traces are not the uninstrumented A/B results.

## Verification

- Normal KitDB/releasegate suite: 919 passing roots, 14 packages, 20 skip events
  including manual campaigns. Frozen-source full build and KitDB/releasegate vet
  pass. Existing full-compaction, snapshots, restore and WAL-sharing cases pass.
- Focused race selection repeated three times: 90 passing root executions,
  no skips. Writer ownership, cleanup, append position, exact published identity,
  close/reopen, held-writer fallback and extended paths are included.
- Actual `kitdbfault` run: 51 passing roots and four helper skips. Required
  parents include SQL publication crashes, the now-19-case SQL I/O matrix,
  kernel WAL-I/O drain, Windows post-rename Sync failure and publication crashes.
- Extra fault-path race: three repetitions of both the SQL matrix and Windows
  published-Sync failure, six passing root executions, no skips. Both new
  `rotate-open-writer` subcases must explicitly pass in every repetition.
- An independent artifact-level race probe checks writer-lock exclusion after
  four rotations, history on/off, a Unicode parent directory, three repetitions.
  All pass. It runs against the frozen after source, not a different workspace.
- Six 16-database lifetimes per side: File counts remain at each process's
  baseline of 29 through all eight samples; no fixture-file handles are observed
  after close; manager/kernel counters drain. One unresolved baseline File
  handle remains unchanged. Go Thread starts still grow (10 before, 16 after);
  anonymous timer/event identities are not individually proved. This is bounded
  file-ownership evidence, not an absolute no-leak guarantee or a prior-flag waiver.
- Allocation medians do not show improvement: 2,668,530,020 / 2,670,047,356 total
  allocated bytes per process; malloc counts 2,642,784 / 2,646,656. These are not
  retained RAM or RSS. Do not promote this as a memory optimization.

No Linux execution, physical power-loss test, 24-hour canary, new dependency,
commit, push or machine power-policy change is included. Better checkpoint-
request summaries do not settle the remaining service-tail question.

## Evidence

`.artifacts/kitdb-wal-rotation-20261024/` retains frozen before/after sources,
full source hashes and post-run revalidation, all warm-up/measured JSON,
per-process quantiles and worst samples, raw traces/filtered regions, handle
censuses, ordinary/race/fault logs, the independent lock probe, assessment,
qualification and a final SHA-256 evidence index. Older campaigns are intact.
`prepare.ps1`, `run.ps1`, `post.ps1`, and `analyze.ps1` are the recorded recipes.

For a fresh fixed-work result from either source, set `KITDB_FIXED_WORK=1` and
`KITDB_FIXED_REPORT` to a new file, then run:

```text
go test ./kitdb/relational -run ^TestCheckpointFixedWorkCampaign$ -count=1
go test -race ./kitdb -run ^TestWALRotation -count=3
go test -race -tags kitdbfault ./kitdb ./kitdb/relational -run "^Test(WindowsWALPublishedSyncFailure|SQLStorageIOFaultMatrix)$" -count=3
```
