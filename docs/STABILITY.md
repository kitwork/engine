# Kitwork Engine Stability Contract

This document defines the invariants that must remain true while the engine is
refactored. A change is not complete until the relevant invariant has an
automated test on the production execution path.

## 1. App and site isolation

- A pooled VM must not retain bytecode, constants, globals, builtins, request
  variables, execution hooks, or gas configuration from its previous owner.
- Resetting a VM must detach a root scope that escaped into a closure. It must
  never clear a map still owned by that closure.
- App-scoped caches, databases, persisted data, and resolved paths must remain
  inside that app's identity.
- Configured database connections are app-owned and shared by sibling sites.
  Site eviction and generation replacement must not close them; app shutdown
  closes every connection exactly once.
- Cross-app isolation must be tested with concurrent execution, not only with
  sequential unit tests.

Current enforcement:

- `runtime.VM.ResetForPool` removes tenant-owned VM state.
- `runtime.VM.FastReset` preserves escaped root closures by allocating a fresh
  root scope.
- `app.Pool.Release` sanitizes a VM before pooling it.
- `core.Engine` registers one `app.Runtime` per identity and one child
  `site.Runtime` per domain.
- Hot reload preserves the app/site runtime pair; site eviction closes only the
  selected child.
- `app.Runtime` owns database connections, detached work, and the scheduler.
  Compatibility tenants may adapt those resources but never close them unless
  they own and close the entire app runtime.
- A site generation is prepared before publication, activates monotonically,
  cannot be reactivated after replacement, and retires only after all accepted
  request leases drain.
- Route declarations are prepared before generation activation. The published
  JIT, theme, favicon, and asset snapshot is immutable for every request pinned
  to that generation.
- `site.Generation` owns the prepared route graph and closes it only after
  accepted requests drain. `work.Tenant` may adapt that graph but must not own,
  discover, compile, or republish route nodes on the request path.
- `site.Generation` owns an immutable HTML snapshot and prepared render plan.
  Template edits create a replacement generation; malformed candidates cannot
  replace the last valid renderer. Normal request rendering must not read
  templates from disk.
- The executable source manifest includes every router and native import plus
  watched route directories. Any source-graph change creates a new generation;
  active route nodes are never recompiled in place.
- A failed replacement cannot change the current generation. The candidate is
  retired and the last valid generation continues serving.
- `.env`, templates, render plans, presentation, source manifests, RAM
  response/fetch cache, and `LifetimeSite` capabilities are generation-scoped.
  Reload replaces them only after a complete candidate is ready.
- Disk-persisted responses, rate-limit budgets, SSE connections, and replay
  history are site-scoped. They survive generation replacement and close only
  when that site leaves the app runtime.

## 2. Energy and bounded execution

- Every opcode consumes energy.
- Execution stops with an invalid value after exceeding `MaxEnergy`.
- Background and scheduled execution use the same energy ceiling as request
  execution.
- Request and detached-work contexts are checked by both VM dispatch loops.
  Cancellation interrupts running script execution within at most 64 opcodes;
  energy remains the hard upper bound.

## 3. Capability lifetime

Every capability declares one lifetime:

- `LifetimeTransient`: a new instance on every resolution. Use for mutable
  builders such as QR code and HTTP request configuration.
- `LifetimeRequest`: one instance per `request.Scope`. Repeated resolution in
  one request reuses it; separate requests never share it. Off-request
  execution degrades safely to transient.
- `LifetimeSite`: one instance per loaded site generation. It is closed when
  that generation retires after its request leases drain, including hot reload.
- `LifetimeApp`: one instance per identity-wide `app.Runtime`, shared by its
  scheduler and sibling domains, and closed only when the app unloads.
- `LifetimeSingleton`: one process-wide instance, closed with the registry.

`Register` defaults to `LifetimeSite`; broader sharing must be explicit. An
app-scoped factory must execute at most once under concurrent resolution and
must not retain site/request state. Cached values that implement
`capabilities.Closer` are closed outside cache locks. A closed owner cache is
terminal and cannot recreate resources after shutdown.

Request shutdown cancels its context, releases the primary VM, waits for
accepted child VM leases, and then closes request-scoped capabilities. SSE must
release the primary VM before blocking on the stream.

An authenticated principal and its permissions are immutable request inputs
attached by trusted host middleware. A capability with declared permissions
must be denied before its factory runs. App- and site-scoped factories receive
their stable owner scope, never a request scope retained only for authorization.

## 4. Detached background work

`kitwork().go(fn)` must snapshot its execution inputs before returning:

- builtins, globals, top-level variables, arguments, and the lambda scope chain;
- the bytecode, constants, and source map carried by the lambda's folder program;
- the app energy limit.

The detached lambda must not reference a mutable request frame that can be
returned to the VM pool. Every accepted task is tracked by its app. App shutdown
stops accepting tasks, cancels pending work, and waits for accepted work to
finish before closing app resources.

Site eviction and generation replacement do not cancel app-owned detached
work. The app task group drains before scheduler and app capabilities close;
database connections close last.

## 5. Public API compatibility

- Methods exported through `import { ... } from "kitwork"` are public API.
- Removing or changing a public method requires an explicit deprecation period.
- Contract tests must execute representative `.kitwork.js` fixtures through
  the real compiler, VM, router, and response path.
- Unit tests that only assert a non-nil adapter do not prove integration.

## 6. Required verification

Before merging an engine change:

```text
go build ./...
go test ./...
go vet ./...
go test -race ./...
```

Changes to the lexer, parser, compiler, VM, value conversion, templates, or
path handling must also add seeds to the relevant fuzz target.

## 7. Stability before expansion

Read-only logic capsules may build on these invariants. Capsule writes,
multi-node placement, and new transport backbones must wait until app isolation,
capability permissions, cancellation, and production contract tests are stable.

## 8. Preflight parity

`kitwork check` validates with the same route graph and render-plan preparation
path used by production. It reports every discovered failure in one pass and
never opens a listener, activates a generation, starts cron, or leaves candidate
resources alive. Preflight diagnoses invalid paths and imports; it never guesses
or rewrites application source.
