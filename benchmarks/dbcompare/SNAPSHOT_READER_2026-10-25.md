# Windows snapshot reader experiment: not adopted

## Decision

The ReOpenFile prototype is **not retained in the default runtime**. It improves
the short read-only and write-only batches, but mixed elapsed time is effectively
flat and mixed checkpoint service p99 worsens in five of eight pairs. That is
insufficient evidence for adoption given the checkpoint-tail objective, not
proof of a universal regression.

The default snapshot path is restored byte-for-byte to the prior
`os.Open(db.path)` implementation under the existing state/admission locks.
The native helpers and their Windows-only prototype tests are removed from the
shared source tree; they remain captured with replayable Go overlays in local
evidence. There is no new feature flag, public API, shared-reader refcount or
idle-handle pool. Two portable ownership regression tests are retained in
`kitdb/snapshot_reader_test.go`.

This follows [lock-holder attribution](LOCK_HOLDER_2026-10-25.md). Observing an
expensive phase identifies where to experiment; it does not prove that replacing
one API in that phase will improve end-to-end latency.

## Prototype and invariants

`DuplicateHandle` was ruled out before implementation because it shares file
position with its source. The prototype instead calls `ReOpenFile` through the
selected main generation's `SyscallConn.Control`, then wraps the new handle in
an independently owned `os.File`. Microsoft documents the former's shared
position and the latter's ability to reopen an existing filesystem object.
[DuplicateHandle](https://learn.microsoft.com/en-us/windows/win32/api/handleapi/nf-handleapi-duplicatehandle),
[ReOpenFile](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-reopenfile).

The acquisition remains inside the original locks. It requests read-only,
buffered synchronous access and retains read/write sharing without delete
sharing, preserving replacement exclusion. It does not use `File.Fd`, share an
`os.File` across snapshots, move acquisition outside synchronization, or change
Sync, cache budgets, worker counts or the storage format. The non-Windows
prototype helper simply retains the existing path-open implementation; it is
not a Linux optimization claim.

The native controls verify a missing diagnostic path does not select another
file, independent file positions under concurrent positioned/sequential I/O,
read-only access even from a writable source, survival after original-handle
close, closed-source/sharing errors, and replacement exclusion until close.
Capture failure must release admission; close failure must still unregister the
snapshot. A 1,024-acquisition loop checks explicit close and bounded process
handle growth with an eight-handle tolerance, not an exact global-handle oracle.

The retained portable handoff test disables the page cache, keeps eight
snapshots across checkpoint publication, verifies reads after the old main
reader closes, and verifies each snapshot handle closes separately. The second
retained test checks that close-error reporting still releases snapshot quota
and permits a subsequent capture. Existing full-compaction exclusion tests also
pass on the prototype and restored default.

## Source and measurement

Evidence: `.artifacts/kitdb-snapshot-reader-20261025/` in the engine repository.
The frozen base contains 12,006 verified files from the previous local
point-reader/lock-profile candidate, not the whole current dirty HEAD.
Six files are captured: four common helper/test files, the candidate snapshot
file and the previous profile-only QPC correction. The before/after Go overlays
share the same helpers, tests and QPC source; **only `kitdb/snapshot.go` differs**.
Both normal test binaries are hashed before workloads. For gates, GOFLAGS also
carries the candidate overlay into child Go builds.

The restored-default overlay contains only the retained portable test file and
the unchanged profile-clock correction. Final audit checks the immutable base,
captured prototype, both A/B overlays, restored default snapshot hash, absence
of rejected helpers, retained test hash and unchanged scoped foundation files.
This does not qualify unrelated compiler/UI work in the shared tree.

Windows/amd64, Go 1.26.0, GOMAXPROCS=4, GOGC=100, maximum Go memory limit.
All 34 workload processes use normal binaries, with neither runtime tracing nor
lock recording enabled. Setup, oracle checks, reopen and close stay outside
workload timing; no build, parser or inventory runs overlap those windows.
The workload reports were written between 12:29:38 and 12:31:26 on 2026-10-25
(Asia/Saigon); the largest consecutive report-write gap is 5.51 seconds. Later
analysis/continuation does not become an extra timed workload interval.

There are eight measured mixed pairs, three read pairs and three write pairs,
plus an excluded warmup pair per mode. Before/after order alternates. All modes
use 16 disposable databases, a 256 KiB/database / 4 MiB total page cache and two
maintenance workers. Mixed/write perform 1,024 acknowledged commits and 256
checkpoints. Mixed/read perform 3,072 point and 3,072 secondary-index reads.
Every process verifies 16 reopens. These are native SQL/kernel/node paths, not
pgwire, HTTP, another database engine or production application data.

## Normal results

Side columns are medians of processes. Change is the median of paired
percentage changes, **not the ratio of those side medians**. Total allocation
is not resident memory. Service timings remain the existing coarse node
duration; end-to-end checkpoint request timing uses QPC.

| Work/metric | Before median | Prototype median | Median paired change |
| --- | ---: | ---: | ---: |
| Mixed elapsed, ms | 1,528.55 | 1,484.40 | -0.33% |
| Mixed allocated, MiB | 921.88 | 921.57 | -0.050% |
| Mixed service p95, ms | 42.1570 | 38.4829 | -6.40% |
| Mixed service p99, ms | 67.3115 | 71.8663 | +8.25% |
| Mixed service maximum, ms | 83.9573 | 91.7905 | +7.05% |
| Mixed queue p99, ms | 254.9217 | 261.6098 | +4.71% |
| Mixed request p99, ms | 288.8612 | 302.1206 | +5.63% |
| Read-only elapsed, ms | 532.55 | 470.20 | -13.19% |
| Read-only allocated, MiB | 779.04 | 778.08 | -0.123% |
| Write-only elapsed, ms | 958.70 | 875.80 | -8.65% |
| Write-only service p99, ms | 14.2473 | 11.5423 | -19.01% |

Read-only elapsed improves in all three measured pairs. Mixed elapsed improves
in four pairs and worsens in four. Every mixed service-p99 pair is retained:

| Pair | Before p99, ms | Prototype p99, ms | Change |
| --- | ---: | ---: | ---: |
| 1 | 78.9842 | 72.1026 | -8.71% |
| 2 | 65.4832 | 82.4780 | +25.95% |
| 3 | 70.5137 | 68.4128 | -2.98% |
| 4 | 76.2568 | 65.4874 | -14.12% |
| 5 | 64.2733 | 78.9254 | +22.80% |
| 6 | 69.1397 | 93.8216 | +35.70% |
| 7 | 65.1883 | 71.6299 | +9.88% |
| 8 | 57.3389 | 61.1304 | +6.61% |

These are short, non-isolated Windows measurements with warm setup, not a
statistical equivalence test, cold-cache test or causal attribution of each tail.
No worker/cache/GC policy was tuned to make the prototype pass. No pair was
discarded or pooled with earlier campaigns to erase the unfavorable tail.

The earlier 1,024-iteration micro-probe measured path acquire/close at roughly
21.5-22.0 microseconds and pinned reopen/close at 21.0-21.7 microseconds. Its
three path runs precede its three pinned runs, so it is a smoke measurement,
not balanced evidence of a general speedup. It reports 376 versus 232 B/op and
three allocations/op for both. This small local gain did not justify adoption
after mixed SQL testing.

## Verification and disposition

Prototype evidence:

- Baseline common controls: nine root pass executions, one package.
- Candidate full KitDB/releasegate suite: 940 root passes, 19 root skips,
  14 packages.
- Candidate profile-tag race selection: 135 root pass executions over three
  repeats, two packages; this is not every package under race.
- Candidate profile controls: 45 root passes, two packages. Normal/profile vet
  and normal build pass.
- All 34 reports pass exact work/oracle/reopen and policy checks. Managers
  close; leases, open/warm/idle handles, waits, cache reservations and maintenance
  queues drain without reported open/maintenance/cancellation/rejection/close
  failures.

After removing the native prototype and restoring default snapshot capture:

- Full KitDB/releasegate suite: 933 root passes, 19 root skips, 14 packages.
- Default race selection: 68 root pass executions over two repeats, two
  packages. Default vet/build pass.
- The restored snapshot source exactly matches the frozen prior default; only
  the two portable tests and this report/index documentation remain as new
  shared-tree work from the experiment.

An initial helper test incorrectly required RawConn.Control's closed-file error
to unwrap as os.ErrClosed; its actual error is preserved, and the test now checks
failure without corruption classification. The first integration smoke did not
compile because syscall does not export ERROR_SHARING_VIOLATION; the test uses
Windows errno 32 instead. The failed smoke log and prototype note are retained;
the corrected smoke passes. These are prototype/test corrections, not hidden
benchmark retries. All timed A/B work ran once after source freezing.

No Linux run/install, new long canary, physical power-failure proof, power-plan
change, production rollout, commit or push. Stable and tail qualification remain
open. The next experiment should address snapshot acquisition frequency or an
explicit generation-lifetime policy, with bounded handles and close-error
ownership, rather than assuming another native API substitution will fix tails.
