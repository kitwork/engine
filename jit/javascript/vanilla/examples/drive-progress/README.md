# Actual Drive progress demo

This demo does not simulate work. `progress@1.0.0` adapts the real
`kit:navigation` lifecycle emitted by Hydrate Drive into one reusable state
stream. `progress-bar@2.0.0` subscribes to that service and owns only two UI
values (`visible` and `value`) plus its hide timer:

```text
start -> zero or more measured progress events -> one finish
```

Unknown response size stays indeterminate. A percentage is shown only when
Drive supplies real finite `loaded` and `total` bytes. A successful finish
shows 100% briefly. Cancellation, error, and fallback finishes hide the bar
immediately.
The measured bytes belong only to the Drive HTML response; the bar does not
claim to measure later image, font, or stylesheet loading.

Both routes identify the same component host explicitly without borrowing the
HTML `id` contract:

```html
<section
  data-kit-retain="app-progress"
  data-kit-component="progress-bar"
  data-kit-version="2.0.0">
</section>
```

They also contain the same immutable artifact URL (filled after the retain
runtime is sealed):

```text
hydrate.kit.0.8.0.9a1f9c39f86cecbc71157cb3ef2e28363f2f8989ab04d1ff3491eac0c2dde534.js
```

The filename hash identifies the exact Hydrate runtime, `progress@1.0.0`,
`progress-bar@2.0.0`, and the component-to-service dependency edge. Rebuilding
unchanged inputs reproduces this path; changed bytes or metadata produce a
different immutable path.

From `engine/`, reproduce it with:

```powershell
go run ./jit/javascript/vanilla/cmd/assemble -profile hydrate -service progress=1.0.0=jit/javascript/vanilla/service/progress/1.0.0.js -component progress-bar=2.0.0 -component-require progress-bar=progress=1.0.0 -script progress-bar=jit/javascript/vanilla/component/progress-bar/2.0.0.js -canonical-dir jit/javascript/vanilla/examples/drive-progress
```

The checked `../kitjs.examples.cb95d3a46e8563f61a36a45167e067f1a1c3e74dbfc504a175358b23802dc881.css`
is generated from the literal Tailwind
utilities in this demo and the preferences demo by Kitwork's JIT CSS engine. It
contains no hand-written rules, CDN dependency, or runtime class generator:

```powershell
go run ./jit/javascript/vanilla/cmd/styles -canonical-dir jit/javascript/vanilla/examples jit/javascript/vanilla/examples/preferences/index.html jit/javascript/vanilla/examples/drive-progress/index.html jit/javascript/vanilla/examples/drive-progress/next.html jit/javascript/vanilla/examples/request-form/index.html jit/javascript/vanilla/examples/request-form/design.html
```

The slow, fast, and error query variants are interpreted by the Go browser test
server. A plain static server still demonstrates real, normally fast Drive
navigation between `index.html` and `next.html`.
