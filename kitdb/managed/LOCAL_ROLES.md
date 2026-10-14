# Database-local roles: bounded table privileges

This is a bounded database-local authorization profile, not full PostgreSQL ACL,
column permissions, RLS or indirect-effect authorization.

## Ownership and transfer

The managed root `.catalog` owns login accounts, password verifiers, CONNECT
grants, and explicit account-to-local-role mappings. A user database's
`data.kitdb` owns its role definitions and SELECT/INSERT/UPDATE/DELETE grants. They are a
bounded versioned engine-owned security catalog record, not a sidecar or a
second database owner. Every publication uses the normal kernel WAL transaction.
Backup/restore carries these records with the database, not root accounts or
account mappings. A same-named account in a destination root gets no implicit
permissions; its administrator must explicitly map it.

The repeatable operator workflow, including `shop` restored as `archive`, is
documented in [TRANSFER.md](TRANSFER.md).

See [REVOCATION.md](REVOCATION.md) for commit-after-revoke semantics, account
incarnations and the limits of immediate KILL SESSION termination.

Role IDs are random 16-byte identities, distinct from their display names.
Root mappings store these IDs. DROP ROLE refuses a role still mapped in this
root; revoke those mappings first. Recreating a role does not revive an old ID.
Standalone raw Engine/kernel owners remain host-trusted; this enforcement is
for managed native SQL, pgwire and KitSQL clients, not a filesystem sandbox.

## SQL

Connect as an enabled root administrator to the selected database (here blog).
Local role commands require autocommit, not a data/maintenance transaction.
The recipient must have a CONNECT-only database grant, without built-in
reader/writer/developer or database-wide READ/WRITE/SCHEMA. Revoke any broad
database permissions before mapping a restricted role. Nothing is granted
implicitly by creating a role or mapping an account.

```sql
CREATE USER alice WITH PASSWORD 'Alice-local-example-2026!';
GRANT CONNECT ON DATABASE blog TO alice;

CREATE ROLE article_reader;
GRANT SELECT ON TABLE articles TO ROLE article_reader;
GRANT ROLE article_reader ON DATABASE blog TO alice;

-- Connect a fresh client/pool as alice:
SELECT title FROM articles WHERE id = 1;
-- SELECT * FROM payments is denied without a grant.
```

```sql
REVOKE SELECT ON TABLE articles FROM ROLE article_reader;
REVOKE ROLE article_reader ON DATABASE blog FROM alice;
DROP ROLE article_reader;
```

These are KitDB statements, not PostgreSQL role-management compatibility.
`TO ROLE` explicitly distinguishes a database-local role from a login account.
Names are lowercase safe identifiers of at most 63 bytes. No role inheritance,
login attributes, ownership transfer, CASCADE, value parameters or grant option.
Only root administrators manage roles in this slice. Prepared Describe validates
without publishing changes. The maintenance database has no user-table roles.

Auth documents upgrade to version 3 before local ACL publication or when a mapping
is published. An unsuccessful database mutation can leave this safe format-only
upgrade; no cross-database atomicity is claimed. Existing v1/v2
roots remain readable; older binaries fail closed on v3. Do not downgrade a
mapped root using an old binary. The built-in root database roles remain separate
and unchanged. Broad permissions and local mappings cannot be mixed.

The local security document remains at the reserved v1 key. Its document version
is 1 for existing SELECT-only databases and upgrades to 2 on the first write
privilege command. Version 2 preserves existing role/table IDs and SELECT grants;
no extra file/owner is introduced. Older SELECT-only readers reject version 2
or unknown write fields instead of silently interpreting a write role as global
permission. New readers accept v1/v2; invalid IDs, duplicate grants, unsupported
versions and oversized records fail closed. Do not downgrade a database with v2
ACLs to an old binary.

## Write semantics

Each command changes one privilege on one table. Multiple roles compose by union
for each operation; creating/mapping a role does not grant any operation.

```sql
CREATE ROLE article_editor;
GRANT SELECT ON TABLE articles TO ROLE article_editor;
GRANT INSERT ON TABLE articles TO ROLE article_editor;
GRANT UPDATE ON TABLE articles TO ROLE article_editor;
GRANT DELETE ON TABLE articles TO ROLE article_editor;
GRANT ROLE article_editor ON DATABASE blog TO alice;

-- On a fresh connection as alice:
INSERT INTO articles(id, title) VALUES(2, 'A draft');
UPDATE articles SET title = 'Published' WHERE id = 2;
DELETE FROM articles WHERE id = 2;

-- As administrator:
REVOKE UPDATE ON TABLE articles FROM ROLE article_editor;
```

INSERT accepts literal/parameter VALUES and safe non-function expressions. UPDATE
and DELETE operate on one table with the existing bounded mutation executor.
Reading fields in WHERE or an UPDATE expression requires SELECT on that table;
RETURNING always requires SELECT as well. INSERT alone is enough for a simple
VALUES insert without RETURNING. UPDATE with literal assignments and no predicate
does not implicitly read values; DELETE without a predicate does not require
SELECT. These can affect every row within the existing mutation bound. Use WHERE
when narrowing changes. Grants are whole-table, not row restrictions.

Functions, triggers on the target, sequence
defaults/identity fields, partitioned tables and incoming/outgoing foreign-key
relationships are refused in this initial direct-table write profile. In
particular, a target grant cannot authorize a trigger or cascade into another
table. Existing database-wide WRITE retains its existing SQL support. Extending
these shapes requires explicit read/effect authorization, not relaxing this gate.

### INSERT SELECT

Restricted roles may copy from one ordinary table with a source SELECT in the
same bounded local-read profile: projections, safe non-function expressions,
predicates, ordering/pagination and supported direct aggregates. JOIN, CTE,
derived sources, UNION, ranked search, functions and table-free scalar SELECT
sources remain refused. The target keeps the direct-write restrictions above.

```sql
GRANT SELECT ON TABLE drafts TO ROLE article_editor;
GRANT INSERT ON TABLE articles TO ROLE article_editor;

INSERT INTO articles(id, title)
SELECT id, title FROM drafts WHERE id = 2;
```

The executing account needs SELECT on the source and INSERT on the target;
neither permission substitutes for the other. RETURNING additionally needs
SELECT on the target. An empty source still requires permission. Same-table
source/target needs both permissions too. Grants can come from the union of the
account's captured role IDs, but must authorize each table independently.

Authorization reads both table IDs and the ACL document from the transaction's
exact snapshot. Describe checks both permissions without writing; Execute checks
again. A fresh snapshot sees source or target revoke, while active transactions
retain pinned ACLs. Root mappings still follow the connection policy above.

The ordinary INSERT SELECT executor materializes its bounded source before
staging any target row, including for same-table copies. Its source budget is
min(MaximumResultRows, SQL MaximumInsertRows), currently at most 10,000 rows.
There is no unbounded copy or second database handle. Limits, cancellation and
constraint errors do not publish partial target writes. Existing native
statement-savepoint behavior and pgwire failed-transaction/ROLLBACK behavior
remain unchanged; autocommit publishes only the whole successful statement.

### ON CONFLICT

Restricted roles support single-table ON CONFLICT using the ordinary primary or
unique-key arbiters, including composite keys. Other index expressions and
non-unique conflict targets are not accepted by the underlying executor.

```sql
GRANT INSERT ON TABLE articles TO ROLE article_editor;
GRANT UPDATE ON TABLE articles TO ROLE article_editor;

INSERT INTO articles(id, title) VALUES(1, 'A revised title')
ON CONFLICT(id) DO UPDATE SET title = EXCLUDED.title;
```

INSERT is always required, including when every proposed row already exists.
DO NOTHING requires no additional UPDATE privilege. DO UPDATE always needs
UPDATE, even when input produces zero rows or no actual conflict. Permission
checks precede target writes and do not depend on which branch happens to run.

EXCLUDED fields are values proposed by the INSERT, not reads of stored data.
Literal/parameter/default assignments and safe non-function expressions using
only EXCLUDED do not by themselves require SELECT on the target. Reading old
fields (qualified or unqualified), any DO UPDATE WHERE clause, and RETURNING
require target SELECT. This WHERE rule is deliberately conservative, even for
a condition over only EXCLUDED values. Source SELECT permission is additionally
required when the INSERT uses a table source.

For example, the following needs target SELECT as well as INSERT and UPDATE:

```sql
INSERT INTO articles(id, title) VALUES(1, 'A revision')
ON CONFLICT(id) DO UPDATE SET title = articles.title || EXCLUDED.title
RETURNING title;
```

The same read-only, snapshot and prepared-Execute rules apply. Functions,
trigger/FK/sequence/partition effects remain refused for restricted targets.
Conflict handling uses the existing bounded INSERT executor and statement
savepoint; it introduces no second owner/WAL path. A later unique/check/RETURNING
failure or repeated-row DO UPDATE cardinality error rolls back the whole
statement, including earlier conflict updates. DO NOTHING suppresses uniqueness
conflicts only, not invalid CHECK/NOT NULL input. Root privileges and ACL storage
formats are unchanged.

ACLs, schema checks and execution use one transaction snapshot. Autocommit checks
and executes in the same owned transaction, committing only after successful
execution; errors/denials cannot publish partial writes. Active transactions pin
their ACL snapshot until completion. Prepared statements reauthorize at Execute
using that execution snapshot. Root mapping changes still require reconnect.
Node read-only and explicit read-only transactions override write grants. Mapped
connections are not automatically forced into a read-only transaction solely
because their root grant is CONNECT-only: per-statement local authorization
controls which operations they can execute.

## Read semantics

Role membership is captured at pgwire connection/native logical-pool creation;
KitSQL authenticates each request. Reconnect/recreate the pool after root mapping
changes. Database-local table grants, however, are read from the exact kernel
snapshot used for the query. Revoke therefore affects the next fresh snapshot;
a previously opened transaction retains its pinned data and grant snapshot.

The supported profile is single-table SELECT/EXPLAIN, projection, ordinary
predicates, ordering/pagination and supported direct aggregates. JOIN, CTE,
UNION, derived sources, ranked search and all scalar function calls fail closed.
INSERT/UPDATE/DELETE require their own grants; DDL/PRAGMA are not enabled by a local role. Existing
database-wide grants retain their original broader profile. Restricted reads
use bounded materialization, not the native streaming optimization.

Schema IDs are currently name-derived. SQL DROP TABLE therefore removes that
table's SELECT/INSERT/UPDATE/DELETE grants atomically with its schema definition. A newly created
same-name table does not inherit old ACLs, even on an existing connection.
An old transaction can still read its pre-drop data/ACL snapshot. Table rename
preserves the underlying ID and its grant. Host-trusted raw catalog mutation
must not be treated as permission-governed SQL.

Bounds: 128 roles per database, 4096 table IDs per privilege per role, 1 MiB encoded security
catalog, 32 local role mappings per account/database. Invalid/oversized catalogs
deny restricted access; missing mappings/roles do not fall back to global rights.
Existing users/grants views describe root permissions. The read-only roles,
table_grants, role_members and effective_privileges views are documented in
[ROLE_VIEWS.md](ROLE_VIEWS.md); these report grants without modifying them.

## Verification

The seeded concurrent-write, revocation, interruption and reopen checks are
documented in [WRITE_CAMPAIGN.md](WRITE_CAMPAIGN.md).
Real standalone pgwire crash boundaries and unknown-commit reconciliation are
documented separately in [PROCESS_CRASH.md](PROCESS_CRASH.md).

```text
go test -race ./kitdb/kitsql -run ^TestDatabaseLocalRolesAcrossAdapters$ -count=1
go test -race ./kitdb/kitsql -run ^TestLocalRoleWritesAcrossAdapters$ -count=1
go test -race ./kitdb/kitsql -run ^TestLocalRoleInsertSelectAcrossAdapters$ -count=1
go test -race ./kitdb/kitsql -run ^TestLocalRoleUpsertAcrossAdapters$ -count=1
go test -race ./kitdb/relational -run ^TestLocalRoleInsertSelect -count=1
go test -race ./kitdb/relational -run ^TestLocalRoleWrite -count=1
go test -race ./kitdb/relational -run '^(TestLocalRolesPortableDatabaseRequiresMapping|TestLocalRoleHardExit)' -count=1
```

Coverage includes all three adapters, parameterized reads/prepared statements,
forbidden indirect reads, snapshot revoke, same-name table recreation, role
mapping persistence, verified backup transfer into a new root, and hard process
exit after CREATE ROLE/GRANT SELECT/root mapping/DROP TABLE and INSERT grant/revoke ACK. Write tests cover
independent grants, SELECT requirements and forbidden indirect effects across
all three adapters. Native/pgwire tests additionally exercise transactions,
rollback and read-only overrides; native tests check pinned ACLs. Process-crash
evidence is not a physical power-loss or stable-release qualification.
