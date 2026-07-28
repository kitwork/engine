# Kitwork Client Runtime

`hydrate.Runtime()` is the only delivery contract. It composes the files below into one cacheable
`/kit.js` response and the same source used by `@kitwork/kitjs`.

The modules are composed eagerly today to preserve one stable asset. Their registry boundaries are
also the future delivery boundary: JIT can emit only-used modules later without changing kernel code
or public APIs.

## Composition Order

1. `bridge.js`
   Detects an injected native adapter and owns request, response, event, timeout, and teardown.

2. `kernel.js`
   Owns the module registry, lifecycle, expression compiler and walker, scopes, state, directives,
   components, delegated events, remember, API data sources, and live streams.

3. `modules/native.js`
   Exposes native-only capabilities through the bridge.

4. `modules/storage.js`
   Selects native storage or the origin-scoped browser fallback.

5. `modules/web.js`
   Exposes browser-backed dialog, clipboard, camera, share, theme, and navigation capabilities.

6. `modules/component-loader.js`
   Optional remote component loading and IndexedDB persistence. It is inert unless
   `kitwork.cdnComponents` is configured.

7. `morph.js`
   Owns keyed DOM reconciliation and calls the kernel cleanup contract before removing nodes.

8. `compat.js`
   Provides `kitwork.components.action/target/state/fire` for existing JIT verb modules.

9. `drive.js`
   Optional same-origin navigation, head reconciliation, prefetch, progress, and scroll restore.

10. `boot.js`
    Starts the kernel only after every module has registered.

## Contracts

- There is one public root: `window.kitwork`.
- A module registers with `kitwork.module(name, value)` and guards with `kitwork.has(name)`.
- Startup work registers with `kitwork.onStart(callback)`.
- Global resources register with `kitwork.cleanup(callback)`.
- Element resources register with `kitwork.onCleanup(element, callback)`.
- Private cross-module lifecycle helpers live on non-enumerable `kitwork.internal`.
- `data-kit-*` is author source; `data-kitwork-*` is engine-emitted IR.
- Client and Go walkers share short-circuit semantics, blocked member names, and a 10,000-node budget.
- No module may use `eval` or `new Function`.

## Adding A Module

Keep the kernel unaware of the implementation. Register a small interface:

```js
(function (window) {
  var kitwork = window.kitwork;
  if (!kitwork || !kitwork.module || kitwork.has("example")) return;

  var example = {
    run: function () {
      return true;
    }
  };

  kitwork.module("example", example);
})(window);
```

Add its embed and explicit position in `render.go`, then extend the Node behavior contract in
`bridge_test.go`. Run `node --check` for every runtime file and:

```text
go test ./jit/hydrate ./jit/js
go vet ./jit/hydrate ./jit/js
```
