# Transfer a database between managed roots

This procedure transfers one database, not the root's authority. A database's
folder name selects its logical name; its internal database, table and role IDs
remain unchanged. `shop` can be restored as `archive` in another root.

## What travels

- Canonical data, schema and internal database identity.
- Database-local role definitions, IDs and SELECT/INSERT/UPDATE/DELETE grants.
- The committed snapshot represented by the verified backup anchor.

Root `.catalog` registrations, login accounts, password verifiers, CONNECT and
database-wide grants, and account-to-role mappings do not travel. The destination
root decides who can connect and which local roles they receive. Its existing
administrators retain intentional root-wide access to newly attached databases.
Do not copy `.catalog` to transfer a single database.

## Verified Backup and Restore

Run these commands from the engine directory. This is an offline CLI example:
stop/drain the source node and destination node first. The CLI opens its own
source handle and cannot borrow an already serving node's owner. An online host
must instead schedule backup through its existing owner/manager; do not open a
second live handle or copy live files.

Use fresh backup and destination paths. If `db-b` already has a managed catalog,
skip `init-root`; never replace it.

```powershell
New-Item -ItemType Directory -Path ./backups
go run ./cmd/kitdb backup ./db-a/shop/data.kitdb ./backups/shop.kitdb

go run ./cmd/kitdb init-root ./db-b
New-Item -ItemType Directory -Path ./db-b/archive
go run ./cmd/kitdb restore ./backups/shop.kitdb ./db-b/archive/data.kitdb
go run ./cmd/kitdb attach ./db-b archive
go run ./cmd/kitdb root-catalog ./db-b
```

Backup publishes a verified anchor; restore verifies its new image before
publication and refuses to overwrite an existing destination. No history option
is needed when restoring exactly the anchor transaction. The backup represents
that snapshot, not subsequent source writes. Backup pins/history retention are
managed separately; do not delete recovery resources needed by another policy.

Attach exclusively opens and verifies storage before recording the original
identity. It does not create missing data. Missing/corrupt/busy folders,
redirected/unsafe paths and duplicate identities fail closed. Exact retries are
idempotent. Storage attach is not a promise that every relational schema or ACL
document is supported; normal first acquisition validates that layer too.

## Destination Authority

For a fresh root, initialize its administrator offline using a password supplied
through `KITDB_AUTH_PASSWORD` by your secret mechanism, not a CLI password flag:

```powershell
go run ./cmd/kitdb auth-init ./db-b admin
```

Skip this initialization when destination auth already exists. Start the
destination node normally. Connect as its administrator to database `archive`;
the example below assumes `reader` is a new destination account. If it already
exists, inspect its grants instead of recreating/resetting it.

```sql
CREATE USER reader WITH PASSWORD 'Replace-this-example-password';
GRANT CONNECT ON DATABASE archive TO reader;

SELECT role_id, role_name FROM kitdb_catalog.roles;
SELECT role_name, table_name, can_select, can_insert, can_update, can_delete
FROM kitdb_catalog.table_grants;

GRANT ROLE editor ON DATABASE archive TO reader;
```

`editor` must already exist in the restored database. Do not recreate it: that
would generate a different role ID. The account must have a CONNECT-only grant
for restricted local-role mapping; built-in reader/writer/developer and broad
READ/WRITE/SCHEMA are separate policies and cannot be mixed with that mapping.

Reconnect the reader's physical pgwire connection, or recreate its native pool,
after mapping. An existing authenticated session does not silently gain a new
root mapping. KitSQL authenticates each request. Verify allowed operations and
that unrelated tables remain inaccessible. Inspect session/configured policy:

```sql
SELECT username, table_name, policy_scope, can_select,
       can_insert, can_update, can_delete
FROM kitdb_catalog.effective_privileges;
```

A same-named destination user does not inherit the source user's password or
rights. If its destination grants intentionally already authorize the database,
those grants still apply; transfer does not revoke legitimate destination policy.

## Move, Copy and Rename

Backup/restore copies a snapshot. Source and destination can subsequently diverge;
equal internal identity does not make them synchronized replicas. Independent
clone identities, distributed sync and conflict resolution are not provided by
this workflow. The same identity cannot be attached twice within one root.

For a move, after the destination has been checked, stop/drain the source and
detach its old name offline. Detach removes that name's grants/mappings in the
source catalog, not the folder or account. It is not required for a snapshot copy.

```powershell
go run ./cmd/kitdb detach ./db-a shop
```

For a rename within one root, stop/drain, detach the old name, rename the complete
quiescent folder to a fresh safe basename, then attach the new name and explicitly
grant CONNECT/map roles for it. Do not rename a live or still-registered folder:
catalog registration and named grants do not automatically follow an OS rename.
Local role/table IDs and ACLs remain intact; root grants are deliberately cleared
by detach and must not silently move to a different logical name.

## Repeatable Evidence

```text
go test ./kitdb/relational -run ^TestLocalRoleTransferRenamedRootPGWire$ -count=1
go test -race ./kitdb/relational -run ^TestLocalRoleTransferRenamedRootPGWire$ -count=1
go test -race ./kitdb/relational -run ^TestLocalRoleOfflineFolderRename$ -count=1
go test ./kitdb/managed -run ^TestFolderAttachDetachTransferAndGrants$ -count=1
```

The relational test restores the same anchor as both `shop` and `archive` in
separate fresh roots, retains IDs and all four grants, exercises real loopback
pgwire clients, rejects source credentials, requires explicit mapping/reconnect,
checks wrong-name mapping, allowed writes and forbidden-table reads, verifies
source data stays unchanged, and rechecks data/roles/write permissions after
destination restart. It rejects restore overwrite and attach failures without
catalog/grant changes, including after catalog reopen. Tests use isolated roots;
the offline rename test additionally performs detach, an actual folder rename,
attach and explicit remapping while preserving data and all four local grants.
no existing application database is modified. Windows evidence is not a full
release, Linux, physical power-loss or distributed-sync qualification.
