# Bounded lock-holder diagnostics

This is a `kitdb_profile` build facility for disposable diagnostic campaigns,
not a production metrics API or a storage-policy setting. Ordinary builds have
empty recorder/span types, no timing calls and no retained sample buffer.
Their lock wrappers are covered by a zero-allocation test; this does not assert
zero instruction overhead.

```go
// Build/run the host or test with -tags kitdb_profile.
db, err := kitdb.Open(path)
// Check err and retain normal DB ownership/Close discipline.
err = db.EnableLockProfile() // once, before workload goroutines start
// Run a bounded workload, then join its goroutines.
report := db.LockProfile() // caller-owned copy; serialize outside timing
```

Only diagnostic builds expose these two methods. Enabling a second time fails:
epochs cannot be reset under in-flight spans. A recorder belongs to one DB
handle, never a process-global path map. Its backing array holds at most 4,096
80-byte samples (320 KiB per enabled DB on amd64), allocated before the workload.
Overflow increments `Dropped`; qualification campaigns must reject dropped
evidence. Recording appends after `db.mu` is unlocked and does not call the
runtime tracer, format strings or write files. Timing and collection still
perturb scheduling and the live heap; compare profile-on/off controls.

Each sample uses monotonic nanoseconds relative to that DB's epoch:

Timing uses `internal/benchclock`: QPC on Windows, avoiding Go interrupt-time
quantization of short phases. These are still wall intervals, not CPU timings.
The clock/platform helper can allocate; the sample-buffer bound is not a bound
on all observer allocations or total process memory.

- `start_ns`: immediately before attempting the instrumented lock.
- `acquired_ns`: immediately after acquisition returns.
- `release_ns`: immediately before unlock.
- `unlocked_ns`: immediately after unlock, before collector synchronization.
- `transaction`: captured under the lock or supplied from the checkpoint's
  already-fixed boundary; reservation-only samples use zero.
- `snapshot_mu_open_clone_ns`: three non-overlapping wall durations for waiting
  on `snapshotMu`, opening the generation file and cloning the overlay map.
  They include scheduling/GC, not just CPU or storage execution.

`acquired - start` is wall time until acquisition, not a pure mutex-blocking
duration. `release - acquired` is an inner interval during which ownership is
certain; boundary timing overhead is not included. The unlock call lies between
`release` and `unlocked`. Collection time is excluded from these intervals but
can delay subsequent work. Never compare epochs across DB handles.

Covered sites are direct snapshot, reservation admission, admitted capture,
checkpoint generation-read work, incremental/compacted main publication, and
the detach/install state locks during WAL rotation. This is intentionally not
coverage of every `db.mu`, `commitMu`, snapshot, catalog or page-cache lock.
Uninstrumented holders and scheduling may explain residual acquisition time.
No lock order, Sync policy, worker limit or snapshot semantics is changed.

To investigate one checkpoint, match its database and transaction, then
intersect each writer acquisition interval with captured snapshot hold
intervals **from that same database**. Take their union, not their sum: readers
can hold an RWMutex concurrently. Such overlap proves observed reader ownership
during part of the writer's wall interval; it does not prove that every
overlapped nanosecond was spent blocked on that reader. Phase totals describe
work within each holder, not exact phase/writer-wait intersections.

The fixed-work relational test harness accepts `KITDB_LOCK_PROFILE=1` with
`KITDB_TAIL_WORK=1` and a profile-tag binary. It starts recorders after seeding,
collects after workload goroutines join, and rejects overflow. Setup, report
copying, validation and close/reopen stay outside workload timing. All ordinary
work/oracle/durability-count/drain checks remain enabled. Runtime tracing need
not be enabled to collect these samples.
