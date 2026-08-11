# Plain Shop demo

Each route loads one sealed, immutable Hydrate artifact. That artifact contains
the Hydrate runtime, the exact four-component manifest, and the readable
`shop.js` package source:

```text
shop-products@1.0.0
shop-cart@1.0.0
shop-checkout@1.0.0
shop-dialog@1.0.0
```

The HTML contract keeps the name and version separate:

```html
<aside data-kit-component="shop-cart" data-kit-version="1.0.0"></aside>
```

From `engine/jit/javascript/vanilla`, reproduce the checked-in artifact with:

```powershell
go run ./cmd/assemble -profile hydrate -component shop-products=1.0.0 -component shop-cart=1.0.0 -component shop-checkout=1.0.0 -component shop-dialog=1.0.0 -script shop=examples/shop/shop.js -canonical-dir examples/shop
```

The command is deterministic. With unchanged runtime, manifest, and `shop.js`,
it produces the same filename and bytes. If any input changes, update the one
artifact URL in all three HTML routes and keep the old artifact for old pages.

Hydrate navigation needs same-origin HTTP. Opening these files through `file://`
uses normal browser navigation and therefore reloads the page.

From `engine/jit/javascript/vanilla`, run:

```powershell
python -m http.server 4173
```

Then open:

```text
http://localhost:4173/examples/shop/products.html
```

All three routes publish the exact same artifact URL. Drive can therefore morph
between them without executing an incoming component package. A direct load of
any route receives the complete runtime and component graph from that one URL.

The checkout confirmation sends two independent component commands. It is not
a cross-component transaction: `shop-cart.clear()` deliberately returns `true`
before the checkout component commits its deterministic success state.
