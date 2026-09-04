# KitDB SQL

This package is KitDB's storage-neutral relational frontend. It must not import
the Kitwork VM, tenant runtime, or `work` package.

## Owned here

- canonical logical types and SQL aliases;
- PostgreSQL type metadata shared with `kitdb/pgwire`;
- VM-independent Schema IR decoding and validation;
- the bounded SQL lexer, tokens, and source byte spans;
- top-level statement classification and the bounds-checked parser cursor;
- one bounded expression IR shared by checks, predicates, projections,
  assignments and `HAVING`, with explicit depth/node/argument limits;
- standalone ASTs for catalog DDL, multi-row `INSERT`, bounded `SELECT`,
  `UPDATE`, `DELETE`, aggregates, grouping, equality joins, bounded
  `UNION ALL`, non-recursive `WITH` queries, derived tables, and one-argument
  read-only inspection pragmas such as `analytics_status(table)`;
- planning-only `EXPLAIN [QUERY PLAN] SELECT/WITH` and the explicit
  execution-bearing `EXPLAIN ANALYZE SELECT/WITH` flag. Runtime metrics remain
  owned by `kitdb/relational`, not by this storage-neutral parser.

## Extraction boundary

The first independent binder and executor live in `kitdb/relational`. Work's
broader SQL-light implementation still contains syntax and features not yet
promoted into the standalone profile, but it delegates lexical and top-level
syntax contracts to this package. New standalone features move through this
package rather than importing Work into the database.

Every slice must preserve the existing SQL/error contract, keep resource bounds
explicit, and pass both standalone package tests and Work compatibility tests.
Consumers of `ParseEnvelope` must honor `ExplainAnalyze`; Kitwork's older
SQL-light bridge fails closed instead of silently downgrading it to
planning-only `EXPLAIN`.
