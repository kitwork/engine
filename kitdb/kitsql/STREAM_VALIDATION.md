# KitSQL Streaming Validation

## Scope

This suite checks the existing KitDB engine through native SQL and the KitSQL
HTTP adapter. It introduces no new wire version, transaction sessions, or
second database owner.

`network_stream_test.go` exercises real loopback HTTP/1.1 and TLS sockets:

- Early `Rows.Close`, request cancellation, and a client that stops reading.
- A four-MiB result and small socket buffers to force network backpressure.
- Database lease, native cursor, and admission-slot reuse after interruption.
- A paused TLS row delivery while native SQL updates and inserts records.
  The old cursor sees its captured snapshot; the next request sees the update.
- Graceful HTTP shutdown waits for an active request, then completes after
  client cancellation. The pause middleware preserves ResponseController.
- Native/remote value and column-type parity for int64 beyond JavaScript's
  exact range, fractional numbers, booleans, Unicode, escaped text, binary
  values, timestamps, NULL, empty results, and materializing SQL plans.
- Both adapters reject invalid SQL, unknown tables, and duplicate primary keys.
  Transport errors are not expected to have the native error identity.

The relational `TestSelectRowStreamShutdownWaitsForSnapshotRelease` separately
checks engine ownership: shutdown cannot close storage while a snapshot cursor
owns its read lease, and resumes when that cursor closes.

Go TLS can take up to five additional seconds to send `close_notify` after a
failed write. Tests allow this bounded teardown grace; they do not mistake it
for query execution time or promise an exact QueryTimeout handler lifetime.

## Reproduce

Run from the engine repository:

```sh
go test -race ./kitdb/kitsql -count=3 -timeout=3m
go test -race ./kitdb/relational -run TestSelectRowStreamShutdownWaitsForSnapshotRelease -count=3
go test ./kitdb/kitsql -run '^$' -bench BenchmarkKitSQLNetworkStream -benchtime=20x -count=1
go run ./cmd/releasegate --mode kitdb-verify --report .artifacts/kitdb-stream-20261010.json --timeout 20m
```

## Benchmark Sample

Local verification passed on Windows/amd64: KitSQL race suite repeated three
times, timestamp/Unicode parity and relational shutdown checks repeated three
times under race, and package vet. The final `kitdb-verify` run passed all eleven
steps in 142950 ms. Its JSON evidence is
`.artifacts/kitdb-stream-20261010.json` (local artifact, not committed). The gate
includes recovery, backup/PITR, standalone commerce and host database journeys.
The checkout was dirty; this is current-checkout evidence, not a release artifact.

2026-10-10, Windows/amd64, Go 1.26.0, i7-11850H, 256 rows, text payload
128 bytes per row except the original fixture row. Twenty iterations per
configuration, no network latency beyond loopback:

| Adapter / consumption | Total ms/op | First row ms/op | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: |
| Native / all rows | 0.816 | 0.158 | 381943 | 4003 |
| Native / first row then close | 0.193 | 0.100 | 138275 | 709 |
| TLS / all rows | 4.230 | 2.310 | 810802 | 9638 |
| TLS / first row then close | 2.629 | 2.598 | 379115 | 2324 |

First-row latency includes query setup and `Rows.Next`, not just receipt of
column headers. Rows are consumed without Scan in this transport benchmark.
The first-row-only TLS case may create fresh connections because early body
close prevents keep-alive reuse. Both adapter modes use the same storage engine.
TLS uses deliberately small client socket buffers, as in the lifecycle tests.

Allocation counters include work by the in-process HTTP server. B/op is total
allocated memory, not peak resident RAM, retained snapshots, or a memory-growth
proof. This short sample was collected while other verification was running;
it is a reproducible smoke measurement, not a performance regression threshold.

## Remaining Qualification

The opt-in [real TCP load campaign](NETWORK_LOAD.md) adds sustained concurrent
traffic, hot-row contention, cancellation overload, concurrent checkpoints and
an exact acknowledged-write model. Its capacity findings and local results are
separate from the small stream benchmark above.

The local release gate, race tests, and bounded lifecycle checks do not replace
Linux CI, HTTP/2 coverage, long-duration concurrency soaks, sustained WAN tests,
or power-loss qualification. Streaming applies only to non-materializing plans;
JOIN, aggregation, DISTINCT and uncovered ordering retain bounded materialization.
