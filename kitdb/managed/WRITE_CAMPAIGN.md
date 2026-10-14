# Seeded restricted-write campaign

This bounded correctness campaign exercises managed local-role writes through
two native clients and two actual loopback pgwire clients. It is not a speed
benchmark, long-running qualification or evidence of full PostgreSQL parity.

## Run

From the engine directory, using fresh temporary roots automatically:

```text
go test ./kitdb/relational -run ^TestRestrictedWriteCampaignSeeded$ -count=1 -v -timeout=240s
go test -race ./kitdb/relational -run ^TestRestrictedWriteCampaignSeeded$ -count=3 -v -timeout=240s
```

Seed 20261014 generates four workers with twelve operations each. It fixes the
operation list, not OS scheduling. Conflict counts can differ between runs.
Every operation must eventually receive a successful autocommit ACK within
64 attempts. Only explicit transaction conflicts (native conflict or SQLSTATE
40001) are retried with bounded backoff. Authentication, permission, network and
unknown-commit errors fail the campaign; they are not blindly retried.

## Checks

1. Concurrent upserts arbitrate the same primary row and another shared unique
   key. SQL expressions add seeded deltas to the stored value. All 48 commit ACKs
   must match exact sums [93, 50], with exactly two rows and the original ID of
   the unique-key row. No successful commit may disappear or be counted twice.
2. A source update commits after a copy transaction has pinned its snapshot but
   before INSERT SELECT reads. The copy must either commit a coherent old
   snapshot or fail with transaction conflict and publish no target rows. A
   conflict retry uses a fresh snapshot; no mixed source state is accepted.
3. UPDATE is revoked after a pgwire transaction stages an upsert. A statement
   beginning after revoke must fail with 42501, while the pinned transaction
   commits its authorized delta. The resulting ledger becomes [100, 50].
4. A native cancel and a pgwire KILL target two separate batches paused after
   staging an upsert and a copy, before commit. Those transactions must fail to
   commit, retain the ledger and leave zero discarded rows. Workers complete
   rollback/connection cleanup before signaling completion. This tests logical
   batch interruption, not interruption of a kernel fsync already committing.
5. Clients/listener/node are closed and checked for empty session registry,
   handles, opening/closing databases, waiters, leases, maintenance queues/work
   and page-cache reservations. Closed=true is mandatory. A fresh node then
   verifies the ledger, copy outcome, empty discarded table, unchanged role ID
   and SELECT/INSERT/UPDATE grants. The reopened owner must drain as well.

All data, users and role configuration are temporary. Restart here means closing
and reopening the node in the test process, not process-crash recovery or power
loss. Native/pgwire are covered; the stateless HTTP KitSQL adapter has separate
functional/race tests and is not claimed as a worker in this campaign.

## Limits

For real standalone-process termination and controlled unknown-commit recovery,
see [the process crash campaign](PROCESS_CRASH.md). It is a separate opt-in test,
not an implied property of the close/reopen checks above.

Passing this seed and bounded worker count is evidence for these invariants, not
proof for every scheduler, filesystem or workload. It does not qualify Linux,
distributed sync, network partitions, ambiguous commit retry, or prolonged
resource stability. Production paths/pools and currently running demo servers
are not selected by the test. Do not weaken a failing invariant into an allowed
outcome merely to make a future run pass.
