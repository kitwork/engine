# Secondary Tail Qualification Interrupted

## Decision

Withdraw the secondary-index duplicate-copy optimization from the default
source as a precaution. This is not proof that it caused the observed slowdown
or the WAL error. The six-pair A/B protocol did not complete, and a real Windows
WAL publication failure takes priority over another read optimization.

`kitdb/relational/transaction.go` is byte-identical to the pre-experiment
baseline, SHA-256:

```text
4F3B0EFC766680436EA2DBDCCB78C3F271A700A3AB8D5BB0ED95CB6C1643B4B9
```

Only the latest owned-copy experiment was removed. Earlier point-reader,
prepared-primary, snapshot/admission and durability work remains intact. No
WAL retry, sharing bypass, handle pool, cache-size change or OS policy change
was introduced. Ownership, cache, snapshot and checkpoint regression tests
remain; their allocation envelopes now describe the baseline (460 warm / 480
disabled), not the withdrawn prototype's stricter experimental limits.

## Fixed Protocol

Windows amd64, Go 1.26.0, GOMAXPROCS=4; disposable fixtures, no app database or
network service. A shared 623-file source capture builds normal and diagnostic
baseline/candidate binaries. Only `transaction.go` differs between variants.

Each normal process runs four independent epochs. Each epoch uses 16 databases,
128 writes and 512 point/index read iterations per database, checkpointing every
four writes. The existing bounds remain: 256 KiB cache per database (4 MiB total),
two maintenance workers, and unchanged transaction/snapshot admission budgets.

Every complete process must produce 8,192 ACKs, 32,768 point reads, 32,768 index
reads, 2,048 checkpoint samples and 64 reopen verifications with closed managers
and no remaining leases/cache reservations/maintenance work. Setup, explicit
pre-work GC, validation, serialization and close/reopen stay outside summed
workload timing. This is four fresh fixtures, not a sustained single-DB soak.

The protocol pools raw checkpoint samples per process before taking p99. It
does not average epoch percentiles. Three same-binary A/A pairs precede six
alternating-order A/B pairs. CPU/allocation/trace/lock runs are separate and do
not contribute normal latency evidence. The pre-analysis protocol is retained
locally, including its refusal to treat an A/A range as a confidence interval.

## What Actually Completed

All six A/A processes completed. The right-versus-left changes from identical
baseline executables were:

| Pair | Service p99 | End-to-end p99 | Summed workload duration |
| --- | ---: | ---: | ---: |
| 1 | -6.77% | -13.39% | -1.84% |
| 2 | -5.21% | -9.69% | -0.49% |
| 3 | -5.45% | -1.67% | -2.64% |

This demonstrates same-binary variation at the scale of the earlier 4-6%
observation. It does not prove equivalence or explain the earlier observation.

Only one A/B pair completed before interruption. It is unfavorable and remains
visible, but is not used as a six-pair acceptance result:

| Metric | Baseline | Candidate |
| --- | ---: | ---: |
| Summed workload duration | 11.4404 s | 24.6830 s |
| Service p99 | 64.8951 ms | 74.1148 ms |
| End-to-end checkpoint p99 | 273.2257 ms | 430.9294 ms |
| Allocated bytes | 10,055,566,136 | 9,973,664,472 |
| Allocated objects | 26,264,614 | 24,987,757 |

Collection stopped at `ab-2-baseline`, epoch 02: Windows refused replacement of
`db_11.kitdb.wal` with `Access is denied`. Nine normal processes completed, one
failed, and eight planned processes were not started. No complete A/B median,
regression verdict or latency qualification is claimed. The failed run used Go
temporary-directory cleanup, so its original fixture is unavailable; no ACK
recovery claim is made for that particular fixture.

## Independent WAL Forensics

A separate existing forensic campaign used the same baseline executable and
a fresh OS-temp fixture root. It was not a retry of the failed checkpoint or
a replacement performance run. One 30-second warmup completed; the next failed
after about 21.5 seconds with another WAL publication denial, this time database
4. The full forensic test exited failed after 54.71 seconds.

The error was observed before owner cleanup. Restart Manager listed `System`
(PID 4) and the current baseline test executable as resource users. This is
best-effort post-error observation, not identification of the blocking handle,
its share flags, a particular driver, or an antivirus cause. Publication cleanup
and old-WAL recovery may already have changed which handles remain observable.
No process was killed, restarted or excluded from security scanning.

After every owner closed, the diagnostic copied the failed fixture and verified
all 16 databases against the independent per-tenant ACK model: 19,261 acknowledged
transactions, including the noisy writer's batch-32 updates. The original file
digests remained unchanged. Resource ownership drained (closed manager, zero
open handles/leases/cache reservations/queued or running jobs), while the one
failed maintenance operation remained recorded as a failure.

This is bounded recovery evidence, not a passing campaign or a universal claim
against lost ACKs, disk failure or power loss. Both original and verified-copy
fixtures were additionally preserved under the workspace: 96 files with checked
SHA-256 hashes. The original temp data was not deleted.

## Independent Attribution

Eight separate one-epoch attribution processes completed: baseline/candidate
CPU, before/after allocation profiles, runtime traces, and bounded lock profiles.
Their timings never replace normal-build results or the failed qualification.

The workload-only sampled allocation difference attributes about 61% of allocated
bytes directly to `mainImage.readPageInto` (baseline 1,451 MiB; candidate 1,423 MiB).
These are cumulative sampled allocation volumes, not resident RAM or evidence of
a leak. Page-buffer allocation remains a much larger target than the removed
duplicate copies.

Runtime syscall traces contain snapshot file-open and file-sync delay. Aggregate
goroutine delays overlap; they are not additive process wall time. Likewise the
Windows CPU profile's large syscall/cgocall category is not proof that the same
fraction is hardware CPU work or physical-disk latency.

The baseline lock diagnostic records checkpoint-install acquisition p99 of
28.63 ms versus a held interval p99 of 10.6 microseconds. About 662.5 of 689.5 ms
of aggregate install acquisition intervals overlaps observed reader ownership
from the same database. Snapshot-open phases are prominent. This is observed
overlap under an intrusive diagnostic build, not proof that all that time is
blocked on a particular reader. Uninstrumented holders, GC and scheduling remain
possible contributors. No reader-lifetime or locking redesign is justified yet.

## Retained Harness

`TestSecondaryTailCampaign` is opt-in through `KITDB_SECONDARY_TAIL=1`; regular
qualification plans force it off. The bounded plan and fresh-root validation
have ordinary unit coverage. `KITDB_TAIL_REPORT` must be a fresh report file.
Optional `KITDB_TAIL_ROOT` must be an absolute, nonexistent directory and retains
fixtures on success or failure. Use it for fault investigation; the default
continues to use Go temporary cleanup. This retention option was added after the
original benchmark capture and was not present in the frozen timing binaries.

## Verification And Evidence

Local evidence lives in `.artifacts/kitdb-secondary-tail-20261026/`: protocol,
source/binary manifests, all attempted normal reports, failed-run logs,
`collection-status.json`, forensic observations and digests, preserved fixtures,
independent profiles and their analysis. Do not read the top-level
`verification.json` as a qualification pass: it records focused development
checks performed before the final conservative withdrawal.

The final restored source was checked separately under `final-verification/`:
focused harness/gate unit tests and focused race tests pass; `kitdb-read` passes
normal/race/profile with 33/33/36 required entries. The broad
`go test ./kitdb/... ./cmd/releasegate -count=1` run passes 955 root tests across
14 packages with 20 explicit root skips. Scoped vet/build also exit zero. These
development checks do not erase either real WAL failure or complete the aborted
A/B protocol, and are not full-engine or stable-release qualification.

The next priority is identifying the precise Windows publication failure phase
and the refusing handle/OS condition, while preserving the existing no-retry,
no-sharing-bypass and recovery rules. A successful forensic copy does not resolve
publication availability. Only after that should the identical A/A + A/B protocol
be run afresh. Linux/mobile, cold OS cache, wire protocols, a 24-hour soak and
production stability are not qualified. No commit or push was performed.
