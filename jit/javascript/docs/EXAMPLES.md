# KitJS examples

The examples in this directory target KitJS `0.9.0-next.12`. They use literal
Tailwind utility classes only; there are no hand-written component styles or
runtime-generated class names. Read the [browser contract](https://github.com/kitwork/kit.js/blob/master/KITJS_SPEC.md)
and [runtime README](../README.md) before treating an example as API guidance.

## Small standalone examples

These pages load the readable Kit development artifact and are useful while
working on the browser kernel:

- [`counter.html`](../examples/counter.html): component state, actions, and text.
- [`dropdown.html`](../examples/dropdown.html): `show`, safe `bind`, outside click, and Escape.
- [`form.html`](../examples/form.html): model coercion, IME handling, classes, and component methods.
- [`list.html`](../examples/list.html): template-only `if`, keyed `for`, and row reuse.
- [`dialog.html`](../examples/dialog.html): an action-only component alias and lexical callback.
- [`hydrate-home.html`](../examples/hydrate-home.html) and [`hydrate-next.html`](../examples/hydrate-next.html): direct load and same-document Hydrate continuity.

From `engine/jit/javascript`, serve them over same-origin HTTP:

```powershell
python -m http.server 4173
```

Then open `http://127.0.0.1:4173/examples/counter.html`. Choose one profile per
document: `kit.js` for reactive HTML or `hydrate.kit.js` for the same runtime
plus private Morph and Drive continuity. No `data-kit-app` or
`data-kit-hydrate` marker is needed.

A data-only counter needs no component registration:

```html
<section
  data-kit-scope="count: 0"
  class="inline-flex items-center gap-3 rounded-2xl border border-slate-200 p-4">
  <button
    type="button"
    data-kit-click="count = count - 1"
    class="rounded-lg bg-slate-900 px-3 py-2 font-bold text-white">
    -
  </button>
  <output data-kit-text="count" class="min-w-10 text-center font-mono text-xl">0</output>
  <button
    type="button"
    data-kit-click="count = count + 1"
    class="rounded-lg bg-indigo-600 px-3 py-2 font-bold text-white">
    +
  </button>
</section>
<script src="/kit.js"></script>
```

Use a component when the boundary needs trusted methods or lifecycle cleanup:

```html
<section
  data-kit-component="counter"
  data-kit-scope="count: 3"
  class="rounded-2xl bg-slate-950 p-6 text-white">
  <button type="button" data-kit-click="reset()" class="rounded-lg bg-indigo-500 px-4 py-2">
    Reset <span data-kit-text="count">3</span>
  </button>
</section>

<script src="/kit.js"></script>
<script>
  kit.component("counter", {
    count: 0,
    reset() {
      this.count = 0;
    }
  });
</script>
```

The seed may override only a declared writable non-function data field. Generic
npm artifacts have no component manifest, so direct registrations use an
unversioned host.

When a third-party library owns a subtree, make that ownership explicit:

```html
<section data-kit-ignore class="rounded-2xl border border-slate-200 p-4">
  <div id="external-chart"></div>
</section>
```

Neither Kitwork's scanner nor the browser runtime interprets that host or its
descendants, and Hydrate treats the subtree as opaque. This is not component
retention; use `data-kit-retain` when a live KitJS component must survive
compatible navigation.

## Sealed service examples

These examples load immutable artifacts whose exact service/component graph is
embedded before boot:

- [`preferences/`](../examples/preferences/): `storage@1.0.0` used only by trusted component JavaScript.
- [`drive-progress/`](../examples/drive-progress/): real Hydrate navigation events adapted by `progress@1.0.0` and presented by `progress-bar@2.0.0`.
- [`request-form/`](../examples/request-form/): same-origin JSON requests, latest-wins cancellation, normalized errors, and shared progress.
- [`shop/`](../examples/shop/): one closed component graph shared by three Hydrate routes.

The request-form demo includes a successful standard-library API. From
`engine/`, run:

```powershell
go run ./jit/javascript/examples/request-form/server
```

Open `http://127.0.0.1:4174/request-form/index.html`, then choose **Save
profile**. Its [README](../examples/request-form/README.md) documents the exact
request and graph rebuild command.

The Shop demo requires same-origin HTTP. From `engine/jit/javascript`, run the
same `python -m http.server 4173` command and open
`http://127.0.0.1:4173/examples/shop/products.html`. Opening it with `file://`
correctly uses normal browser navigation instead of Drive.

## Rebuilding an immutable graph

Every component/service name has exactly one version in a graph. The HTML keeps
component name and version in separate attributes; versions are selected by
the build, never by the browser. For example, from `engine/`:

```powershell
go run ./jit/javascript/cmd/assemble -profile hydrate `
  -service progress=1.0.0=jit/javascript/service/progress/1.0.0.js `
  -component progress-bar=2.0.0 `
  -component-require progress-bar=progress=1.0.0 `
  -script progress-bar=jit/javascript/component/progress-bar/2.0.0.js `
  -canonical-dir jit/javascript/examples/drive-progress
```

The output filename includes `0.9.0-next.12` and the SHA-256 of the exact graph
bytes. Keep an old artifact available while any old page or open tab still
references its URL.
