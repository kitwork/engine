# KitDB v0.7 Non-Goals

The current milestone does not attempt to provide:

- production readiness or long-term file-format compatibility;
- SQL parsing or PostgreSQL compatibility;
- compiler-owned static struct types, stable rename identities, or generated
  codecs; the current Kitwork adapter builds deterministic runtime Schema IR;
- joins, grouping/aggregates, or a cost-based query optimizer;
- full SQLite SQL or full Hrana v3 compatibility; the experimental remote
  profile is JSON pipeline plus a bounded SQL subset, not a claim that KitDB is
  SQLite over another file format;
- automatic/online schema migration; a changed persisted struct is currently
  refused until an explicit migration mechanism exists;
- cascading or set-null/set-default foreign-key actions; the adapter enforces
  existence and restrictive/no-action references and refuses unsupported
  actions at schema validation;
- MVCC writers, write-conflict resolution, unbounded or persistent snapshots,
  or concurrent write transactions; v0.7 snapshots are bounded read views;
- online/nonblocking compaction or compaction concurrent with commits;
- an in-place B-tree, mutable physical pages, or startup independent of active
  page count; persisted directories are flat sparse indexes;
- automatic background scrubbing or repair of a page that fails verification;
- page compression, physical space reclamation without compaction, or retained
  historical snapshots; obsolete generations are implementation debris, not a
  public time-travel API;
- memory independent of sparse-key cardinality or uncheckpointed WAL changes;
- search, analytics, vector indexes, or AI execution;
- replication, branching, merge, or distributed consensus;
- encryption at rest, authentication, or authorization;
- direct multi-process read access while a writer is open;
- confinement beneath a Kitwork tenant root.

The embedding adapter, not the standalone kernel, will confine tenant paths.
This milestone opens one exclusive process writer and serves concurrent reads
through that process. Incremental checkpoints bound ordinary write
amplification, but a legacy migration or the 32-segment limit still requires a
full streaming rewrite.
