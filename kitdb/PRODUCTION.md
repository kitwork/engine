# KitDB Controlled Production Profile

This runbook defines the first real-project profile for KitDB. It is narrower
than a general-purpose database claim: a project is admitted only when its
workload, deployment, recovery policy, and release evidence all fit this
document and the [1.0 release contract](RELEASE_1_0.md).

KitDB is currently a release candidate. Passing this profile means
`controlled_ready`, not generally available or universally production-ready.

## Workloads that fit

The first profile is intended for tenant-isolated Kitwork applications with:

- one local KitDB file per tenant or bounded tenant shard;
- one owning application process and one writer coordinator per database;
- primary-key CRUD, bounded indexed filters and ordering, immediate relational
  constraints, and the documented SQL-light aggregate/join surface;
- request transactions whose operation count, bytes, result size, and lifetime
  are bounded by the host;
- local SSD-backed storage and an independently stored verified backup;
- planned maintenance, schema migration, and restore drills.

Do not admit a workload that requires multi-process writers, a network-mounted
database file, automatic leader election, synchronous cross-region failover,
full PostgreSQL/SQLite SQL compatibility, unbounded analytical scans on the
request path, or public unauthenticated protocol access.

## Non-negotiable deployment boundaries

1. Store the main file, WAL, history, pins, and lock artifacts on one regular
   local filesystem. Do not place live KitDB files on NFS, SMB, object storage,
   or a folder synchronized by a consumer cloud-drive client.
2. Let exactly one Kitwork node own a database at a time. Threads and goroutines
   may share the node-managed handle; separate processes may not share it.
3. Open project databases through `kitdb/node.Manager` with explicit limits for
   open databases, page-cache bytes, concurrent opens, maintenance workers,
   maintenance queue depth, and maintenance timeout.
4. Enable `RetainHistory` before first production traffic when point-in-time
   recovery, projections, or replication are requirements. Enabling it later
   cannot manufacture earlier history. On an existing large database, first
   enablement publishes a complete immutable base anchor and can temporarily
   require I/O, time, and free disk comparable to the active main generation;
   schedule and measure that bootstrap instead of discovering it during a
   restart window.
5. Keep the PostgreSQL-compatible listener on loopback. Expose remote KitDB
   access only through Kitwork's authenticated HTTPS/Hrana boundary or a
   separately reviewed encrypted proxy.
6. Do not overwrite a live file during restore. Restore to a new destination,
   verify it, compare identity and expected logical state, then switch ownership.
7. When publication is required, provision its destination before host startup
   and keep it outside the live and local-backup trees. A directory path is not
   evidence of an independent failure domain by itself; the deployment must
   establish that property through a separate volume, host, or reviewed transfer.

## Standalone PostgreSQL node

`cmd/kitdbpg -root <directory>` can serve several independent files through one
loopback listener without loading Kitwork. Its maintenance database is virtual
and metadata-only; clients list `pg_database`, then reconnect with the selected
logical name. Do not expect `USE database` or user tables on maintenance.

The node discovers only regular non-hidden `.kitdb` files immediately below the
configured root. It opens a selected file lazily through `kitdb/node.Manager`,
coalesces same-file opens, shares one relational/search owner, and LRU-evicts
idle owners. Set explicit limits for discovered files, open databases, total and
per-database page-cache reservation, concurrent opens, connection count, query
time, result rows, and search candidates. Capacity exhaustion is a retryable
connection failure rather than permission to exceed the fleet budget.

One standalone-node credential currently grants every discovered database.
Keep the listener on loopback, put encryption and per-principal policy in a
reviewed proxy or host adapter, and never start a second process over any file
already served by the node. Standalone directory mode does not own SQL database
lifecycle; create or remove files only while their ownership is quiesced.

## Search projection operations

Ranked SEARCH is derived state outside the `.kitdb` durability authority. A
matching source watermark reopens existing immutable search segments. Retained
history lets the projection owner catch up complete row mutations; a missing
history range or incompatible schema requires a full fixed-snapshot rebuild.
Never treat the projection directory as the only copy of application data.

Provision search disk separately in the capacity plan, set explicit result and
candidate budgets, and measure cold-open, warm-query, filtered-query, rebuild,
and catch-up behavior using the project's real text distribution. Monitor both
the KitDB source transaction and projection watermark. A stale projection must
fail or rebuild; it must never be served as current merely because segment files
exist. Keep SEARCH in autocommit mode until explicit transaction semantics are
part of the supported profile.

## Production Supervisor

The host can automate the local protection loop through the same bounded node
manager that owns database handles and maintenance:

```go
publisher, err := node.NewVerifiedDirectoryPublisher(
	"/mnt/kitdb-offhost/tenant-42",
	node.VerifiedDirectoryPublisherOptions{
		MaxEntries:  32,
		KeepAnchors: 8,
	},
)
if err != nil {
	return err
}
if err := fleet.RegisterProductionAnchorPublisher("offhost", publisher); err != nil {
	return err
}

health, err := fleet.RegisterProductionPolicy(node.ProductionPolicyConfig{
	Name:            "tenant-42",
	Source:          "/srv/kitwork/live/tenant-42.kitdb",
	BackupDirectory: "/srv/kitwork/backups/tenant-42",
	Publisher:       "offhost",
	SourceOptions:   kitdb.OpenOptions{RetainHistory: true},
	BackupInterval:  15 * time.Minute,
	MaxBackupAge:    30 * time.Minute,
	RestoreInterval: 24 * time.Hour,
	MaxRestoreAge:   36 * time.Hour,
	KeepBackups:     8,
	Priority:        node.MaintenanceBackground,
})
```

Registration is host-trusted, creates no file, and must be repeated after each
process restart. The controller rediscovers and verifies its immutable anchors,
reconciles the durable source pin, performs a fresh independent restore drill,
and then derives `ready`, `degraded`, or `unsafe` from the declared age bounds.
One dispatcher and a fixed worker pool serve all policies; idle tenants retain
no policy goroutine or database lease. The next cycle follows the earlier
backup or restore deadline. Foreign, mixed-identity, corrupt, malformed, or
future-dated named anchors make readiness `unsafe` until a successful recovery
cycle completes.

Each backup directory is exclusive to one policy and must be outside the source
and its history tree. If `Publisher` is configured, `ready` additionally
requires a path-free receipt matching the exact newest local anchor after the
publisher retrieved and fully verified it. An upload acknowledgement alone is
never evidence. The built-in directory publisher uses deterministic immutable
names, bounded discovery and retention, removes abandoned staging from its own
reserved namespace, verifies before reuse or deletion, and refuses identity
changes, backward transactions, forks, corruption, and overlapping owned
destinations.

The directory publisher is suitable for a separately mounted volume or a
host-transfer spool, but cannot prove physical independence. A reviewed R2/S3
adapter can implement the same `ProductionAnchorPublisher` contract without
changing supervisor readiness. Native object-store credentials, transport, and
alert delivery are intentionally not hidden inside the transaction kernel.

## Admission gate

Qualify the exact engine commit first:

```text
go run ./cmd/releasegate --mode kitdb-release \
  --require-clean \
  --report .artifacts/kitdb-release.json \
  --timeout 90m
```

Then stop the application's writer and qualify the exact project database on
its deployment filesystem:

```text
go run ./cmd/kitdb doctor \
  --expected-id <recorded-database-id> \
  --max-wal-bytes 268435456 \
  /data/tenant.kitdb
```

Admission requires `controlled_ready: true`. The doctor performs a full source
verification, proves a verified same-filesystem backup boundary, restores to a
new file, reopens it, and compares a canonical digest of every logical key and
value. Record the JSON evidence outside the database directory. `stable_ready`
remains false until the compiled release itself has completed stable promotion.

The admission gate is offline and database-specific. It does not replace a
load test using the project's real schema, query distribution, row sizes, and
concurrency. Before traffic, that load test must establish project-owned latency
and resource baselines; KitDB does not publish a universal latency guarantee.
`doctor` proves only its same-filesystem drill; it does not satisfy or inspect a
configured production publisher.

## Backup and recovery contract

Every production project must declare an RPO and RTO. KitDB does not silently
choose them.

- Schedule verified backups at an interval no longer than the declared RPO.
- Use durable named history pins so pruning cannot remove transactions still
  needed by a backup, replica, or projection.
- Configure a reviewed publisher for every workload that claims host-loss
  recovery. A second file on the same disk protects against logical mistakes,
  not disk loss.
- Alert when the newest verified off-host backup is older than the RPO.
- Rehearse restore to an independent database at least once per release and at
  the project's chosen recurring interval.
- Verify the restored file and execute application-level invariants, not only a
  checksum check. Measure the drill against the declared RTO.
- Keep at least one known-good release binary capable of reading the recorded
  compatibility profile and frozen backup.

Use the node maintenance path for an online verified anchor:

```text
go run ./cmd/kitdb backup --pin backup/nightly tenant.kitdb anchor.kitdb
go run ./cmd/kitdb verify anchor.kitdb
go run ./cmd/kitdb restore anchor.kitdb restored.kitdb
```

Use `restore-time` only when retained timestamped history covers the requested
instant. Treat `ErrHistoryGap` as a hard recovery boundary, never as permission
to guess or return a nearby state.

## Runtime observability

Export both `kitdb.DB.Stats()` and `kitdb/node.Manager.Stats()` through the
host's bounded metrics surface. Establish baselines under realistic traffic and
alert on sustained deviation. At minimum observe:

- WAL bytes, last transaction, active snapshots, active transactions, active
  transaction bytes, pending commits, and commit queue capacity;
- managed/open/idle databases, active leases, waiting acquisitions, reserved
  page-cache bytes, open failures, evictions, waits, and last close error;
- queued/running maintenance, maintenance rejections, failures, cancellations,
  duration, checkpoint completions, verification completions, and backup
  completions;
- history bytes, retained and retired segments, pins, oldest pinned transaction,
  retention bytes over limit, and retention limited by pins;
- replica link failures, consecutive failures, backoff, maximum transaction lag,
  and pending batch bytes;
- production publisher count, publication attempts/failures, newly published
  and resumed anchors, published bytes, pruned remote anchors, publication age,
  and exact-current-anchor readiness;
- process RSS, goroutines, file descriptors or Windows handles, disk latency,
  free disk bytes, request error rate, and application p50/p95/p99 latency.

A useful initial warning policy is 80 percent sustained occupancy for bounded
queues, page-cache reservation, transaction bytes, or disk capacity. This is an
operator starting point, not an engine guarantee. Any corruption, failed sync,
maintenance failure, unexplained identity change, or missed backup RPO is a
page-worthy event.

## Schema and release procedure

1. Test every production query and migration against the exact release binary.
2. Take and verify a backup before schema changes.
3. Run migration admission/preflight and respect its bounded or resumable
   status. Do not raise limits merely to force a large migration through.
4. Canary one disposable or low-risk tenant first, then a small tenant cohort.
5. Verify, checkpoint, close, and reopen the canary before wider rollout.
6. Stop rollout on new errors, maintenance backlog, memory growth, restore
   mismatch, or an unexplained latency regression.
7. Roll back by switching to an independently restored database and the known
   compatible binary. Never edit WAL, history, catalog, or segment bytes by hand.

## Incident order

When integrity or durability is uncertain:

1. Stop new writes and preserve the complete database artifact set.
2. Record the database identity, compatibility profile, file sizes, process
   state, and last successful backup without modifying source bytes further.
3. Work on copies. Run `inspect` and `verify`; do not delete a WAL tail manually.
4. Restore the newest verified anchor to a new destination and replay only a
   verified contiguous retained-history range.
5. Compare application invariants and logical digests before serving traffic.
6. Keep the failed artifacts for a deterministic regression test and root-cause
   analysis.

## Stable promotion

`controlled_ready` is enough for an explicit, monitored canary project whose
owner accepts the release-candidate boundary. A stable 1.0 claim additionally
requires the same clean commit to pass Windows and Linux release gates, a
24-hour deployment-filesystem canary, independent restore drills, compatibility
fixtures, rollback rehearsal, and review of every remaining exception listed in
[RELEASE_1_0.md](RELEASE_1_0.md).
