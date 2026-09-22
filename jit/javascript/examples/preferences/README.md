# Sealed preferences demo

This page loads one immutable KitJS artifact containing exactly:

```text
KitJS Kit profile 1.0.0-rc.2
storage@1.0.0
preferences@1.0.0
```

The component source remains readable in `preferences.js`. HTML calls only
`choose()` and `reset()`; only trusted component JavaScript can see
`kit.storage`.

From `engine/`, reproduce the checked artifact with:

```powershell
go run ./jit/javascript/cmd/assemble -profile kit -service storage=1.0.0=jit/javascript/service/storage/1.0.0.js -component preferences=1.0.0 -component-require preferences=storage=1.0.0 -script preferences=jit/javascript/examples/preferences/preferences.js -canonical-dir jit/javascript/examples/preferences
```

With unchanged runtime and package bytes, the command produces the same file:

```text
kit.1.0.0-rc.2.0d0e24973796ac3565a4d1e0219de25b46d5c36fe5a9819b95d65b1ef0f7f7b1.js
```

The page loads the shared checked, content-addressed `../kitjs.examples.<sha256>.css`, generated only from
literal Tailwind utility candidates by Kitwork's JIT CSS engine. The exact
rebuild command is documented in the Drive progress README; no Tailwind CDN,
CLI, custom rules, or runtime class generator is used.

Serve the KitJS directory over HTTP:

```powershell
cd jit/javascript
python -m http.server 4173
```

Then open `http://localhost:4173/examples/preferences/`.
