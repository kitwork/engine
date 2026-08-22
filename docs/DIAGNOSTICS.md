# Kitwork Runtime Diagnostics

Kitwork diagnostics is a private host API for observing the production runtime
without walking mutable owners or exposing tenant data. It is the data boundary
for future local tooling and an authenticated dashboard; it is not an automatic
HTTP endpoint.

## Snapshot

`core.Engine.Diagnostics()` returns a detached `DiagnosticSnapshot` containing:

- the diagnostic schema version and UTC capture time;
- engine policy and lifecycle state;
- bytecode, artifact, compiler, and instruction-set compatibility identities;
- the detached runtime limits enforced by decoding, verification, calls,
  cancellation, energy defaults, and failure cleanup;
- process architecture, Go version, CPU/GOMAXPROCS, and goroutine count;
- bounded heap and GC counters;
- the existing request, VM, render, cache, generation, and latency health;
- process-local VM pool lease counters.

The snapshot never contains the apps root, hostname, route, URL, request ID,
headers, arguments, source text, environment values, database configuration, or
runtime-owner pointers. Program observation remains checksum-based and bounded.
Callers may serialize or retain a snapshot without pinning an app, site,
generation, Program, request scope, VM, or capability.

The VM pool fields have precise meanings:

- `active`: VMs currently checked out;
- `created`: VMs allocated since process start;
- `acquired`: successful leases since process start;
- `released`: completed leases since process start.

`created` is not idle capacity. Go may discard objects from `sync.Pool` during a
GC and does not notify the pool owner, so Kitwork does not publish an invented
idle count. All four VM pool counters are process-global, not per-engine.

Generation health includes current `preparing`, `activating`, and `draining`
gauges. `draining_leases` is the aggregate lease count of generations whose
owners have entered an observed drain attempt; a generation can briefly be in
the handoff before its retired flag is set. When ownership is available, these
leases are also included in the top-level active generation lease total.
`oldest_drain_nanoseconds` returns to zero when no drain is active, while
`max_oldest_drain_nanoseconds` is the process-local high-water mark.
`drained` and `drain_failures` are observed attempt outcomes, not unique
generation identities; no completed-generation tombstone is retained.
These scalar observations never expose the generation, tenant, route, or URL.

`ownership_snapshot_available` describes the top-level `loaded_apps`,
`loaded_sites`, `active_generations`, and `active_generation_leases` fields. A
false value means the engine ownership lock was busy, so those four fields are
zero and unavailable rather than stale; lifecycle and process-global VM gauges
remain valid.

`policy_snapshot_available` describes the engine uptime, energy/idle policy,
hot-reload, bytecode-cache, and closed fields. Diagnostics tries the engine and
bytecode policy locks separately and never holds both. If either is busy, the
complete policy is unavailable and all policy fields remain zero rather than
publishing a partial mixed-time view; Health remains live.

## Bundle

`core.Engine.WriteDiagnosticBundle` writes a ZIP archive. By default it contains
only `diagnostics.json`:

```go
file, err := os.Create("kitwork-diagnostics.zip")
if err != nil {
	return err
}
defer file.Close()

err = handler.WriteDiagnosticBundle(file, core.DiagnosticBundleOptions{})
```

A caller investigating retention may explicitly add a Go heap profile:

```go
err = handler.WriteDiagnosticBundle(file, core.DiagnosticBundleOptions{
	IncludeHeapProfile: true,
})
```

`heap.pprof` may reveal build paths and function names, and profile generation
can briefly pause the process. Store the bundle privately and enable this option
only for a deliberate investigation. Extract `heap.pprof`, then analyze it with:

```text
go tool pprof heap.pprof
```

## Transport Boundary

The engine does not mount diagnostics under a reserved URL. The embedding host
owns authentication, authorization, rate limiting, storage, and transport. A
future Kitwork dashboard should call this API and expose a redacted JSON view or
one-time private download; it must not inspect `Engine`, `Tenant`, `Generation`,
or VM internals directly.

Any future remote transport requires explicit opt-in and an authorization test
before it can ship. A token in a query string is not acceptable.

## Change Gate

Diagnostic changes must preserve:

- a bounded JSON snapshot;
- privacy tests with request, host, source, and filesystem sentinels;
- detached health maps and latency buckets;
- zero process-global active VM leases after all requests and generations drain;
- zero lifecycle gauges and draining generation leases after engine shutdown;
- prompt unavailable ownership/policy snapshots under lifecycle lock contention;
- a snapshot-only bundle unless heap profiling is explicitly requested;
- race-safe capture during production lifecycle tests.

Increment `DiagnosticSchemaVersion` when removing or changing the meaning or
JSON type of a published field. Adding a backward-compatible field does not
require a schema increment.
