# KitDB 1.0 Release Contract

KitDB 1.0 is being qualified as a small, pure-Go, single-node relational
database for standalone and embedded deployments, including isolated Kitwork
tenants. The target is `1.0.0`; the compiled profile remains
`release-candidate` until the promotion evidence below exists for one clean
commit on both Windows and Linux.

This contract deliberately makes a narrow production claim. It does not use a
`1.0` label to imply PostgreSQL feature parity or distributed-database safety.
The operator procedure implementing this claim lives in
[PRODUCTION.md](PRODUCTION.md).

Candidate binary packaging, build identity and native executable checks are
documented in [DISTRIBUTION.md](DISTRIBUTION.md). Packaging is separate from
the platform and deployment qualification below.

## Supported profile

KitDB 1.0 supports:

- a regular local filesystem whose file, `fsync`, rename, and directory-sync
  behavior matches the operating-system contract;
- one writer process per database, with concurrent readers and bounded
  transaction preparation inside that process;
- embedded Go kernel/relational access, the standalone `kitdb query/serve`
  process, and Kitwork-owned tenant-confined files;
- Kitwork `struct()`/ORM, standalone embedded SQL, and the explicitly documented
  loopback-only PostgreSQL-familiar protocol profile; the bounded Hrana adapter
  is retained for existing Kitwork deployments but does not define the feature
  baseline for new KitDB work;
- versioned Schema IR, primary/unique/secondary indexes, immediate constraints,
  fixed-snapshot transactions, checkpoint, full verification, verified backup,
  retained-history point-in-time recovery, and one-way local replication;
- node-level bounded handles, page cache, maintenance workers, COPY admission,
  queues, timeouts, operation-size limits, and host-registered production
  policies that compose verified local backup, independent restore drills,
  retention, retry backoff, optional exact-anchor publication through a
  host-owned adapter, and path-free readiness health;
- the machine-readable `kitdb version` and operator `doctor`, `inspect`,
  `verify`, `backup`, `restore`, and `restore-time` commands.

Before admitting a real project database, stop its writer and run:

```text
go run ./cmd/kitdb doctor /path/to/project.kitdb
```

`doctor` works in a unique sibling directory on the same filesystem, performs
full source verification, creates and verifies a backup anchor, restores and
reopens it, and compares a canonical SHA-256 digest over every logical key and
value. `controlled_ready` means the database passed the bounded RC deployment
profile. `stable_ready` additionally requires a compiled stable release. The
temporary drill directory is removed and the JSON result exposes no source
path, key, or value.

The durability claim ends at a successful sync reported by a truthful supported
local filesystem. Release evidence proves process-death, torn-tail, failed-sync,
and publication-order recovery. It does not prove dishonest drive firmware or
physical power-loss behavior on hardware that was not qualified.

## Compatibility promise

`kitdb.CurrentCompatibility()` and `kitdb version` publish the `kitdb/1`
contract. The first 1.x line reads main-file versions 1 through 3 and writes
version 3. WAL, transaction frames, history, history pins, replica protocol,
and replica wire are version 1. Relational release evidence separately freezes
Schema IR, row, node-catalog, statistics, import, index-build, and migration
encodings.

For the 1.x line:

1. A newer 1.x engine must read every healthy database accepted by 1.0, or ship
   an explicit tested migration that preserves logical rows and identity.
2. Existing version numbers and field meanings cannot be reused.
3. Additive metadata must be safely ignored or rejected by an older reader;
   silent reinterpretation is forbidden.
4. An older 1.x engine reading data written by a newer 1.x engine is not
   generally promised. It must either preserve the documented unknown fields or
   fail closed.
5. A format change requires a changed compatibility profile, frozen fixtures,
   recovery tests, and release evidence on both supported operating systems.

## Explicit exclusions

KitDB 1.0 does not claim:

- multiple writer processes for one file, shared-network-filesystem safety,
  distributed consensus, automatic failover, or synchronous cross-region
  durability;
- general PostgreSQL or SQLite SQL compatibility;
- standalone COPY/resumable CSV/JSONL import. The existing `kitdbimport` tool
  requires the Kitwork adapter's KIMP/COPY integration and is excluded from
  the standalone RC bundle until that path is ported and qualified;
- public-network PostgreSQL service operation. The current adapter is loopback
  only and intentionally has no TLS or SCRAM;
- savepoints, deferred constraints, unbounded queries/migrations/imports, or
  unrestricted joins, grouping, and result sizes;
- lock-free or zero-pause DDL;
- forward compatibility with unknown future 2.x formats;
- recovery from arbitrary application bugs, stolen credentials, compromised
  hosts, or operator deletion without a verified backup.
- durable production-policy discovery, native R2/S3/object-store transport,
  credential storage, or notification delivery. A host must register policies
  and publishers again after restart. The built-in verified directory publisher
  proves exact read-back and bounded retention, but deployment must establish
  whether its pre-created destination is a separate physical failure domain.

## Executable gates

Run the fast KitDB gate during development:

```text
go run ./cmd/releasegate --mode kitdb-verify \
  --report .artifacts/kitdb-verify.json \
  --timeout 45m
```

Run the complete candidate gate from a clean repository:

```text
go run ./cmd/releasegate --mode kitdb-release \
  --require-clean \
  --report .artifacts/kitdb-release.json \
  --timeout 90m
```

The release gate includes full kernel/node/relational/operator tests, the
standalone pure-Go search suite, build, vet, compatibility checks, database
journey and durability oracles, the mandatory standalone commerce journey, explicit analytics
publication/corruption/upgrade recovery, kernel/search/relational race detector
coverage, ten repetitions of commerce/replica/catalog/import/index/analytics hard-crash
matrices, a multi-tenant canary smoke, and a seeded 128-iteration replica crash
soak.

The commerce gate builds fresh `CGO_ENABLED=0` native `kitdb`/`kitdbpg`
executables from the current checkout, checks their standalone dependency
allowlist, and cannot skip through a missing binary-directory environment
variable. It exercises products/orders/order_items/audit, DOMAIN/FUNCTION/
TRIGGER/SEQUENCE, exact money, late failures and rollback, a forced process
termination with staged audit writes, independent backup restore and timestamp
recovery of data and catalog objects. See [DISTRIBUTION.md](DISTRIBUTION.md).
Its bounded `.artifacts/kitdb-commerce-gate.json` includes platform, binary
digests, passed phases and success, not source paths or credentials. This is
one development gate, not a clean-commit packaged-candidate qualification.

Run the long storage canary on the intended deployment filesystem:

```text
kitdbcanary \
  --root /qualified/local/filesystem/kitdb-canary \
  --duration 24h \
  --tenants 128 \
  --workers 16 \
  --max-open 32 \
  --json .artifacts/kitdb-canary-24h.json
```

The canary reuses a bounded keyspace, bounds retained history, exercises node
handle eviction, checkpoint and verification, and finishes by creating,
verifying, restoring, reopening, and comparing every tenant against its exact
committed model. The report contains no database path, key, or value.

Use the packaged `kitdbcanary` for candidate evidence. Its `build.commit` must
match the platform reports; `workload_completed` and `success` must both be
true. Require at least 86,400,000 in `requested_workload_ms` and `workload_ms`.
Total `duration_ms` includes setup and restore and is insufficient by itself.

## Promotion checklist

The `Stability` constant may change from `release-candidate` to `stable` only
after all of the following are true for the same clean commit:

- `kitdb-release` succeeds on `windows/amd64` and `linux/amd64` with identical
  kernel and relational compatibility profiles;
- each JSON report records success, the same commit, Go release line, and no
  dirty entries;
- restore drills create independently verified databases from the Windows and
  Linux artifacts and compare their expected logical models;
- a minimum 24-hour bounded KitDB canary completes with no corruption, panic,
  leaked lease, failed invariant, or unexplained p99 regression;
- backup and point-in-time restore are exercised from a copy of the intended
  deployment filesystem, not only an in-memory or temporary CI filesystem;
- a configured production publisher is exercised across process restart and
  its independently stored current anchor is restored and logically compared;
- upgrade from the frozen pre-1.0 fixture and rollback procedure have both been
  rehearsed;
- security documentation keeps PostgreSQL on loopback or places it behind a
  separately authenticated and encrypted trusted proxy;
- every remaining exception is documented as an explicit non-goal rather than
  hidden by a raised timeout or resource ceiling.

Until those artifacts have been reviewed, KitDB is a release candidate and the
repository must not describe it as generally available 1.0 storage.
