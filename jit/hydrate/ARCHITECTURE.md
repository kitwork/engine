# Kitwork Client Runtime

`hydrate.Runtime()` is the delivery contract. It composes the files below into one cacheable
`/kit.js` response and the same core source generated for the standalone `@kitwork/kitjs` export.

The modules are composed eagerly to preserve one stable asset. Their registry boundaries are also
the future only-used delivery boundary: a module can move out of core without changing its public
API.

## Composition order

1. `bridge.js`
   Adopts or creates the canonical `window.kit` root. It detects an injected native adapter and owns
   request/reply correlation, events, timeouts and teardown.

2. `kernel.js`
   Owns the module registry, lifecycle, expression compiler and walker, scopes, state, components,
   directives, delegated events and the curated expression service facade.

3. `modules/native.js`
   Maps trusted `kit.*` platform services to the private native bridge.

4. `modules/storage.js`
   Selects native storage or the origin-scoped browser fallback.

5. `modules/web.js`
   Exposes browser-backed dialog, clipboard, camera, share, theme and navigation services.

6. `modules/component-loader.js`
   Provides optional remote component loading and IndexedDB persistence. It is inert unless
   `kit.cdnComponents` is configured.

7. `morph.js`
   Owns keyed DOM reconciliation and calls the kernel cleanup contract before removing nodes.

8. `compat.js`
   Provides compatibility for existing JIT action modules and the deprecated global aliases.

9. `drive.js`
   Provides optional same-origin navigation, head reconciliation, prefetch, progress and scroll
   restoration.

10. `boot.js`
    Starts the kernel only after every core module has registered.

## Public roots and responsibilities

- `window.kit` is the canonical trusted JavaScript root.
- `window.kitwork` and `window.hydrate` are compatibility aliases to the same object.
- Platform/runtime capabilities live on `kit.*`.
- The raw `kit.bridge` transport is an implementation seam for runtime modules and native shells.
  It is not an authored-expression API.
- Authored expressions resolve `kit` through a curated, read-only facade. Only explicit service
  grants are visible; kernel controls and the raw bridge are denied.
- `$app` is not installed by the kernel. An application may register an ordinary component and
  explicitly alias its instance as `$app`:

```js
kit.component("app", {
  copied: false,
  copy: function () {
    var self = this;
    return kit.clipboard.writeText("hello").then(function () {
      self.copied = true;
    });
  }
});
```

```html
<main data-kit-component="app" data-kit-alias="$app">
  <button data-kit-click="$app.copy()">Copy</button>
</main>
```

This division keeps application state and lifecycle in the component layer while reusable OS/web
services stay on the platform layer.

## Runtime contracts

- A module registers with `kit.module(name, value)` and guards with `kit.has(name)`.
- A platform capability registers with
  `kit.service(name, value, { expression: ["exactMethod"] })`. The registry installs the trusted
  `kit.<name>` object and derives a separate, read-only expression facade from the exact grants.
- Service names do not expose raw dispatchers. Capability identifiers match public paths, such as
  `camera.capture` and `clipboard.writeText`.
- Startup work registers with `kit.onStart(callback)`.
- Global resources register with `kit.cleanup(callback)`.
- Element resources register with `kit.onCleanup(element, callback)`.
- Private cross-module lifecycle helpers live on non-enumerable `kit.internal`.
- `data-kit-*` is authored source. On expression directives, `data-kitwork-*` is engine-emitted IR.
- Client and Go walkers share short-circuit semantics and blocked member names.
- No runtime module may use `eval` or `new Function`.

Platform service methods such as `kit.clipboard.writeText()` and `kit.camera.capture()` return
Promises on both web and native paths. The expression walker remains synchronous. Delegated
click/away/escape effects and component `init()` observe a returned thenable and schedule a render
when it resolves or rejects, so async component methods do not need to call `kit.render()` manually.

Component aliases must match `$[A-Za-z][A-Za-z0-9_]*`. Kernel-owned handles are reserved, and an
alias can never replace the curated `kit` identifier.

## Private bridge contract

A native shell may seed `window.kit.bridge` at document start. Runtime modules call its
`call(action, params)` method and receive a Promise. The bridge owns transport-specific details;
app components and authored expressions call the corresponding `kit.*` service instead.

```text
app component -> kit.camera.capture() -> private bridge -> native shell
```

The canonical built-in shapes are noun-first namespaces:

```text
kit.theme.mode / resolved / set() / toggle()
kit.clipboard.writeText() / readText() / copy()
kit.camera.capture()
kit.navigation.back() / forward() / reload()
kit.window.minimize() / maximize() / restore() / close()
kit.capabilities.supports()
```

Origin checks, permissions and payload validation remain native-shell responsibilities.

## Adding a module

Keep the kernel unaware of the implementation and register a small interface:

```js
(function (window) {
  var kit = window.kit;
  if (!kit || !kit.module || !kit.service || kit.has("example")) return;

  var example = {
    run: function () {
      return true;
    }
  };

  kit.service("example", example, { expression: ["run"] });
  kit.module("example", example);
})(window);
```

Add its embed and explicit position in `render.go`, then extend the relevant behavior contract test.
Run `node --check` for every runtime file and:

```text
go test ./jit/hydrate ./jit/js
go vet ./jit/hydrate ./jit/js
```
