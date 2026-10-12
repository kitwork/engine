# KitSQL Real TCP Load Campaign

## Scope

`TestKitSQLNetworkLoadCampaign` is an opt-in loopback HTTP/1.1 test over a real
TCP listener, not a mocked handler or an in-process SQL benchmark. It opens one
temporary KitDB owner and shares it with the native SQL and KitSQL adapters.
No existing application database is used. The listener closes after the run.

The fixture has 10,000 rows, a 104-byte text payload, and a secondary value index.
Five bounded phases exercise point reads (64 clients), independent mixed
read/write traffic (64), hot-row writes (64), complete 10,000-row streams (8),
and stream cancellation/admission overload (128). The shared relational engine
evaluates the real node checkpoint-pressure policy on SQL traffic; there is no
periodic checkpoint timer in the campaign. Bad credentials must be rejected.

`KITDB_NETWORK_PAYLOAD_REPEATS=1..64` changes the 13-byte payload repetition
count (default 8, or 104 bytes). The final memory-budget campaign used 64
repetitions (832 bytes) and 24 seconds per phase. See
[memory-budget evidence](../MEMORY_BUDGET.md) for results and scope. Drain
checks include snapshot/reservation bytes and frontend/kernel transaction bytes.

Only acknowledged writes enter an independent per-row counter model. Writes
are not intentionally canceled: ambiguous transport failures are unexpected,
not silently retried or counted as successful. Conflict and busy responses are
counted separately. Cancellation applies only to read streams.

After draining HTTP, the test checks every row against the model, checks each
secondary-index value group, requires zero active snapshots/transactions/queued
commits/native connections in use, verifies storage, checkpoints, verifies again,
closes, reopens, and repeats row/index comparisons. This is clean restart
evidence, not a new hard-crash or power-loss test.

## Reproduce

Run in the engine repository using PowerShell. Port 18195 must be free; the test
fails rather than taking over another listener. Use port 0 for an ephemeral port.
Report paths should be absolute because Go tests run in the package directory.

```powershell
$env:KITDB_NETWORK_LOAD = "1"
$env:KITDB_NETWORK_PORT = "18195"
$env:KITDB_NETWORK_SECONDS = "20" # per phase, five phases
$env:KITDB_NETWORK_REPORT = Join-Path (Get-Location) ".artifacts/kitdb-network-load.json"
go test ./kitdb/kitsql -run '^TestKitSQLNetworkLoadCampaign$' -count=1 -timeout=5m -v

$env:KITDB_NETWORK_SECONDS = "3"
$env:KITDB_NETWORK_REPORT = Join-Path (Get-Location) ".artifacts/kitdb-network-race.json"
go test -race ./kitdb/kitsql -run '^TestKitSQLNetworkLoadCampaign$' -count=1 -timeout=5m -v
Remove-Item Env:KITDB_NETWORK_LOAD
```

## Local Evidence

### Managed Pressure Campaign (2026-10-11)

`.artifacts/kitdb-network-pressure-20261011.json` uses report format
`kitdb-network-load/v2-pressure`: 24 seconds per phase, 832-byte payloads,
64 native connections, one managed kernel owner. It acknowledged 15,362 writes
and completed 7 pressure-triggered checkpoints during traffic. Every row and
secondary-index group matched the independent acknowledgement model before and
after reopen. No unexpected errors occurred; conflicts and overload rejections
remain separately reported. Active snapshot/reservation/transaction bytes,
waiters, pending commits and native connections in use drained to zero.

Sampled combined client/server Go heap was 2.54 MiB before, 70.93 MiB peak and
1.69 MiB after close/GC. These are not RSS or an engine-only memory budget, and
timings are not a controlled comparison with older runs.

The short race campaign (`kitdb-network-pressure-race-20261011.json`, 3 seconds
per phase) passed with 1,238 acknowledged writes and exact row/index/reopen
validation, but triggered **zero** checkpoints. It is race/drain evidence, not
evidence of checkpoint overlap; use the longer campaign and focused pressure
race tests for that boundary.

The longer race campaign (`kitdb-network-pressure-race-long-20261011.json`,
15 seconds per phase) passed with 5,269 acknowledged writes and 2 actual
pressure-triggered checkpoints, no reported races or unexpected errors, and
exact row/index/reopen checks. Focused native SQL/pgwire pressure, hard
admission and commit-outcome regressions also passed repeated race runs.

After adding owner validation and pre-snapshot writable-BEGIN admission, the
final 15-second-per-phase race run
(`kitdb-network-pressure-final-race-20261011.json`) passed with 2,520 acknowledged
writes and 1 pressure-triggered checkpoint, exact row/index/reopen data and no
unexpected errors. Runs shared machine resources with other verification work;
their request rates are not directly comparable. The complete relational and
KitSQL suites passed independently, as did whole-engine build and KitDB vet.

The complete release gate attempt is retained as
`.artifacts/kitdb-sql-pressure-20261011.json`. It passed kernel/node and search
steps, then failed the broad `work` suite including theme prepaint/CSP tests.
This attempt does not qualify the whole engine as green; concurrent UI changes
were left intact.

### Historical Timer-Based Campaign

2026-10-10, dirty Windows/amd64 checkout, Go 1.26.0, local filesystem, server
and clients in the same process. No TLS, WAN latency, other-machine client or
distribution qualification is measured. Each main phase requested 20 seconds.

| Phase | Requests | Success | Conflict | Busy | Attempt/s | p99 upper bound |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Point read | 125884 | 125884 | 0 | 0 | 6292 | 32.8 ms |
| Mixed independent | 47757 | 47757 | 0 | 0 | 2385 | 131.1 ms |
| Hot row | 23822 | 6906 | 16916 | 0 | 1190 | 524.3 ms |
| Large stream | 1089 | 1089 | 0 | 0 | 54 | 262.1 ms |
| Cancel/overload | 91820 | 36307 | 0 | 55513 | 4584 | 262.1 ms |

Total: 290372 attempts, 217943 successful operations, 28086 acknowledged writes,
and 33 successful background checkpoint calls. There were no unexpected errors.
Every large-stream operation consumes all 10,000 rows, not just its first row.
Hot-row successful write throughput was about 345/s; attempt throughput must not
be presented as successful write throughput. Busy responses are load shedding,
not useful completed queries. Percentiles use power-of-two microsecond histogram
buckets and include failures as well as successes; they are upper bounds, not
precise percentile samples.

Sampled Go heap peaked at 46.1 MiB, versus 1.35 MiB before traffic and 1.56 MiB
after close/GC. This includes the test client, server, and engine, is not process
RSS, and cannot prove long-duration absence of leaks. Peak goroutines were 653;
the kernel/native resource checks passed after drain. Data matched exactly
before and after reopen; storage Verify passed before/after the final checkpoint.

The subsequent three-second-per-phase race run passed with 2461 acknowledged
writes, no race report or unexpected errors, and complete row/index comparisons
before and after reopen. Its slower throughput is not a production benchmark.

Local reports (ignored artifacts):

- `.artifacts/kitdb-network-load-20261010.json`: 100-second main workload.
- `.artifacts/kitdb-network-race-20261010.json`: race + row/index validation.
- `.artifacts/kitdb-network-smoke-20261010.json`: original capacity failure below.

## Capacity Finding

The initial one-second-per-phase run used 64 native SQL connections. The kernel
allows at most 32 active snapshots per database, so mixed/hot/cancel traffic
received `KSQL_EXECUTION: kitdb: active snapshot limit reached`. All 1603
acknowledged writes still matched the model after reopen; it was a capacity
rejection, not demonstrated data loss. This failed report is retained.

The 2026-10-10 successful campaign limited the native pool to 24 connections, leaving
snapshot headroom; the HTTP handler still admits at most 64 requests, and the
client pool permits 128. Queuing and rejection are visible in measured latency
and busy counts. This configures the fixture, not a production-engine fix.
That workaround is historical. The current campaign uses 64 native connections
and the per-database reservation admission described below. The snapshot ceiling
remains 32; raising it without a memory budget is not the conclusion of this test.

## Snapshot Admission Evidence (2026-10-11)

The relational adapter now reserves capacity before taking its commit gate.
The kernel shares 32 slots between snapshots and reservations, admits up to 128
FIFO waiters, and bounds each admission wait to two seconds or the caller's
shorter context. No snapshot file or overlay is allocated while waiting.
Typed KitSQL errors distinguish conflict, busy/admission timeout, execution
timeout, and uncertain durability; the client does not automatically replay writes.

The 64-connection campaign ran all five phases for 20 seconds each on the same
Windows/amd64 and Go version as above:

| Phase | Requests | Success | Conflict | Busy | Attempt/s | p99 upper bound |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Point read | 36515 | 36515 | 0 | 0 | 1825 | 262.1 ms |
| Mixed independent | 25692 | 25689 | 0 | 3 | 1282 | 131.1 ms |
| Hot row | 32243 | 5733 | 26510 | 0 | 1609 | 131.1 ms |
| Large stream | 748 | 748 | 0 | 0 | 37 | 524.3 ms |
| Cancel/overload | 38439 | 22917 | 0 | 15522 | 1917 | 524.3 ms |

All 16,400 acknowledged writes matched the row and secondary-index oracles
before and after reopen; Verify passed. There were zero unexpected errors.
Snapshot, reservation, waiter, transaction, commit-queue and native-in-use counts
were zero after drain. There were 33 successful background checkpoint calls.
Go heap before/peak/after close+GC was about 1.38/37.28/1.65 MiB, with 645 peak
goroutines. These measurements include both client and server and are not RSS.

The new three-second-per-phase race run passed with 1,406 acknowledged writes,
zero unexpected errors, no race report, and exact row/index comparisons after
reopen. Separate HTTP tests hold all 32 snapshots to force admission timeout,
cancel a queued write, confirm neither rejected write was applied, then submit
a successful explicit retry.

This is correctness/capacity evidence, not a speedup claim: the pool size changed,
local machine load was not controlled, and other verification commands overlapped
parts of the run. Hot-row successful write throughput was about 286/s, not the
1,609/s attempt rate. Busy responses remain deliberate rejections, not useful work.

Reports:

- `.artifacts/kitdb-network-admission-20261011.json`
- `.artifacts/kitdb-network-admission-race-20261011.json`

Next qualification should cover sustained larger datasets, longer write phases,
TLS, Linux, explicit slow readers, and isolated client/server processes. The
existing streaming lifecycle suite separately tests stalled-reader teardown.
