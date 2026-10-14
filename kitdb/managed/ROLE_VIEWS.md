# Database role and effective-privilege views

These read-only `kitdb_catalog` views apply to the selected authenticated managed
database. Native SQL, pgwire and KitSQL share the same implementation and owner.
The maintenance database does not open other databases to answer these queries.

## Views

- `roles`: database_name, role_id, role_name.
- `table_grants`: database_name, role_id, role_name, table_id, table_name,
  table_exists, can_select, can_insert, can_update, can_delete. One row combines
  all explicit privileges for each role/table pair; booleans describe recorded
  grants, not a login account's effective permission or supported SQL shapes.
- `role_members`: database_name, username, role_id, role_name, disabled,
  role_available. These are current root-configured mappings. An unavailable
  role has an empty role_name and role_available=false.
- `effective_privileges`: database_name, username, table_id, table_name,
  policy_scope, select_source, can_connect, can_select, can_insert, can_update,
  can_delete, can_schema.

```sql
SELECT role_name, role_id FROM kitdb_catalog.roles ORDER BY role_name;

SELECT role_name, table_name, can_select
FROM kitdb_catalog.table_grants ORDER BY role_name, table_name;

SELECT username, role_name, disabled, role_available
FROM kitdb_catalog.role_members ORDER BY username;

SELECT username, table_name, policy_scope, select_source, can_select
FROM kitdb_catalog.effective_privileges
WHERE username = 'alice' ORDER BY table_name, policy_scope;
```

## Visibility

An administrator must have both a captured admin login and a currently enabled
admin record to enumerate other accounts/roles. A disabled, dropped or demoted
admin's old session cannot retain administrative visibility through these views.
This does not silently revoke its captured user-data policy or end its session.

Ordinary users see only their own root mappings, and role definitions/grants
referenced by their captured membership or their current configured membership.
Effective-privilege rows contain only their own account. Table names are visible
only when that account has a permission on the table in at least one of the two
policy scopes. A table with no rights in either scope is omitted, not exposed as
a denial row. After a mapping revoke, the account can still compare true session
rights with false configured rights on tables its old session can access.

Visibility is applied before WHERE, OR, ordering, aggregation and pagination.
SQL predicates cannot widen it. No password/verifier, filesystem path, query text
or another database's role definition is present in these schemas. Stable IDs
are identifiers, not filesystem capabilities.

## Two Policy Scopes

`session` describes the issuing physical session's captured database permissions
and role IDs, combined with the database ACL snapshot used by this query. It
does not claim to describe all other currently connected sessions. It applies
the session/node read-only restriction, and read-only transaction restrictions;
can_schema is false inside a data transaction where DDL is unsupported.

`configured` combines current root account/grant/membership configuration with
the same database snapshot. Administrators see configured rows for all root
accounts in the selected database; ordinary users see themselves only. Node
read-only policy applies, but the issuing session's older read-only/membership
state is not incorrectly applied to a different account or a future connection.

`select_source` is database, local_role or none. Database-wide READ and local
table SELECT are deliberately not collapsed into the root `grants.can_read`
field: that older view continues to describe database-wide root grants only.

For example, after REVOKE ROLE the old connection may report session SELECT=true
and configured SELECT=false. Reconnect/recreate its pool to capture the new root
mapping. After REVOKE SELECT ON TABLE, a fresh DB snapshot sees the revoke even
on the old connection. Prepared data statements recheck this at Execute.

These are privilege reports, not a promise that every SQL shape or feature is
supported. Write flags combine existing database-wide WRITE and local-role
INSERT/UPDATE/DELETE grants. A true flag does not authorize forbidden indirect
effects or remove a SELECT requirement for WHERE/RETURNING; see LOCAL_ROLES.md.

## Snapshots and Limits

Role definitions, table names/IDs and ACLs come from one pinned data snapshot.
Inside a transaction, the views use its existing snapshot. Root configuration
belongs to another owner and is read currently; no cross-database atomic snapshot
of root configuration and user data is claimed. The configured scope inside an
old transaction therefore still uses that transaction's database ACL snapshot.

DDL currently waits for an open relational transaction to finish. A pinned view
keeps the old table name; after the transaction ends and rename finishes, a new
view reports the new name and the same grant. Role drop/recreation uses a fresh
role ID: old captured membership does not inherit the recreated role's rights.

Supported SQL is a single-view SELECT with field projections or COUNT(*), bounded
WHERE, field ORDER BY, LIMIT and OFFSET. JOIN/CTE/UNION/DISTINCT/GROUP BY, computed
projections, aggregates other than COUNT(*) and mutations fail closed. Result
retention uses the node's MaximumResultRows; scans stop at 65,536 visible candidate
rows. This bound can reject large administrator effective-privilege enumerations
even when a predicate would select fewer rows; no unbounded scan is promised.
Cancellation is checked during evaluation. No partial results are returned on
permission, syntax, corruption, cancellation or row-limit errors.

## Verification

```text
go test -race ./kitdb/relational -run ^TestLocalRoleCatalog -count=1
go test -race ./kitdb/kitsql -run ^TestDatabaseLocalRolesAcrossAdapters$ -count=1
```

Tests cover OR/parameterized filters, privacy, bounds, root vs effective rights,
read-only transactions, stale admin, rename/drain, mapping revoke, role identity
recreation, prepared data access after revoke, and concurrent grant/view reads.
