# Runtime Next Roadmap

## M2 — Core runtime (this package)

- Multi-app ownership records.
- Zero-eval expression engine.
- Scope/component/ref/alias lifecycle.
- Core and structural directives.
- Event modifiers and Promise observer.
- Dirty-boundary scheduler and subtree hydration.
- `kit.task`, `kit.request`, service grants.

## M3 — Drive and morph

- Port the proven morph behavior from KitJS instead of rewriting it from memory.
- Preserve keyed/persisted nodes, focus, selection, live form values and scroll.
- Route all removal through `cleanupTree()` and all inserted subtrees through `hydrateTree()`.
- Add head reconciliation, history, abortable navigation and prefetch.
- Emit navigation events; keep progress/announcer presentation outside Drive policy.

## M4 — Capabilities

- `live` / SSE.
- `remember` persistence.
- component loader and cache.
- teleport and transition.
- browser/native platform adapters.
- only-used capability emission from Go.

## M5 — Compatibility and production freeze

- KitJS v1/v2 template rewriter or isolated adapter.
- Go runner for shared conformance fixtures.
- fuzzing for expression and named-map parsers.
- benchmark budgets for startup, hydration, keyed lists, moves and cleanup.
- production canary before declaring Runtime 1.0 frozen.
