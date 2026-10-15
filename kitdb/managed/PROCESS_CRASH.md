# Standalone restricted-write crash recovery

This opt-in campaign builds the real `cmd/kitdbpg` executable from the current
worktree, uses authenticated loopback pgwire clients and forcibly terminates
only child processes it started. Each case has a fresh temporary managed root;
existing demo servers and production databases are never selected.

## Run on Windows

The mandatory normal/race integration is also available through the existing
release runner:

```text
go run ./cmd/releasegate --mode kitdb-auth --report .artifacts/kitdb-auth.json --timeout 20m
```

That profile explicitly enables the workload, checks required test/subtest PASS
events and validates a fresh process report containing the server SHA256 and six
case outcomes. `KITDB_PROCESS_CRASH_REPORT` is an optional engine-relative JSON
path for standalone test invocations; the gate sets separate normal/race paths.
Its enclosing schema-3 report embeds the evidence rather than only linking a
mutable sidecar. See [releasegate](../../cmd/releasegate/README.md).

From the engine directory in PowerShell:

```powershell
$env:KITDB_PROCESS_CRASH = '1'
go test ./cmd/kitdbpg -run '^TestStandaloneRestrictedWriteCrashRecovery$' -count=3 -v -timeout=900s

$env:KITDB_PROCESS_CRASH_RACE = '1'
go test -race ./cmd/kitdbpg -run '^TestStandaloneRestrictedWriteCrashRecovery$' -count=1 -v -timeout=480s
Remove-Item Env:KITDB_PROCESS_CRASH, Env:KITDB_PROCESS_CRASH_RACE
```

The second variable builds the **child server** with the race detector too.
Without it, `go test -race` instruments the test harness, not the separately
built server. The campaign logs the executable SHA256 and child-race setting.
Child stderr is checked for race diagnostics even after forced termination,
which bypasses the detector's normal exit-status reporting.
Without `KITDB_PROCESS_CRASH=1`, the regular suite skips this process campaign.
It adds no production failpoint or dependency. Database atomic renames and
loopback sockets must be permitted by the execution environment.
Clients allow 15 seconds for authentication, including race-instrumented
password verification; server readiness has a bounded 30-second polling window.
These are test budgets, not a login-performance service-level objective.

## Exact fault boundaries

1. **Commit ACK:** a restricted writer inserts one receipt and one effect in a
   transaction. After `Commit` succeeds, the parent syncs its client ledger and
   kills the server without database cleanup. Both rows and values must survive.
2. **Staged batch:** both INSERT statements succeed inside a transaction, but
   COMMIT is never sent. Hard termination must recover neither row. Statement
   ACKs inside a transaction are not durability promises.
3. **Grant ACK:** UPDATE is granted to a database-local role and acknowledged.
   After termination/restart, the mapped writer must retain that permission.
4. **Revoke ACK:** a previously granted UPDATE is revoked and acknowledged.
   After termination/restart, even a zero-row UPDATE must return SQLSTATE 42501.
5. **Commit not forwarded:** the test-only single-connection bridge captures the
   client's simple-query COMMIT before forwarding it. The server is killed and
   the transport closed. The client must report a transport error, not success;
   recovery must find neither receipt nor effect.
6. **Commit reply withheld:** the bridge forwards COMMIT but captures the
   server's `CommandComplete(COMMIT)` before forwarding any commit response.
   Only then does the parent kill the server and close the transport. The client
   must report a transport error; recovery must find both durable rows.

The bridge parses bounded protocol frames and waits at explicit gates. Random
sleep-and-kill timing is not evidence that an ambiguous commit was exercised.
Every case verifies the same local role ID and its root account mapping after
fresh-process recovery with `-verify-on-open`.

## Reconciliation, Not Blind Retry

The parent owns a synced JSONL ledger outside the server. The application batch
uses a stable primary-key operation ID in `receipts`, in the same transaction as
the associated `effects` row. After an unknown commit result, a new authenticated
client reads that receipt: present means already committed; absent means retry
the batch with the same ID. Both controlled unknown outcomes must end with
exactly one receipt/effect of amount 17, including after another hard stop and
fresh-process restart.

This is a test of a caller's reconciliation pattern, **not** a new automatic
retry API or an exactly-once claim for concurrent/distributed callers. Reading
absence and retrying are separate operations; applications must retain unique
operation IDs, handle conflicting concurrent retries and re-reconcile subsequent
unknown results. No external side effect is made transactional by this test.

## Evidence Limits

Each run reaps fourteen owned child processes and drains the bridge goroutines
and sockets. Forced termination does not run the server's graceful cleanup, so
this test cannot assert `Closed=true` or empty in-process queues in the killed
owner. Graceful ownership/drain assertions remain in
[the seeded write campaign](WRITE_CAMPAIGN.md).

This demonstrates bounded SQL/local-role process-crash recovery on the tested
Windows filesystem. It is not physical power-loss, dishonest-fsync, Linux,
HTTP KitSQL, network-filesystem, prolonged canary or whole-engine qualification.
The separately built binary comes from a dirty worktree, not a clean release
commit. Passing these cases alone does not establish that KitDB is stable.
