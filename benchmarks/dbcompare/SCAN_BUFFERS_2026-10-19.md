# Relational Scan Buffers - 2026-10-19

## Change And Scope

The [noisy-16 investigation](NOISY_2026-10-19.md) identified page decoding as
the largest sampled allocation category, but not a repeatable planner
regression. This step uses a fixed-work fixture to reduce allocation in a
specific path: forward relational KROW scans with no own-write overlay.

The kernel already offers callback-scoped Snapshot.Scan with private reusable
page/merge buffers. Transaction.Scan formerly used an ordinary copying cursor
and then copied its returned row again. It now delegates eligible scans to
Snapshot.Scan and makes one caller-owned key/value copy at the public visitor
boundary. Public visitors may still retain or mutate those bytes.

The final fast path requires an explicit canonical or shadow KROW prefix,
forward direction and an empty relational write overlay. Reverse scans,
secondary-index/metadata ranges, prefix-less scans and own-write merges retain
the ordinary path. Read dependencies are recorded before dispatch. LIMIT stays
at the relational visitor boundary; errors, early stop and snapshot visibility
remain unchanged. Borrowed kernel slices never become public relational results.

No new API, cache, pool, mutable shared buffer, worker, codec or durability
policy is introduced. Existing kernel scan buffers are reused; no stdlib-external
dependency was added. Only relational/transaction.go changes production logic.

## Fixed Work

BenchmarkFixedReadWork creates fresh disposable databases with 1,024 logical
rows, 512-byte payloads, sixteen deterministic 32-row updates and a checkpoint
after the initial load and each update. There are no writes or checkpoints
during measured reads. The resulting version model and query schedule are the
same on both implementations; fresh database identities are not bitwise clones.

Cache is 256 KiB, snapshot/write budgets are 8 MiB, GOMAXPROCS=4. Each case has
exactly 128 measured operations and three repetitions. SQL point/index cases
repeat a fixed 64-step schedule twice: point id=(step*37)%1024; index bucket=step.
All queries include parse/bind/execute/result creation, not prepared statement
or result-cache reuse. Model validation and small expected-vector construction
are included equally in both binaries' timings/allocations.

Cases are a full forward/reverse Transaction.Scan, a SQL full row scan on
unindexed version>=0, point lookups, and index pages ordered by id LIMIT 16.
Raw scan rows are checked against canonical KROW encoding of the independent
version model; every SQL row's identity, version, bucket, payload and expected
order/count are checked independently of SQL execution. Query output is not
validated merely by comparing the two implementations to each other.

An extra full scan outside timing reports page counters on the immutable view:
both implementations read 26 pages / 892,800 bytes and decode 1,536 physical
records to return 1,024 logical rows. Those counters describe the separate
instrumented traversal, not instrumentation inside the timed ordinary Scan.
This is less copying/allocation for the same full range, not an I/O-pruning win.

The first final pair runs before then final-after; the second reverses order,
each in fresh processes. No own build, test, profile or analysis job overlaps
latency measurement. Other desktop activity and OS cache are uncontrolled.

## Fixed-Work Results

| Case | Before B/op, approx. | Final after B/op, approx. | Before objects/op | After objects/op |
| --- | ---: | ---: | ---: | ---: |
| Forward Transaction.Scan | 2,318,700 | 1,123,030 | 4,226 | 2,153 |
| SQL full scan | 3,493,750 | 2,297,600 | 19,284-19,285 | 17,209-17,210 |
| Reverse Transaction.Scan, control | 2,318,730 | 2,318,730 | 4,230 | 4,230 |
| SQL point schedule, control | 43,300-44,400 | 43,300-44,400 | 144 | 144 |
| SQL index schedule, control | 534,550-534,660 | 534,550-534,660 | 505-506 | 505-506 |

Forward relational scan allocation falls about 51.6%; SQL full-scan allocation
falls about 34.2%. Buffer reuse avoids repeated page allocations and the new
visitor boundary removes redundant row copies. Caller-owned copies remain:
this is not a zero-copy public Scan or an index-only SELECT.

| Median time, three samples | Before-first pair, before -> after | After-first pair, before -> after |
| --- | ---: | ---: |
| Forward Transaction.Scan | 1.123 -> 0.766 ms | 1.085 -> 0.774 ms |
| SQL full scan | 3.771 -> 3.096 ms | 3.593 -> 3.568 ms |

The forward timing improves in both short sweeps. SQL scan timing ranges
overlap, and unchanged reverse/point/index controls also vary. Do not attribute
every timing difference to buffer reuse or present this as a universal SQL
speedup, a production percentile/SLA or proportional peak-RAM savings.

## Retained Broad Trial

An earlier trial applied the fast path to all forward scans without own writes,
including index/metadata ranges. It produced the same full-row allocation win,
but the short index schedule barely saved bytes (about 534.6 -> 534.0 KB/op)
and had worse observed timings in both short sweeps. These observations do not
prove a causal index regression. They justify keeping this optimization narrow
rather than claiming a benefit where none was established.

The broad trial's binary, raw fixed-read/reverse logs and exact transaction.go
source copy remain in r2. It is superseded by the KROW-only r3 implementation,
not silently discarded. The final comparison uses r1 versus r3 and the exact
same benchmark source/body. Later correctness tests do not change that body.

## Mixed Regression Checks

Resource/durability settings are unchanged from the earlier campaign: fresh
native relational databases, eight seconds plus in-flight drain, paced
readers/normal writers, one unpaced 32-row writer in noisy DB zero, two managed
checkpoint workers and 100 ms checkpoint cadence. No profiling is enabled.
These are variable-progress time windows, not fixed-work measurements.

| Case / pair | Order | Before index/s | After index/s | Index p99 bucket ms, before/after | ACKs, before/after |
| --- | --- | ---: | ---: | --- | --- |
| Noisy 16 / 1 | Before, after | 1149.4 | 1255.8 | 100/100 | 5566/5997 |
| Noisy 16 / 2 | After, before | 1289.3 | 1270.1 | 50/100 | 6064/5841 |
| Noisy 16 / 3 | Before, after | 1309.1 | 1297.1 | 50/50 | 6146/6073 |
| Balanced 4 | After, before | 380.9 | 351.6 | 50/50 | 1569/1472 |

Noisy pairs two and three have lower index rates after the change; pair two
has a worse p99 bucket. Balanced four also regresses in this pair. Preserve
those results. The workload is primarily point/index traffic and is not a
demonstration of a full-scan speedup. It neither establishes consistent mixed
improvement nor statistically excludes a regression. Do not assign a specific
OS/GC/scheduler cause without evidence or raise worker/cache limits to hide it.

All eight reports passed independent row/version/payload and every secondary
index verification after reopen, ACK/sync equality, and zero owned snapshot,
transaction, admission, pending-commit, handle/lease and maintenance charges.
The exact ACK sum is 38,728. Raw reports and derived hash validation are retained.
This does not classify all process OS handles or prove long-duration leak freedom.

## Correctness And Memory

New tests exercise retained caller-owned bytes across page reuse/reader close,
visitor mutation followed by Get, exact full-range page counters, visitor
errors, limits near page boundaries, checkpoint from a callback, close/rollback
from a callback, own-write/reverse fallback, concurrent scans with private
buffers, a large single-row page, and range-read conflict tracking. The allocation
gate caps the fixed forward scan at 2,300 objects (about 2,153 measured), not a
wall-clock threshold. A fixture test checks source transaction/generation/block
state and the empty overlay remain unchanged by full scans.

Reusable pages and merge scratch remain scan-local, not pooled or shared.
Capacity may retain a selected large page until the scan ends; lower TotalAlloc
is not necessarily proportionally lower peak memory. Snapshot/transaction
charges and limits are unchanged, and do not become a new whole-process cap.
See [memory ownership and exclusions](../../kitdb/MEMORY_BUDGET.md).

Completed gates on the final implementation:

- Full kernel/node/relational/KitSQL suites: PASS, 38.346s / 18.265s /
  143.935s / 56.350s.
- Focused race suites, count=2: kernel 13.052s, relational 31.598s and KitSQL
  6.311s, all PASS. Includes scan ownership, concurrency, read dependencies,
  snapshots, streaming and memory admission paths.
- Tagged checkpoint-publication crash and storage-I/O fault matrices: PASS,
  14.857s.
- Vet of kernel/node/relational/KitSQL: PASS.
- Whole-engine `go build -mod=readonly ./...` with GOPROXY=off: PASS.
- Host `TestKitDBDatabaseReleaseGate`: PASS, 3.584s.
- Separate evidence tests: PASS, 3.038s package time. The fixed view has 17
  segments, 60 total blocks, transaction 26 and zero overlay before/after reads.
  Its full KROW range reads the 26 pages above. The allocation gate measures
  2,152 objects (versus 2,153 in the benchmark harness), below the 2,300 ceiling.

The complete engine/UI/browser test suite was not rerun.
No Linux, clean-candidate release campaign, 24-hour canary, hardware power-loss
test or stable promotion is part of this iteration. No commit or push was made.

## Provenance And Reproduction

HEAD e89bd9824396a2684c7669a029e94d9b526f1910, dirty shared worktree. Earlier
kernel/prefix/range/SQL-order changes are present in every binary. No unrelated
UI/JIT edits were made for this work.

| Binary | SHA-256 |
| --- | --- |
| r1/read.test.exe, before | `6ca6c7f5794db8337fb70ccc5ffcfbdfc55afacb49082b8aa98fa3ddb115084a` |
| r2/read.test.exe, superseded broad trial | `1705608b62d156fa47ec91dae0b25f6adbafabd0e9c3d4902b00ad9ba563f5cb` |
| r3/read.test.exe, final KROW-only | `0227127b7131757e386374273ea735cc7f9d35c2c778bfbd48937cfd856fa6df` |

Raw evidence is in `.artifacts/kitdb-buffer-20261019-r1/`, r2 and r3. Final
fixed comparisons use r1/r3 `paired-fixed.log` and `final-reverse-fixed.log`.
Earlier `fixed-read.log` / `reverse-fixed.log` files are broad-trial/initial
diagnostics, not substituted for the final series. Mixed reports are
`noisy16-pair01..03.json` and `balanced4.json` in r1/r3. Test/build logs,
`evidence.log` and a derived validated summary are retained in r3.

The unchanged benchmark source SHA-256 is
`556ebc27292babc359822f42821e6424234c0f6fd470c31e91b19fc78a2c4076`.
From engine/, freeze each implementation into new output paths:

```powershell
go test -c -o .artifacts/fixed-new.test.exe ./kitdb/relational
& ./.artifacts/fixed-new.test.exe '-test.run=^$' '-test.bench=^BenchmarkFixedReadWork$' '-test.benchmem' '-test.benchtime=128x' '-test.count=3' '-test.cpu=4'
go test ./kitdb/relational '-run=^TestTransactionScan|^TestFixedReadFixture' -count=1
```

Use the existing mixed campaign controls, fresh report paths, and one selected
case per process for regression checks. Do not run tests/profiles concurrently
with latency measurements or silently replace failed/regressing attempts.
