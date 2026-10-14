# Authorization lifetime and immediate termination

Changing policy is not the same operation as terminating a live session.

## Effective Boundaries

- NOLOGIN/password rotation: future authentication uses the current enabled
  account/password. Existing data sessions are not automatically terminated.
- DROP USER: future login is denied; root grants/mappings are removed. Existing
  data sessions retain their captured policy until they end.
- Root permissions or role mapping revoke: pgwire sessions and native logical
  pools keep captured policy. Reconnect/recreate the pool for new policy.
  KitSQL authenticates each request.
- Local table privilege revoke: a fresh snapshot sees the revoke. An active
  transaction retains its ACL snapshot and can execute/commit authorized writes,
  subject to normal conflict, read-only and cancellation rules.
- DROP/recreate role: the replacement has a new role ID. Old membership cannot
  gain new rights; transactions already pinning the old role can still commit
  writes authorized by that old snapshot.

Prepared statements reauthorize at Execute using the execution snapshot. Revoke
blocks fresh autocommit statements, not statements in an already authorized
transaction. Revoke is not a commit barrier.

## Account Incarnations

Authentication stamps the account with a process-local root owner and account
incarnation. They are not persisted IDs, credentials, SQL columns or extra files.
The root owns a bounded map of current users; stamps do not survive replacing
the root owner. No on-disk auth migration is required.

Password/grant/LOGIN changes preserve the incarnation. DROP/recreate changes it
even with the same name and password. A stale session cannot enumerate the new
account's configured grants/members, use its recreated admin authority, or
see/terminate its sessions merely by matching username. The replacement cannot
use ordinary same-user session control on the old incarnation either. A current
enabled administrator can control both. Captured data policy remains separate.

Root SQL mutations carry the captured actor through identity revalidation under
the root mutex, including after password hashing. Unstamped UserInfo values and
root APIs accepting actor names remain host-trusted interfaces, not tenant login
APIs. Public Users metadata and JSON do not contain incarnation stamps.

## Immediate Session Termination

Use an independent, currently enabled administrator connection in autocommit:

```sql
ALTER USER reader NOLOGIN;
SELECT session_id, username, database_name, transaction_state
FROM kitdb_catalog.sessions WHERE username = 'reader';
KILL SESSION 123;
```

Use the returned session ID instead of 123, and terminate each intended session.
NOLOGIN blocks new authentication but does not cancel already admitted sessions.
An already authenticated transport may still finish admission: a strict cutover
must drain the listener/node, not treat one list query as an authentication
barrier. KILL targets one current physical session, not future connections.

KILL cancels the lifetime and asynchronously closes the session/transport. After
termination drains, its uncommitted transaction cannot commit and staged writes
are rolled back. Already committed data is not undone. A commit racing KILL may
publish before cancellation wins. ACK requests termination; it is not a
synchronous drain receipt. Observe the session disappearing for coordination.
IDs are node-local, not stable across restart.

The command requires autocommit, refuses the issuing session, and revalidates
enabled authority. Ordinary users may terminate other sessions of the same
incarnation, not same-name replacement accounts. Disabled/dropped users cannot
retain control-plane authority merely because their data session is open.

Native logical pools can create physical connections using captured policy.
Killing current physical sessions is not an account-wide invalidation of every
connector: the host must close/recreate its owned pools or drain the node.

## Verification

```text
go test ./kitdb/relational -run ^TestAuthorizationLifecycle -count=1
go test -race ./kitdb/relational -run ^TestAuthorizationLifecycle -count=1
go test -race ./kitdb/managed -run ^TestSessionAccountIncarnation$ -count=1
```

Tests cover native/loopback pgwire commit after revoke/mapping revoke/disable/drop,
KILL rollback, same-name user isolation, recreated admin authority, recreated
role IDs with pinned writes, and password rotation preserving incarnation.
No RLS, role inheritance, automatic disconnect, bulk kill or distributed revoke.
