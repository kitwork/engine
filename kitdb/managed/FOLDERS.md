# Managed Database Folder Contract

One managed database is one real folder under a root:

```text
db/
  .catalog/data.kitdb
  shop/data.kitdb
  blog/data.kitdb
```

The folder basename is the database name. The existing data file contains its
database identity, format version, schema and records. WAL, lock and supported
sidecars remain storage details, not separate databases. No second manifest or
schema copy is introduced in the root catalog.

The root catalog owns registration and connection accounts/grants. New
registration uses the same name and directory; historical catalogs with aliases
remain readable, but new Register(name, directory) calls reject unequal names.
Migrate an old alias explicitly while offline; no automatic rename occurs.

## Attach and Detach

Run from engine, with the root's node stopped:

```powershell
go run ./cmd/kitdb attach ./db shop
go run ./cmd/kitdb detach ./db shop
```

Attach requires an existing root/shop/data.kitdb, rejects redirected paths,
acquires exclusive database ownership, checks storage integrity and publishes
its existing identity. Unsupported format/open/verify failures do not register
anything. Relational schema loading still performs its normal validation at
first acquisition. Exact attach retries are idempotent. Duplicate identity,
folder or database names are rejected; replacing an existing registration is
not an implicit attach.

Detach removes registration and all per-account grants for that name in one
catalog transaction. It never deletes the folder or data and never drops the
account itself. Existing database files must be exclusively acquired; busy
folders are rejected. Missing files can be unregistered, and a replaced folder
can be detached to clear stale authority. Corrupt/unopenable existing files may
need operator repair before detachment; there is no force flag.

These are offline operator capabilities, not client schema privileges. A serving
node exclusively owns .catalog, so offline mutation cannot run while it is
serving, including with active transactions. Close clients and stop/drain the
node first. This does not introduce live attach/detach or forcibly cancel users.

## Transfer

See [TRANSFER.md](TRANSFER.md) for verified backup/restore, a renamed destination,
explicit local-role mapping, safe attach failure handling and executable tests.

1. Close/drain the source database and stop the source node.
2. Detach it from root A.
3. Copy its complete, quiescent folder into root B, using a fresh destination.
4. Attach the folder at root B.
5. Grant access explicitly using root B's accounts.

Data, schema, local roles/table privileges and identity travel together; root accounts/passwords/grants do
not. Root administrators still have their intentional root-wide access to new
registrations. Detach removes old named grants even if the same identity is
reattached. A different identity at an existing registered name fails acquisition
and cannot inherit access by silently replacing the catalog entry.

Do not copy live files. Use the existing verified backup/restore mechanism for
an online source, then close and attach the restored database folder. Restore
placement remains an explicit host operation; no folder-copy command is added.
Independent clones need an identity policy and are not implemented by this patch.
Sync, live reconfiguration and standalone-file removal are out of scope.

## Evidence

Tests cover SQL/schema transfer with identity preservation, independent root
credentials, duplicate identities, busy database rejection, active transaction
ownership, grant revocation on reopen and acknowledged hard-process detach
recovery without catalog cleanup. Hard-exit evidence is not proof against
physical power loss or dishonest filesystem durability.
