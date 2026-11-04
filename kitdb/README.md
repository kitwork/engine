# kitdb

This package is the kernel of KROW, KitDB's own storage engine until 29/10/2026, kept only to read an old
`.kitdb` file. Nothing in the engine writes one any more: an app's database is SQLite
(`.database/application/kitwork.db`), with the layer KitDB adds above it in `kitdb/sqlite` and
`work/database_history.go` (the root repository's `docs/KITWORK_KITDB.md` is the spec).

Two readers use it, each on a private copy of the file opened with `OpenReplica`:

- `kitwork migrate kitdb` (`work/kitdb_copy.go`, `work/kitdb_read.go`) copies an app's `application.kitdb`
  and `storage.kitdb` into the SQLite files beside them, row for row, digest against digest.
- `kitdb/catalog` imports the accounts of an old managed root (`.catalog/data.kitdb`) into the port's catalog.

Their tests read files the engine wrote while it had KROW (engine f210bef8), in `work/testdata/kitdb/` and
`kitdb/catalog/testdata/kitdb/`.

KROW left in slices on 03–04/11/2026 (`docs/KITWORK_KITDB.md` câu 13.4): the relational layer
(`kitdb/relational`), managed roots (`kitdb/managed`), the node manager (`kitdb/node`), the search
projection, KitSQL, the operator commands (`cmd/kitdb*` but `cmd/kitdbsqlite`), the replica file transport and
wire, the release gate's KitDB steps, and the product documents (README of the product, VISION, RESEARCH,
COMPLETION, NON_GOALS, RELEASE_1_0, PRODUCTION, DISTRIBUTION, KITSQL). The engine's history before 0d0d9eda
holds all of it.

The kernel still compiles its write path, because replica mode opens through it and its own tests build
databases with it. The notes left beside it describe KROW as it was, a record of the format a reader meets:
`FORMAT.md` (the files, frames and catalogue), `INVARIANTS.md`, `CONCURRENCY.md`, `MEMORY_BUDGET.md`,
`LOCK_PROFILE.md` and `FAULT_LAB.md`. What they say of the relational layer, the node manager, replicas and the
tools is history.

When every old file has been copied, this package, `kitdb/record`, `kitdb/internal`, `work/kitdb_read.go` and
`kitwork migrate kitdb` can go together.
