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
- Cross-app isolation must be tested with concurrent execution, not only with
  sequential unit tests.

Current enforcement:

- `runtime.VM.ResetForPool` removes tenant-owned VM state.
- `runtime.VM.FastReset` preserves escaped root closures by allocating a fresh
  root scope.
- `app.Pool.Release` sanitizes a VM before pooling it.

## 2. Energy and bounded execution

- Every opcode consumes energy.
- Execution stops with an invalid value after exceeding `MaxEnergy`.
- Background and scheduled execution use the same energy ceiling as request
  execution.
- Cancellation prevents work that has not started. Running script execution is
  bounded by energy until VM-level context cancellation is implemented.

## 3. Capability lifetime

Every capability declares one lifetime:

- `LifetimeTransient`: a new instance on every resolution. Use for mutable
  builders such as QR code and HTTP request configuration.
- `LifetimeRequest`: reserved for the future request capability cache. Until
  that cache exists, it degrades safely to transient.
- `LifetimeApp`: one instance per app, closed when the app unloads.
- `LifetimeSingleton`: one process-wide instance, closed with the registry.

An app-scoped factory must execute at most once under concurrent resolution.
Cached values that implement `capabilities.Closer` are closed outside cache
locks.

## 4. Detached background work

`kitwork().go(fn)` must snapshot its execution inputs before returning:

- builtins, globals, top-level variables, arguments, and the lambda scope chain;
- the bytecode, constants, and source map carried by the lambda's folder program;
- the app energy limit.

The detached lambda must not reference a mutable request frame that can be
returned to the VM pool. Every accepted task is tracked by its app. App shutdown
stops accepting tasks, cancels pending work, and waits for accepted work to
finish before closing app resources.

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
