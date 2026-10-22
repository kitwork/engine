# Write Pipeline And Snapshot Ownership: 2026-10-17

This is an incremental optimization on the current dirty engine tree, including
the earlier reusable KROW scan. It does not change any storage format, SQL API,
WAL write/sync order, commit validation, checkpoint algorithm or admission limit.
No production database was opened. All fixtures use disposable test directories.

## Measured Bottleneck And Change

The baseline allocation profile for 4,096 individual SQL INSERTs with a primary
and secondary index attributes 93.51% of cumulative allocated bytes to
`cloneSnapshotOverlay`. Each transaction copied the map **and every value** in
the still-live WAL overlay, even though published values never change in place.

Snapshots now copy map membership and tombstones, but retain references to
immutable, kernel-owned committed values. Kernel commit/recovery still copy
caller buffers before publication; updates replace the map entry. A snapshot
can keep older bytes after overwrite, delete, checkpoint and another snapshot's
close. `Get` and ordinary cursor results remain caller-owned copies; `Scan`
callback bytes remain explicitly read-only and callback-scoped.

Snapshot admission still charges the full retained key/value lengths for every
snapshot. Sharing does not reduce limits or charges. Closing one reader does
not clear bytes that another reader retains. There is no global pool, unsafe
row alias, unbounded version chain or dependency addition. This is **not** a
replacement of the copied-map snapshot design with a new MVCC architecture.

The after-profile still attributes 80.18% of its much smaller allocation total
to copying overlay maps. This identifies remaining work, not a reason to claim
that snapshot creation is now O(1). Profiles are sampled process totals,
including setup/verification; they are not per-query RSS measurements.

## Harness And Clock

Windows/amd64, Intel i7-11850H, Go 1.26.0, GOMAXPROCS=1. Timed benchmark
processes were run sequentially, without our test/build jobs overlapping.
Other desktop activity, OS cache, CPU power and device flush latency were not
controlled. These small synthetic workloads are not a production SLA.

The initial harness used `time.Now` for individual phases and quantized some
short Windows measurements to zero. Those phase measurements are superseded.
The final harness uses benchmark-only `internal/benchclock`: Windows QPC,
ordinary monotonic time elsewhere. QPC syscall timing and its allocations are
included in the instrumented paths; WAL phase data is not a zero-overhead
storage microbenchmark. These counters do not run in production commits.

For an exact harness match, the final baseline binaries use Go's `-overlay`
facility. The replacement file is a copy of the current `snapshot.go`, with
only `cloned[key] = mutation` restored to the former value-cloning expression.
The working source was never reverted or swapped while other work was active.
Both final binaries contain the same new benchmarks and prior KROW scan work.

## Snapshot Capture

2,048 live WAL-overlay entries; 20 captures per sample, three samples. Database
setup/initial durable commit is excluded. Each captured snapshot is closed.

| Value size | Original allocated bytes/capture | Optimized allocated bytes/capture | Original allocations | Optimized allocations |
| --- | ---: | ---: | ---: | ---: |
| 128 bytes | 492,465 | 230,321 | 2,064 | 16 |
| 4,096 bytes | 8,618,929 | 230,321 | 2,064 | 16 |

The 4 KiB-value case reduces cumulative allocation by **97.3%** and allocation
count by **99.2%**. The retained data is still reachable from the owning live
overlay/readers; these numbers are not a claim of a 97.3% reduction in resident
RAM. Map size, capture time and retained-data accounting still scale with the
entry count. The allocation envelope fails before the change (2,064, limit
100) and passes after it (16).

## SQL Ingestion

Each iteration opens a fresh database, creates a three-field table, inserts
4,096 explicit integer IDs with 512-byte text payloads and 64 categories, and
checkpoints after every 1,024 rows. Every serial transaction uses an ordinary
durable commit. Each case has two independent iterations per binary.

The primary-only profile still maintains KitDB's hidden ordered-primary path.
The indexed profile adds one ordinary category index. It is not a comparison
between an entirely unindexed table and an indexed one.

Reported QPC ingest time includes snapshot admission/capture, SQL parse/bind,
constraints, encoding/index staging and durable commit. It excludes setup,
parameter preparation, checkpoints and validation. Checkpoint time is shown
separately and must be added for a combined ingestion/checkpoint cost. Go
allocated bytes exclude those same checkpoint/setup/validation regions, but
include clock instrumentation. Verification and exact COUNT/SUM after close
and reopen run outside timing for every iteration.

| Profile / rows per transaction | Original ingest ms, both samples | Optimized ingest ms, both samples | Original allocated bytes, both samples | Optimized allocated bytes, both samples | WAL commit syncs per 4,096 rows |
| --- | --- | --- | --- | --- | ---: |
| Primary only / 1 | 2882, 3188 | 2155, 2501 | 1,782,679,064; 1,783,486,616 | 509,617,176; 507,953,568 | 4096 |
| Primary only / 128 | 81.87, 89.40 | 104.3, 89.71 | 73,451,680; 73,508,680 | 64,899,848; 64,728,232 | 32 |
| Primary + secondary / 1 | 3271, 3312 | 2563, 2618 | 2,042,001,040; 2,042,843,120 | 701,290,384; 699,918,296 | 4096 |
| Primary + secondary / 128 | 101.4, 97.52 | 96.68, 91.76 | 88,032,304; 88,029,440 | 78,848,848; 78,849,392 | 32 |

Individual indexed INSERTs allocate about **65.7% less**, and ingest time falls
by about 20-23% in these samples. Primary-only batch INSERT is **not faster**
in the final samples, despite lower allocation. Do not present this as a
universal write-throughput improvement. Batching also changes transaction
boundaries: 32 larger commits are not equivalent to 4,096 independent commits
for application latency or atomicity, even though each commit is still synced.

Final checkpoint totals for the indexed one-row workload are 71.68/73.58 ms
before and 72.78/70.97 ms after. The checkpoint path was not optimized here.

## Other Components

These characterize the same synthetic workload, not optimized code paths:

- Encoding a three-field KROW row: 976-1,072 ns/row, 2,792 bytes and 13
  allocations (10,000 iterations per sample, three samples).
- Generating index entries: 2,072-2,201 ns/row, 4,112 bytes and 37 allocations.
  This excludes constraint lookups, staging, WAL and sync.
- WAL frame encoding: one allocation; 640 bytes for one operation and 73,728
  bytes for 128 operations. The short 20-iteration timings are retained in the
  raw logs but are not used to claim an encoding improvement.
- A real instrumented single-operation WAL commit has 316-928 microseconds in
  `Sync` and 19-29 microseconds in `Write` across the final controls. Both
  binaries call the same real operations. Their timing variability is evidence
  against attributing all wall-clock differences to this snapshot change.
- Checkpointing 128 updates over an 8,192-row main file allocates about 240.8 KB
  and 577 objects per checkpoint in both binaries. The 20-iteration run reaches
  21 segments, below full compaction at 32. Times vary from 13.0-23.2 ms/op;
  there is no checkpoint speedup claim.

## Verification

`snapshot_overlay_test.go` proves caller-buffer isolation, copied Get/cursor
results, old values/tombstones after overwrite/reinsert/checkpoint, both fresh
and recovered WAL state, independent reader close, exact charge drain, and the
allocation envelope. Four independent databases also read old snapshots while
writers commit/checkpoint; each retains its own data and verifies/reopens to
the exact final value. This is concurrency **correctness**, not a fleet p99,
peak-RSS or long-duration leak benchmark.

All completed successfully:

```text
go test ./kitdb/... ./internal/benchclock -count=1 -timeout=10m
go test -race ./kitdb ./kitdb/relational ./kitdb/kitsql -run 'TestSnapshot|TestTransactionMemory|TestRelationalMemory|TestStandaloneTransaction|TestGroupCommit|TestKitSQLSnapshotAdmission|TestKitSQLSlowReaderMemoryAndCheckpoint' -count=3 -timeout=5m
go test -tags=kitdbfault ./kitdb ./kitdb/relational -run 'TestSQLCheckpointPublicationCrash|TestSQLHardCrashRecovery|TestGroupCommitSyncFailureIsResolvedByRecovery|TestRecoveryTruncatesEveryIncompleteTail' -count=1 -timeout=5m
go test ./work -run '^TestKitDBDatabaseReleaseGate$' -count=1 -v -timeout=5m
go vet ./kitdb/... ./internal/benchclock
go build ./...
```

The tagged tests hard-exit child processes and reopen their disposable files;
this is not physical power-loss or dishonest-fsync proof. The host gate covers
CRUD/index/transaction, restart, verify, backup, restore and restored queries.
No broad UI/browser suite, Linux gate, 24-hour canary or release promotion was
run for this change. Earlier clean-candidate qualification does not cover it.

## Evidence And Reproduction

Base commit: `a22edf8d3f677f7006d1bdff3ece61f164e40f8c`, uncommitted changes.
Final artifacts live under `.artifacts/kitdb-write-qpc-20261017/`:
`before-kernel.log`, `after-kernel.log`, `before-sql.log`, `after-sql.log`,
`row-index.log`, separate before/after CPU/allocation profiles, the source
overlay, binaries, and test/race/crash/host-gate/vet/build logs.
Earlier `.artifacts/kitdb-write-pipeline-20261017-*` runs are exploratory and
are not the final phase-timing evidence. These ignored artifacts are local.

| Final binary | SHA-256 |
| --- | --- |
| kernel-before.test.exe | `1f19e2b6e6a85043e0782c3934b361fc0c0a9260c4cb6275c9e03b21607dc01e` |
| kernel-after.test.exe | `229f79580e1b8a2ffc586da049a473bfe635acf1fb42a9b2abda16f437b48a70` |
| sql-before.test.exe | `80d94ba83cfb86377263af6740535410c8d051838aaa58212c9997136b4b30eb` |
| sql-after.test.exe | `57ecb0fea7e3bc24b15c4977d1e5d8a020e9b67ad3d2315cf7ba995ebf2264ad` |

From `engine/`, reproduce the optimized bounded workloads:

```powershell
go test ./kitdb -run '^$' `
  -bench 'Benchmark(EncodeTimedWALFrame|SnapshotCaptureWALOverlay|CheckpointOverlay128|DurableWritePhases)$' `
  -benchmem -benchtime=20x -count=3 -cpu=1
go test ./kitdb/relational -run '^$' -bench '^BenchmarkSQLWritePipeline$' `
  -benchmem -benchtime=1x -count=2 -cpu=1
go test ./kitdb/relational -run '^$' -bench '^BenchmarkRowEncodeAndIndexEntries$' `
  -benchmem -benchtime=10000x -count=3 -cpu=1
```

## Next Work

Avoid combining WAL/checkpoint redesign with the snapshot change. Remaining
targets are overlay-map capture itself, bounded group-commit behavior under
real concurrency, and checkpoint impact on mixed-workload tail latency and
peak memory. Profile those independently before changing ownership or lock
boundaries. No commit or push was performed.
