# Implementation Notes — Unified Runtime 1.0.0-draft

## Integration decision

This package replaces the earlier sequence of separate reference kernels with one integrated M1–M5 runtime. The browser artifact is `dist/kitwork-runtime.js`; all subsystems share the same app records, expression engine, scheduler and lifecycle.

## Important invariants

1. Only `data-kit-*` is accepted as authored syntax.
2. AST is private and cached; it is not emitted into HTML or exposed as an ABI.
3. `data-kit-scope` initializes once. Re-render and matching Drive reuse do not reseed it.
4. A component host owns exactly one instance. SSR host scope overrides initial state only during first activation.
5. Every binding owns only the DOM mutations it created.
6. Structural removal always passes through `cleanupTree()`.
7. Drive pauses the app MutationObserver during a controlled morph, then rehydrates through the same lifecycle.
8. Matching component hosts preserve their instance; removed/replaced hosts unmount.
9. Pending component tasks abort on unmount.
10. `data-kit-persist` preserves a keyed node only when the incoming tree declares the same key.

## Drive morph policy

Matching priority:

```text
data-kit-persist
→ id
→ data-kit-key
→ compatible current-position node
→ compatible unkeyed sibling
→ clone incoming node
```

A node is compatible when node type and tag match. Component hosts additionally require the same `data-kit-component` name.

Before incoming attributes are applied, non-event bindings on the reused element are unmounted. This restores their author-owned DOM snapshot. The incoming attributes are then synchronized and bindings are recompiled from the new markup. The component/scope record itself is preserved.

## Form preservation

Drive captures controls that are active, dirty or owned by `data-kit-model`. It restores:

- `value`
- `checked`
- selected options
- focus
- selection range/direction
- element scroll position

File inputs are never written programmatically.

## Script policy

Incoming body/app scripts are inserted inertly and activated after morph. External scripts are deduplicated unless `data-kit-reload` is present. Inline scripts run for each incoming page. Runtime scripts in the document head are not re-executed by body morphing.

## Known boundary

The status remains `1.0.0-draft`, not a frozen compatibility guarantee. The JavaScript implementation and browser integration tests are complete for M1–M5, but Go-side parser/validator parity must still run the shared expression fixtures before declaring Engine-wide 1.0 compatibility.
