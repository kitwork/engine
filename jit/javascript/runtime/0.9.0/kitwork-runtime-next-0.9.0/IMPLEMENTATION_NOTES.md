# Kitwork Runtime Next — Implementation Notes

## Status

This is a runnable client runtime core, not merely an expression engine. It is
also not yet the final Kitwork Runtime 1.0 distribution.

## Architectural improvements over the previous monolith

1. Global document-level registries have been replaced by isolated app records.
2. Per-node, component, and binding ownership is explicit.
3. Expressions are private AST programs compiled by directive mode.
4. Directives are registered through contracts instead of a central generic
   mirroring branch.
5. State changes invalidate the nearest component/scope boundary instead of
   requiring a full-document scan.
6. DOM additions hydrate only their subtree; removed nodes are cleaned after a
   microtask so DOM moves do not unmount accidentally.
7. Component aliases and refs have app/component ownership and deterministic
   cleanup.
8. Structural `if` and keyed `for` preserve lifecycle and node identity.
9. Platform/domain services use explicit expression grants.
10. Asynchronous tasks have component ownership and abort semantics.

## Integration strategy

Do not load this runtime together with the existing KitJS kernel on the same
page. Integrate it on a separate branch or behind an Engine feature flag.

Recommended order:

1. Add the modular source under `engine/jit/javascript/`.
2. Run the included tests unchanged.
3. Point one small application/island at the new bundle.
4. Implement the Go expression validator against
   `test/expression-conformance.json`.
5. Port Drive/morph into `drive/`, using `runtime.cleanupTree`, app persisted
   registries, and logical ownership.
6. Add compiler-side migration diagnostics for legacy directives.

## Observable compatibility decisions

The core intentionally does not understand:

```text
data-kitwork-*
data-kit-action
data-kit-away
data-kit-guard
data-kit-debounce
$el
$root
$props
public kit.compile / kit.run
```

Legacy support belongs in a compiler rewrite or a separate compatibility
capability, not in the new core.
