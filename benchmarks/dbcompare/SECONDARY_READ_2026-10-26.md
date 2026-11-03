# Forward Secondary-Index Scan Ownership

**Superseded decision:** the later [tail qualification attempt](SECONDARY_TAIL_2026-10-26.md)
was interrupted by a real baseline WAL publication error. The copy-elimination
prototype below has been withdrawn from the default source as a precaution;
ownership tests remain, with baseline allocation envelopes. The measurements
below are historical experiment results, not current default behavior.

## Decision

Retain a small allocation cleanup, not a new read architecture or a claimed
latency breakthrough. A forward relational index scan with no transaction-local
overlay still uses the original cursor. Its Key/Value methods already return
owned copies, so the relational visitor can receive those slices directly,
without cloning them again. Reverse scans, metadata ranges and overlay merges
keep their existing behavior, including the final visitor copy.

No durable encoding, commit fence, admission budget, cache size, file-handle
ownership, checkpoint policy or public API changes. The query-local PointReader
continues hydrating canonical KROW from the same snapshot.

## Attribution

An exploratory CPU/heap profile of `BenchmarkPointReaderSQL/large/hot_index`
showed index traversal going through a copying cursor, then copying its public
Key/Value results again for the relational visitor. The snapshot callback API
was initially tried as an alternative. The retained implementation instead
keeps the cursor itself unchanged and removes only the final duplicate copies.

Page allocation and row decoding remain much larger costs. This change does
not remove those costs, decode only index keys instead of authoritative rows,
reuse mutable results, or add a shared scratch pool. Profiles include fixture
setup and benchmark calibration; they are attribution, not A/B timing evidence.
An exploratory candidate profile accidentally used the process default of 16
processors; `candidate-4.*` is the replacement matching the baseline's four.
Neither profiled timing is used in the normal-build comparison.

## Workload

Windows amd64, Go 1.26.0, i7-11850H, GOMAXPROCS=4. Disposable databases only.
The working engine repository is dirty; this is development evidence, not a
qualified clean release candidate.

The isolated fixture has 1,024 rows, eight buckets, a composite `(bucket,id)`
index, and 512-byte text payloads. Each measured query cycles through one of
the eight buckets and checks all 16 returned rows against an independent
arithmetic oracle:

```sql
SELECT id,bucket,payload FROM items
WHERE bucket=$1 ORDER BY id LIMIT 16
```

Setup, seeding, checkpoint and cache warm-up are outside benchmark timers.
Each process executes 2,000 timed queries per policy, including the same oracle
on both sides. Warm uses a 256 KiB KitDB page cache; disabled uses `-1` and keeps
the existing bounded private PointReader behavior. This is **not cold OS cache**.
EXPLAIN checks 16 index entries, 16 hydrated rows and 16 point lookups. Warm
hydration hits all 16 cached pages; disabled hydration has no shared-cache hits.

Six unprofiled pairs alternate baseline/candidate order. Three separate mixed
pairs use the existing fixed-work campaign: 16 databases, 1,024 acknowledged
commits, 3,072 point queries, 3,072 ordered secondary queries and 256 checkpoints
per process. The total shared-cache budget is 4 MiB and maintenance concurrency
is two. Every mixed run verifies 16 reopened databases and a closed manager
with no remaining leases, cache reservation or maintenance work.

## Retained Cursor Comparison

| Policy | Baseline objects/query | Candidate objects/query | Baseline bytes/query | Candidate bytes/query | Median paired time change |
| --- | ---: | ---: | ---: | ---: | ---: |
| Warm | 435 | 403 | 71,722.5 | 70,186.5 | -1.975% |
| Disabled | 451 | 419 | 225,308.5 | 223,766 | -1.261% |

These are medians over six independent process pairs, not six samples from
one process. The repeatable result is 32 fewer allocations/query, about 7.4%
warm and 7.1% disabled. The small timing changes are not a general speedup claim.

Mixed paired medians: elapsed +2.51%, bytes -0.52%, objects -4.11%, checkpoint
service p99 +5.69%, end-to-end checkpoint p99 +4.12%. Allocation improves but
this batch has unfavorable elapsed/tail medians. These short, scheduler-sensitive
samples do not establish significance or clear mixed p99 for release. Retain
this only as a narrow allocation/ownership cleanup, not a throughput or tail
optimization; longer qualification remains open.

## Callback Experiment Not Retained

The wider callback-scanner variant saved 30 objects/query. Its initial six-pair
batch had effectively unchanged isolated timing (-0.114% warm, +0.044% disabled);
three mixed pairs gave elapsed -3.48% and service p99 -14.90%. A second batch
from frozen source instead gave -3.44% warm, +5.97% disabled, mixed elapsed
+0.98% and service p99 +27.22%. Both batches remain in the evidence, not just
the favorable one. With no consistent timing benefit, switching the iterator
is unnecessary for eliminating duplicate public copies. The production KROW
callback branch and index cursor selection have therefore been left as before.

## Regression Contract

`kitdb-read` now requires secondary ownership across page boundaries, visitor
mutation/error/close, LIMIT/OFFSET and reverse results, both cache policies,
cancellation, retained SQL results, old snapshots and own writes, phantom range
conflicts and concurrent checkpoint reads. The same normal/race/profile steps
also retain all prepared-primary contracts.

The 1,024-row fixture has allocation envelopes of 425 warm / 444 disabled,
with headroom for race instrumentation. A negative control uses Go's file
overlay to restore only the old scanner for testing, without modifying the
working source: both envelopes correctly fail (435 warm / 450 disabled).
Gate unit tests require each new root and both cache subcases, rejecting missing,
skipped or failed evidence even if the test process otherwise exits zero.

The focused gate passes all three steps, with 33/33/36 required PASS entries
(102 entries including root/subtest overlap and repeated build profiles, not
102 unique tests). No latency threshold is imposed in CI.

Final Windows verification on the retained source also passes
`go test ./kitdb/... ./cmd/releasegate -count=1`: 953 root test passes,
19 root skips and 14 passing packages. These skips remain explicit rather than
being counted as passes. Scoped `go vet` and `go build` both exit zero. This is
KitDB/releasegate verification, not a full `go test ./...` engine run.

## Evidence And Limits

Local evidence: `.artifacts/kitdb-secondary-read-20261026/` contains the initial
normal binaries, per-process logs/reports, profiles, the original transaction
source, negative-control overlay, scoped source capture, summary scripts and
verification output. `frozen/` is the callback variant; `v2/` is the retained
cursor-copy cleanup. Both use the same 622-file scoped source capture, with
Go overlays replacing only `transaction.go` for baseline/final candidates.
The capture includes local dependencies, tests/fixtures and module metadata;
binary, source and evidence hashes are recorded locally. This comparison does
not test pgwire/network latency,
Linux/mobile, cold OS cache, power loss, a 24-hour soak or production readiness.
No commit, push, Linux installation or sleep-policy change was performed.

Next attribution should target page-read/decoder allocation using the same
snapshot and cache constraints. The current evidence does not justify a reader
handle pool, shared mutable decoder, durable format change or storage-engine
replacement.
