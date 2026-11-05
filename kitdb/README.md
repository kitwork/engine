# kitdb

KitDB is the layer Kitwork adds above SQLite (`modernc.org/sqlite`, pure Go). The root repository's
`docs/KITWORK_KITDB.md` is its spec. Its packages:

- `sqlite`: one writer per file; the history (`kitwork_history`) written in the transaction of each commit,
  chained by checksum; anchors (a base of the file's pages, then the pages changed); restore; `at()` going
  backward (`PastRelation`); `history()`.
- `gateway`: the sessions of the read-only PostgreSQL port on the SQLite files: `pg_catalog` and
  `information_schema`, the rewriting of PostgreSQL statements, grants checked on each statement's program,
  `MATCH`, and the limits of a statement (rows, bytes, time, temporary files).
- `pgwire`: the PostgreSQL wire protocol, SCRAM-SHA-256, the admission of statements.
- `sql`: the tokenizer and parser `pgwire` and `gateway` read statements with.
- `catalog`: the port's accounts, roles and grants, in `.catalog/kitwork.db`.
- `columnar`: the column format KCOL is to read (KITWORK_KITDB.md mục 6); nothing reads it yet.

KROW, KitDB's own storage engine until 29/10/2026, left the engine in slices on 03–05/11/2026 (KITWORK_KITDB.md
câu 13.4). The last of it went on 05/11/2026, once every old `.kitdb` file of the workspace was copied into SQLite or
archived: the kernel kept to read such a file, `kitdb/record`, `kitdb/internal`, `kitwork migrate kitdb` and the
catalog's import of an old KitDB catalog's accounts. Engine e78825d3 is the last commit that holds them, with the
notes on KROW's format (`FORMAT.md`, `INVARIANTS.md`, `CONCURRENCY.md`, `MEMORY_BUDGET.md`, `LOCK_PROFILE.md`,
`FAULT_LAB.md`).
