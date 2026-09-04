# Engine Release Procedure

Kitwork treats stability as repeatable evidence, not a one-time successful test
run. A release candidate must preserve the frozen compatibility boundary, pass
the same gate on Windows and Linux, and survive a bounded canary campaign.

## Verification gate

Use the short gate during normal engine work:

```text
go run ./cmd/releasegate --mode verify --report .artifacts/verify.json
```

Use the complete gate from a clean engine repository before a release:

```text
go run ./cmd/releasegate \
  --mode release \
  --require-clean \
  --timeout 45m \
  --report .artifacts/release.json
```

On PowerShell the same command can be written on one line. `--dry-run` prints
the exact plan without executing commands. The JSON report records the commit,
working-tree state, platform, Go version, commands, bounded environment flags,
durations, outcome, and the exact VM/compiler compatibility tuple. That tuple
contains the bytecode, Program envelope, artifact, and compiler schema versions
plus the instruction-set checksum, compiler fingerprint, and detached runtime
limit policy. It does not record tenant source, request bodies, URLs, or
secrets.

KitDB has narrower gates that qualify only its kernel, relational adapters, and
operator commands. Their supported profile and exclusions are frozen in
`kitdb/RELEASE_1_0.md`:

```text
go run ./cmd/releasegate --mode kitdb-verify \
  --report .artifacts/kitdb-verify.json

go run ./cmd/releasegate --mode kitdb-release \
  --require-clean \
  --timeout 90m \
  --report .artifacts/kitdb-release.json
```

Every release report now records both the `kitdb/1` kernel profile and the
Kitwork relational encoding profile. The complete engine release mode includes
the KitDB release campaigns, so a Kitwork release cannot bypass KitDB race,
hard-crash, canary-smoke, or replica-soak evidence. The required 24-hour
storage canary command is documented in `kitdb/RELEASE_1_0.md`; the short gate
does not replace it.

Release qualification proves the engine build. A project database has its own
admission gate: with its writer stopped, run `go run ./cmd/kitdb doctor
<database>`. Admit a controlled rollout only when `controlled_ready` is true;
require `stable_ready` for a general-availability deployment.

Host-registered production policies are exercised by the KitDB node suite and
race gate: restart must rediscover the same verified anchor without duplication,
restore must match `kitdb-logical-digest/v1`, corrupted or over-capacity stores
must fail closed, an optional publisher must return exact destination read-back
evidence, and manager shutdown must release every lease and publisher call. The
built-in directory publisher proves immutable bounded transfer semantics and
restart idempotence, not that its configured path is physically off-host.
Native object-store transport and notification delivery remain deployment
responsibilities in `kitdb/PRODUCTION.md`.

The verification plan also runs one composed KitDB database journey:

```text
struct()/ORM schema and CRUD -> Hrana transaction -> online index
  -> close/reopen -> full verify -> backup anchor -> exact restore
  -> catalog-hydrated ORM and Hrana query
```

Its separate `.artifacts/kitdb-database-gate.json` evidence records only the
database identity, source/backup/restore transaction, backup SHA-256, row
count, platform, and bounded step timings. A second focused gate runs reviewed
WAL-tail, backup/restore, hard-process index recovery, and resumable-import
WAL-before-acknowledgement tests. The clean
journey therefore cannot be mistaken for crash-safety evidence, and neither
step alone is a claim about dishonest storage hardware or unsupported network
filesystems.

The first gate loads the committed VM v2 compatibility archive without
recompiling its sources and requires each historical Program to decode, verify,
execute, and re-encode unchanged. The next gate runs the manifest-driven VM
fault gauntlet: malformed envelopes and Programs must be rejected
deterministically, execution failures must publish stable diagnostics, and both
reused and pooled VMs must recover without retained state. The gate also
compiles the frozen fixture corpus in six fresh OS processes. All processes must
produce identical artifact identities before the build is allowed to continue.
It separately runs the language conformance corpus through fresh, reused, and
pooled VMs and verifies that the bytecode inspector can explain restored
Programs through canonical metadata. Release mode additionally runs a restart and
recovery campaign across fresh engine processes sharing one application tree
and bytecode cache. It injects truncated, stale-compiler, checksum-corrupt, and
deleted artifacts, then performs an intentional abrupt stop. Every restart must
serve the same response, restore the same artifact hash, preserve healthy cache
hits, and leave no temporary cache files. Its bounded evidence is written to
`.artifacts/restart-campaign.json`.

Before generation-level retention and restart checks, release mode runs the VM
value-pressure campaign. It repeatedly creates large script arrays and object
chains, accepts buffers and reflected collections from native capabilities, and
returns every VM to the pool. Forced-GC checkpoints must plateau and every lease
must balance. Its bounded evidence is written to
`.artifacts/value-pressure.json`:

```text
KITWORK_VALUE_PRESSURE=1 go test ./runtime \
  -run '^TestVMValuePressureCampaign$' \
  -count=1 -timeout=10m -v
```

Run that campaign by itself while investigating startup or cache changes:

```text
KITWORK_RESTART_CAMPAIGN=1 go test ./core \
  -run '^TestEngineRestartRecoveryCampaign$' \
  -count=1 -timeout=10m -v
```

It defaults to 24 child processes over three source revisions. Set
`KITWORK_RESTART_CYCLES` from 1 through 256 for a shorter investigation or a
longer bounded campaign. `KITWORK_RESTART_REPORT` optionally selects a JSON
evidence file relative to the engine repository.

Release mode then runs the concurrent cache campaign. Each round starts every
child engine and HTTP server first, waits until all children report ready, and
releases one filesystem barrier so their first requests contend for the same
cache key:

```text
KITWORK_CONTENTION_CAMPAIGN=1 go test ./core \
  -run '^TestEngineCacheContentionCampaign$' \
  -count=1 -timeout=10m -v
```

The default campaign uses eight processes and four requests per process across
cold-cache, warm-cache, corrupt-cache, and new-source-revision rounds: 32 fresh
processes and 128 requests in total. `KITWORK_CONTENTION_WORKERS` accepts 1
through 32 and `KITWORK_CONTENTION_REQUESTS` accepts 1 through 32. Its bounded
JSON report records p50/p95/max first-response latency, barrier start spread,
artifact identity, cache-hit preservation, repair, and drain results without
recording source, paths, hostnames, URLs, or environment values.

## Synthetic canary

The built-in canary starts a real Kitwork engine behind an HTTP server. Its
route exercises compiler output, closures, array callbacks, the VM pool, a
native HTTP capability, generation rewrites, publication, retirement, and final
drain.

Run it for 24 hours when preparing a release candidate:

```text
go run ./cmd/canary \
  --duration 24h \
  --workers 4 \
  --interval 100ms \
  --reload-every 10s \
  --report-every 1m \
  --max-error-rate 0 \
  --json .artifacts/canary-24h.json \
  --bundle .artifacts/canary-24h.zip \
  --heap
```

The diagnostic ZIP is private operational evidence. Do not publish a heap
profile from a production process without reviewing it.

## Deployment canary

The same runner can probe an existing deployment for 24 to 72 hours:

```text
go run ./cmd/canary \
  --url https://example.com/health \
  --contains healthy \
  --duration 72h \
  --workers 2 \
  --interval 1s \
  --request-timeout 5s \
  --max-error-rate 0.001 \
  --json .artifacts/deployment-72h.json
```

The request uses the complete configured URL, but the report removes URL
credentials, query parameters, and fragments. Prefer a dedicated unprivileged
health route rather than putting credentials in a URL. External mode never
collects a local heap profile or diagnostic bundle.

## Acceptance criteria

A release candidate is ready for controlled rollout only when:

- the release gate passes on Windows and Linux for the same commit;
- VM v2 and current compiler-schema contract checks remain unchanged or have an explicit
  reviewed version migration;
- the VM v2 compatibility archive, VM fault gauntlet, language conformance, and
  bytecode inspector contracts pass unchanged;
- runtime limit boundaries and the limit snapshot in release evidence match the
  reviewed policy;
- the KitDB journey preserves one identity and exact transaction through
  verify, backup, restore, ORM reopen, and authenticated Hrana query;
- the focused KitDB recovery step passes its WAL-tail, backup/restore,
  hard-process index publication, and resumable-import publication boundaries;
- the compiled KitDB compatibility profile still matches the reviewed
  `kitdb/1` contract, including durable format versions and hard transaction
  limits;
- the KitDB release campaign passes kernel and relational race coverage, ten
  repetitions of replica/catalog/import/index hard-crash matrices, and the
  seeded replica crash soak;
- the synthetic canary has zero request failures and reports a healthy drain;
- the deployment canary stays below its declared error-rate threshold;
- diagnostics finish with zero active VM leases and zero in-flight requests;
- VM value-pressure evidence preserves deterministic instruction/energy counts,
  balanced pool reuse, and a post-GC heap/object/goroutine plateau;
- retention evidence remains inside the heap, object, goroutine, and slope
  gates;
- restart evidence proves deterministic recovery for every injected cache
  fault, preserves cache-hit modification times, and records no orphaned
  temporary artifact;
- concurrent cache evidence proves synchronized child processes return the same
  response, converge on the same artifact checksum, preserve warm artifacts,
  drain cleanly, and leave no temporary publication file;
- every unexplained race, panic, corrupt artifact, or lifecycle timeout blocks
  promotion.

The canary is a lifecycle probe, not a peak-load generator. Use the VM and
production-handler benchmarks for performance comparisons, and a separate load
tool when validating network capacity.

## CI evidence

`.github/workflows/ci.yml` runs verification, focused race coverage, and fuzz
smokes on both Windows and Linux. Scheduled and manually dispatched campaigns
also run the full release gate, including VM value pressure, restart/recovery,
a short synthetic canary, and upload JSON/ZIP evidence for inspection. The
short CI canary catches regressions; it does not replace the 24 to 72 hour
release campaign.

Promote in stages: local release gate, cross-platform CI, synthetic canary,
single deployment canary, then wider traffic. Roll back on a failed invariant;
do not compensate by raising a gate until the retained evidence explains why.
