# Sealed preferences demo

This page loads one immutable KitJS artifact containing exactly:

```text
KitJS Kit profile 0.8.0
storage@1.0.0
preferences@1.0.0
```

The component source remains readable in `preferences.js`. HTML calls only
`choose()` and `reset()`; only trusted component JavaScript can see
`kit.storage`.

From `engine/`, reproduce the checked artifact with:

```powershell
go run ./jit/javascript/vanilla/cmd/assemble -profile kit -service storage=1.0.0=jit/javascript/vanilla/service/storage/1.0.0.js -component preferences=1.0.0 -component-require preferences=storage=1.0.0 -script preferences=jit/javascript/vanilla/examples/preferences/preferences.js -canonical-dir jit/javascript/vanilla/examples/preferences
```

With unchanged runtime and package bytes, the command produces the same file:

```text
kit.0.8.0.9d6b2fa971ce899a859dbab01c0a50e6d8f30889c68ef075ee98921d027c1c8c.js
```

The page loads the shared checked, content-addressed `../kitjs.examples.<sha256>.css`, generated only from
literal Tailwind utility candidates by Kitwork's JIT CSS engine. The exact
rebuild command is documented in the Drive progress README; no Tailwind CDN,
CLI, custom rules, or runtime class generator is used.

Serve the Vanilla directory over HTTP:

```powershell
cd jit/javascript/vanilla
python -m http.server 4173
```

Then open `http://localhost:4173/examples/preferences/`.
