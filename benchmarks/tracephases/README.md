# Checkpoint Phase Diagnostics

Diagnostic builds only: `-tags=kitdb_profile` enables fixed-name trace regions
for checkpoint lock admission, snapshot drain, execution, record counting,
append/compaction, generation open, WAL rotation and storage sync. Ordinary
builds compile these region hooks to no-ops. No new tenant API, storage format,
durability policy, background collector or metric registry is introduced.

Build and run the synthetic mixed campaign from `engine/`:

```powershell
go test -c -tags=kitdb_profile -o .artifacts/mixed.test.exe ./kitdb/relational
$env:KITDB_MIXED_CAMPAIGN = '1'
$env:KITDB_MIXED_SECONDS = '8'
$env:KITDB_MIXED_PROFILE_MODE = 'trace'
$env:KITDB_MIXED_PROFILE_PREFIX = "$PWD/.artifacts/checkpoint"
& ./.artifacts/mixed.test.exe '-test.run=^TestKitDBMixedCampaign/noisy_16$' '-test.timeout=2m'
go run ./benchmarks/tracephases .artifacts/checkpoint_noisy_16_1.trace
```

Parent directories must already exist. Files are created exclusively, never
overwritten. Use fresh prefixes or directories for another attempt. Profiles
are opt-in and run from after fixture creation until all admitted workload
operations finish, before drain verification/close/reopen SQL. The trace
summarizer streams the current Go tool's version-specific parsed debug output;
it rejects malformed/unmatched KitDB regions instead of reporting partial data.
Nested durations overlap: never add append/rotation/sync to execute as if they
were exclusive phases. `execute/kitdb.storage.*` includes only sync regions
nested on checkpoint goroutines, not concurrent commit fsyncs. WAL-header sync
and directory-sync calls are separate from generation-file sync. A Windows
directory sync can be a platform no-op; these are call durations, not hardware
device latency or evidence against dishonest fsync.

Run `cpu` and `alloc` as separate modes, using the same workload. For allocation,
subtract the pre-workload cumulative profile, not the whole-process after file:

```powershell
go tool pprof -top -alloc_space '-base=.artifacts/checkpoint_noisy_16_1.before.heap' .artifacts/mixed.test.exe .artifacts/checkpoint_noisy_16_1.after.heap
go tool pprof -top .artifacts/mixed.test.exe .artifacts/checkpoint_noisy_16_1.cpu
```

The profiles cover the whole process during that window, including GC, worker
goroutines, sampling and harness work. Allocation sampling is statistical; the
report's `TotalAlloc` delta is separate exact process accounting. CPU profiles
on Windows can attribute blocked native/syscall time to `runtime.cgocall`;
do not interpret that as measured disk latency or pure CPU computation.

Clear both profiling environment variables for latency runs. Reports also
split maintenance scheduler wait from service time using existing manager
timestamps. These coarse Windows intervals can quantize short phases to zero;
the enclosing request timer still uses QPC. Service time includes handle
acquisition/release, locks, drain, file operations and sync. Individual phase
percentiles are not additive and their maxima need not describe the same job.

None of this qualifies SQL/pgwire performance, hardware power-loss safety,
long-duration fairness, Linux behavior, or production readiness.
