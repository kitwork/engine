# Vanilla KitJS

This directory defines KitJS as an independent browser library first. It has no
Node runtime, package loader, Go server, or SSR dependency. The same readable
source fragments produce two public delivery profiles. KitJS is currently
released as `0.8.0`:

| Profile | Development alias | Immutable production artifact | Contract |
|---|---|---|---|
| Kit | `kit.js` | `kit.0.8.0.<sha256>.js` | component, expression, directive, event, and dirty-boundary runtime |
| Hydrate | `hydrate.kit.js` | `hydrate.kit.0.8.0.<sha256>.js` | the exact Kit profile plus private Morph and Drive continuity before boot |

Choose one artifact; never load both on the same page.

```html
<section data-kit-component="counter">
  <button data-kit-click="count = count - 1">-</button>
  <output data-kit-text="count">0</output>
  <button data-kit-click="count = count + 1">+</button>
</section>

<script src="/kit.js"></script>
<script>
  kit.component("counter", { count: 0 });
</script>
```

The examples may use the stable development aliases while source is changing:

```html
<script src="/kit.js"></script>
```

Choose the Hydrate profile when the site wants safe same-document navigation:

```html
<script src="/hydrate.kit.js"></script>
```

The script profile selects the behavior. Neither profile requires `data-kit-app`,
`data-kit-hydrate`, a plan attribute, or a navigation-root marker. Hydrate uses
`body` as its document replacement root. Morph and Drive remain private
implementation details; they do not expand the public `kit` object.

Hydrate treats the exact resolved external script URL, including its query, as
the compatibility fingerprint. An incoming document must carry the same
Hydrate artifact URL before Drive may mutate the current document. A missing or
different artifact falls back to normal browser navigation. Production CDN and
Go delivery use the canonical immutable filename, for example
`/hydrate.kit.0.8.0.<sha256>.js`. A changed runtime, service/component pin, or
package source produces a new SHA-256 and therefore a new URL. Previously published
canonical files remain byte-for-byte unchanged so old pages, open tabs, caches,
and rollbacks can keep requesting them.

Drive also falls back before mutation when the incoming document changes its
explicit `<base>` semantics, declares an unregistered component, or contains an
active embedded document such as `iframe`, `object`, `embed`, `frame`, or a meta
refresh. Fetched scripts are never inserted or executed.

Hydrate navigation operates inside a closed component/script graph. Drive does
not execute inline scripts or newly discovered executable scripts from fetched
HTML. Every component definition needed by an incoming body must already be
registered before navigation. A standalone site should therefore bundle the
shared component definitions into its versioned Hydrate artifact. Go/build may
compute the union graph for a compatible route group and prepare one shared
artifact. Any runtime URL or private graph mismatch must hard-navigate; it must
never partially activate a document with a different executable graph. No
authored application marker is required for this check.

Morph preserves dirty form state only for a control whose non-empty `id`, tag,
and input type remain compatible. Unidentified controls are replaced so data
cannot drift into an unrelated field on the next route. Select state is matched
by option value rather than its previous numeric index.

An application-owned component that must survive route layout changes can use
one explicit Morph identity:

```html
<section
  data-kit-retain="app-progress"
  data-kit-component="progress-bar"
  data-kit-version="2.0.0">
</section>
```

`data-kit-retain` preserves that exact host and component scope while Morph
still updates its attributes and children from the incoming document. It is not
an opaque or ignored subtree, and it does not require an `id`. Keys are exact,
case-sensitive values matching `[A-Za-z][A-Za-z0-9._:-]{0,127}`. A key must be
unique in the document and live directly on a component host. Version 1 of this
contract rejects retained hosts inside templates/structural regions and rejects
nested retained hosts. Changing the component name, exact version, alias, tag,
or namespace replaces the host instead of retaining an incompatible scope.

Drive emits one non-cancelable `kit:navigation` event on `document`. Its frozen
detail uses `start`, optional measured `progress`, and exactly one `finish` per
visit ID. Finish outcomes are `loaded`, `cancelled`, `error`, or `fallback`.
Byte progress is emitted only for a trustworthy identity-encoded
`Content-Length` response stream and is capped below 100 until Morph commits;
unknown or encoded response sizes remain indeterminate. This measures the HTML
navigation response, not later image, font, or stylesheet loading.

The entire public base API is:

```text
kit.version
kit.component(name, plainObject)
```

There is no manual start, destroy, mount, unmount, or plugin API.

A sealed artifact may add only the service namespaces selected by its exact
build graph. For example, an artifact containing `storage@1.0.0` adds
`kit.storage`; one containing `request@1.0.0` and its exact
`progress@1.0.0` dependency adds `kit.request` and `kit.progress`. The base
artifact remains exactly the two keys above. The package-time `kit.service()`
registrar is private and is removed before component source runs or
`globalThis.kit` is published.

## Sealed services

Services are trusted reusable JavaScript primitives, not directives, component
hosts, or visible presentation. Version selection happens in Go/build:

```text
request@1.0.0 -> progress@1.0.0
share@1.0.0   -> clipboard@1.0.0
```

The complete Vanilla 0.8.0 catalog currently contains ten exact packages:

| Service | Public surface | Purpose |
|---|---|---|
| `announce@1.0.0` | `say`, `polite`, `assertive`, `clear` | Bounded ARIA live announcements |
| `clipboard@1.0.0` | `writeText`, `readText` | Text clipboard access with normalized errors |
| `cookie@1.0.0` | `get`, `set`, `remove`, `has` | Bounded script-readable cookies |
| `fullscreen@1.0.0` | `request`, `exit`, `active` | Fullscreen browser capability |
| `navigation@1.0.0` | `back`, `forward`, `reload` | Browser history traversal and reload |
| `network@1.0.0` | `online`, `snapshot`, `subscribe` | Browser-reported connectivity state |
| `progress@1.0.0` | `snapshot`, `subscribe`, `start`, `update`, `finish` | Latest-visible operation progress |
| `request@1.0.0` | `send`, `get`, `post`, `abort` | Same-origin bounded requests |
| `share@1.0.0` | `open`, `canShare` | Native share with clipboard fallback |
| `storage@1.0.0` | `get`, `set`, `remove`, `has`, `clear` | Namespaced JSON persistence |

The table describes availability, not default payload. An artifact includes and
publishes only services selected by its exact graph. Every selected namespace
is frozen and carries a non-enumerable exact `version`. A component captures a
selected service lexically:

```js
kit.component("preferences", {
  mode: "system",

  async init() {
    this.mode = await kit.storage.get("theme", "system");
  },

  async choose(mode) {
    await kit.storage.set("theme", mode);
    this.mode = mode;
  }
});
```

Authored expressions can call `choose('dark')`, but cannot read or call
`kit.storage` directly. The browser never fetches a service package at runtime;
its source and exact version are already sealed into the artifact. The private,
frozen graph records both `services` and `components`, so loading the same
artifact is a no-op while a different graph fails before any package source
runs.

`progress@1.0.0` is intentionally a latest-visible operation stream, not an
aggregate task manager. `request@1.0.0` reports trustworthy streamed response
bytes through it. `network@1.0.0` reports only browser connectivity state;
online does not prove that an endpoint is reachable. `cookie@1.0.0` deliberately
cannot create `HttpOnly` authentication cookies. `kit.drive` is not a service:
Morph and Drive remain private parts of the Hydrate profile.

The versioned service API is also the portability boundary. A browser profile
may implement `request@1.0.0` with `fetch`, while a future desktop or mobile
profile may provide the same four methods through a native transport. The
selected implementation is still sealed at build time; HTML never receives a
generic bridge or runtime service locator.

## Runtime and package versions

Runtime and package versions have different jobs:

- `0.8.0` identifies the compatible KitJS runtime release.
- The full lowercase SHA-256 in the canonical filename identifies the exact
  artifact bytes.
- `data-kit-component` contains only the component name.
- Optional `data-kit-version` contains one exact SemVer for that host.

The canonical exact declaration is:

```html
<section
  data-kit-component="dialog"
  data-kit-version="1.0.0">
</section>

<script defer src="/kit.0.8.0.<sha256>.js"></script>
```

Inline HTML versions are forbidden. This is invalid and fails closed:

```html
<section data-kit-component="dialog@1.0.0"></section>
```

Go/build resolves package dependencies and embeds private exact-version
manifests for services and components in the artifact. The browser checks
`data-kit-version` against that manifest before it prepares the host. It never
uses the attribute to fetch a package, choose `latest`, or expose the manifest
through `window.kit`. A graph may contain only one exact version of each
component name.

`data-kit-version` is optional. An unversioned host uses the component already
registered by the closed artifact; it does not ask the browser to resolve a
default. Use an exact version when HTML and the artifact must perform an
explicit compatibility handshake.

## Authored directive surface

The base runtime has nine directive families. `key` is the identity companion
for `for`, not a separate family:

| Family | Syntax | Contract |
|---|---|---|
| component | `data-kit-component="counter" data-kit-version="1.0.0"` | creates one isolated instance, optionally verifies its exact packaged version; optional `data-kit-as="$name"` exposes an action-only handle |
| text | `data-kit-text="count"` | writes synchronous expression results through `textContent` |
| show | `data-kit-show="open"` | toggles the `hidden` property without removing the node |
| bind | `data-kit-bind="aria-expanded: open;"` | writes safe attributes and a small form-property allowlist |
| class | `data-kit-class="open ? 'block' : 'hidden'"` | owns dynamic class tokens while preserving authored static classes |
| model | `data-kit-model="name"` | two-way binds one existing writable component field to a supported form control |
| event | `data-kit-click="count = count + 1"` | runs an action through the generic delegated event pipeline |
| if | `<template data-kit-if="ready">` | owns one conditional clone of the template content |
| for + key | `<template data-kit-for="item, index of items" data-kit-key="item.id">` | reconciles keyed clone groups while preserving retained DOM identity |

`data-kit-class` accepts a complete class string or an object whose truthy keys
are complete class-token groups. Keep Tailwind candidates literal and complete;
do not assemble class names from fragments.

`data-kit-model` accepts one bare component field, not a nested path. It supports
text inputs, `textarea`, single and multiple `select`, checkbox booleans or
arrays, radio groups, and finite `number`/`range` values. Empty or invalid numeric
input becomes `null`, and text input respects IME composition.

The event family recognizes exactly these eleven event names:

```text
click dblclick submit input change keydown keyup
pointerdown pointerup focusin focusout
```

Event attributes may add `self`, `prevent`, `stop`, `once`, `outside`, `enter`,
`escape`, or `debounce(ms)`. A debounce delay must be an integer from 1 through
60,000 milliseconds. `outside` is valid only for `click`, `dblclick`,
`pointerdown`, `pointerup`, and `focusin`, and cannot be combined with `self`.
`enter` and `escape` are valid only on `keydown` or `keyup`, and cannot be used
together. Unknown directives, events, modifiers, duplicate modifiers, and
invalid combinations fail closed.

`data-kit-as` names one component for commands originating in another boundary:

```html
<section data-kit-component="editor">
  <button data-kit-click="$dialog.open(() => handleSuccess())">Publish</button>
</section>

<div
  data-kit-component="confirm-dialog"
  data-kit-as="$dialog"
  data-kit-show="visible"
  hidden>
  <button data-kit-click="confirm()">Confirm</button>
</div>
```

Aliases use the reserved `$name` namespace and are resolved from the connected
DOM only when an action runs. Missing, detached, or duplicate aliases fail
closed; no global alias registry owns a component. Aliases are intentionally
action-only: a binding such as `data-kit-text="$dialog.visible"` is rejected.
Top-level component fields, row locals, lambda parameters, and assignment
targets cannot use the reserved `$` prefix.
This keeps external calls command-oriented and avoids a cross-component
subscription graph. A callback lambda keeps its lexical source component, so a
dialog can confirm later and update the component that opened it. A component
that stores such a callback owns that reference and must clear it on confirm or
cancel; the dialog demo does both.

For example, a form remains ordinary HTML while its fields bind directly to a
small component:

```html
<form
  data-kit-component="profile-form"
  data-kit-submit:prevent="saved = true">
  <label>
    Name
    <input data-kit-model="name" autocomplete="name">
  </label>

  <label>
    Age
    <input type="number" data-kit-model="age">
  </label>

  <p
    data-kit-class="saved ? 'text-emerald-600' : 'text-slate-500'"
    data-kit-text="saved ? 'Saved ' + name : name"></p>

  <button type="submit">Save</button>
</form>

<script src="/kit.js"></script>
<script>
  kit.component("profile-form", {
    name: "Ada Lovelace",
    age: 36,
    saved: false
  });
</script>
```

`if` and `for` are template-only structural directives. Put the directive on a
real `<template>`; KitJS owns the materialized clone, not the template itself.
`for` accepts `item of items` or `item, index of items`. The optional
`data-kit-key` evaluates per row and must produce a unique string or finite
number; without it, the current index is the row identity. Row locals are
read-only, and nested structural templates are capped at 64 levels. Replace the
array shallowly after add, remove, or reorder operations so the component's
single dirty flag can schedule reconciliation.

The base intentionally has no local `scope`, page `$`, element refs, target
selector directive, Morph, Drive, application marker, hydration marker, or
ignored subtree contract. Navigation is enabled only by choosing
`hydrate.kit.js`; no authored marker silently changes the profile.

`$app` is not installed by either profile. If a site wants that name, it creates
an ordinary action-only component alias:

```html
<body data-kit-component="app" data-kit-as="$app">
```

That component may expose narrow commands to authored HTML. Trusted services
belong in component JavaScript rather than becoming a global reactive `$app`
service facade.

## Source layout

`kit.js` is generated. The readable source of truth is split by responsibility:

| File | Owns |
|---|---|
| `src/core.js` | single-install guard, private assembly capsule, security primitives, scheduler |
| `src/lexer.js` | source text to closed-language tokens |
| `src/parser.js` | tokens to the private bounded syntax tree |
| `src/evaluator.js` | safe scope/member/call semantics and the 256-entry compile cache |
| `src/component.js` | definition registry, per-host state, dirty-boundary ownership, action-only aliases, one-time `init()` and owned cleanup |
| `src/directives.js` | the reserved directive surface, exact event grammar, and private render hooks |
| `src/dom.js` | node-owned compiled records plus `text`, `show`, and `bind` rendering |
| `src/structure.js` | template-only `if` ownership and keyed `for` reconciliation |
| `src/class.js` | dynamic class ownership while preserving authored static classes |
| `src/model.js` | form-control coercion, IME handling, and two-way field synchronization |
| `src/events.js` | generic delegated dispatch, modifiers, `$event` snapshots, and model events |
| `src/service.js` | optional private service registrar, namespace snapshotting, and final sealing |
| `src/morph.js` | Hydrate-only private DOM reconciliation and boundary lifecycle bridge |
| `src/drive.js` | Hydrate-only same-origin navigation, measured lifecycle signal, compatibility, history, and document continuity |
| `src/boot.js` | final validation, auto-boot, and the only `globalThis.kit` publication |

Every fragment is an ordinary classic browser script. Loading them in the
listed order has the same behavior as loading the concatenated artifact. The
Go assembler performs no transformation; it joins exact fragment bytes through
two explicit deterministic manifests. `morph.js` and `drive.js` occur only in
the Hydrate manifest, immediately before `boot.js`:

```powershell
go run ./jit/javascript/vanilla/cmd/assemble ./jit/javascript/vanilla/kit.js
go run ./jit/javascript/vanilla/cmd/assemble -profile hydrate -output ./jit/javascript/vanilla/hydrate.kit.js
```

The first command is retained as the backwards-compatible shorthand for
`-profile kit`. Those commands write the two development aliases without a
package manifest. For an immutable production artifact, pass exact service and
component pins with their classic-script packages:

```powershell
go run ./jit/javascript/vanilla/cmd/assemble `
  -profile kit `
  -service storage=1.0.0=./jit/javascript/vanilla/service/storage/1.0.0.js `
  -component preferences=1.0.0 `
  -component-require preferences=storage=1.0.0 `
  -script preferences=./jit/javascript/vanilla/examples/preferences/preferences.js `
  -canonical-dir ./dist
```

The assembler writes `kit.0.8.0.<sha256>.js` without replacing an existing
different file at that path. Use `-profile hydrate` to produce
`hydrate.kit.0.8.0.<sha256>.js`. Repeat
`-service-require owner=dependency=version` for exact service dependencies.
Repeat `-component-require owner=service=version` for exact component-to-service
dependencies. Both kinds of edge participate in the immutable graph identity.
Package and CLI identifiers may use their own notation; the HTML contract
remains the two separate component attributes above. Services have no HTML
version attribute.

This small assembler is the future Kitwork integration seam: CDN and Go must
ship these same bytes, not two implementations of KitJS.

## Closed expression language

“Full expression support” means the full **closed KitJS language**, not all of
ECMAScript. Precedence from low to high is:

| Layer | Syntax |
|---|---|
| action program | `a; b; c;` |
| assignment | `name = value` (actions only, existing field only) |
| conditional | `condition ? yes : no` |
| nullish | `left ?? right` |
| logical | `||`, `&&` |
| equality | `==`, `!=`, `===`, `!==` |
| relational | `<`, `<=`, `>`, `>=` |
| arithmetic | `+`, `-`, `*`, `/`, `%` |
| unary | `!`, `-`, `+` |
| postfix | `.name`, `?.name`, `[key]`, `(args)` |

Primary values are finite decimal numbers (including exponents), quoted
strings with escapes, booleans, `null`, identifiers, arrays, null-prototype
objects, parenthesized expressions, and expression lambdas such as
`(item) => item.name`.

Calls support component methods, own object methods, the non-mutating array
methods `join`, `includes`, `indexOf`, `slice`, `map`, `filter`, `find`, `some`,
`every`, the string methods `includes`, `startsWith`, `endsWith`, `trim`,
`toLowerCase`, `toUpperCase`, and number `toFixed`. Receiver identity is
preserved.

Bindings are read-only. Actions can assign a bare existing writable data field and
can sequence expressions with semicolons. Member assignment, page scope `$`,
implicit field creation, comma expressions, declarations, statements, loops,
`new`, `typeof`, templates, update/compound/bitwise operators, optional calls,
and optional computed access are rejected.

Authored shallow assignments are staged and commit only after the complete
synchronous action succeeds. A parse, lookup, member, or budget failure discards
them. Trusted JavaScript inside a component method (including an accessor
setter) may still perform external or deep-object side effects; those are outside the expression
transaction.

The parser and walker both block prototype/global escape names. Object literals
use `Object.create(null)`, computed keys accept only strings or finite numbers,
evaluation is limited to 10,000 node visits and 64 nested calls, and no parser
or evaluator is exposed publicly.

## Ownership and rendering

The connected DOM is the live binding registry. Expressions compile once into
element-owned `WeakMap` records. Each component owns one shallow dirty bit:
changing its state queues that boundary once for the next microtask, then KitJS
queries and renders only elements whose nearest component host is that owner.
Parent, child, sibling, and unrelated components are not evaluated. This is a
boundary scheduler, not dependency tracking: there are no effects, signals,
watchers, or property-read subscriptions.

Structural records own only the nodes cloned
from their template: `if` disposes its branch when false, and `for` reuses,
moves, or disposes whole clone groups by key. Retained keys retain their real
DOM nodes and DOM-local state; moving them does not dispose them. Removed
branches dispose private descendants deepest-first. When retained row locals
change, ownership propagation marks only nested component boundaries in that
row; it still does not build a read graph. One microtask batches repeated writes
to the same owner, and DOM writes are diffed against their last value. There is
no virtual DOM.

`data-kit-bind` writes attributes plus a small form-state property allowlist. It
rejects event handlers, `data-kit-*`, style, `srcdoc`, `innerHTML`, `outerHTML`,
text-replacement sinks, and unsafe URL schemes. HTML content belongs to authored
markup or `data-kit-text`, never to an expression sink.

Events use one document listener per supported event type, plus composition
start/end listeners for IME-safe models. Outside handlers query their current
candidates at event time. Every top-level Promise returned by an action is
observed against the boundary that produced it; repeated references to the same
Promise are grouped across their owners. Pending observations retain only
primitive tokens and resolve owners from the connected DOM, never from a global
component registry.

No page-lifetime collection owns an element or scope. Components without owned
cleanup remain DOM/WeakMap-owned and need no observer. When a synchronous
`init()` result is a cleanup function, the runtime enables one private, lazy,
move-aware removal observer for the cleanup owners only. Structural removal,
Morph replacement, and direct DOM removal invoke cleanup exactly once; moving a
host within the same document does not. The observer disconnects when the last
cleanup owner is released. Cleanup errors are reported while disposal continues.

Explicit application references, such as a callback stored by another
component, remain the application's responsibility and must be cleared when
finished. A pending debounce retains only its compiled event state until the
bounded timer settles; it does not retain the element or component scope. The
compile cache is capped at 256 sources. Loading the same runtime twice is a
no-op; a conflicting runtime fails before listeners are installed.

Directive source and component identity are immutable after an element is first
prepared. Removing a value/event attribute disables it; restoring it reuses the
original compiled program. Structural `if`, `for`, and `key` attributes must not
be changed or removed after preparation. This prevents DOM mutation from
becoming a second compiler channel.

The base assumes directive-bearing templates are authored before boot. Direct
events inside structural clones are resolved lazily, but inserting the page's
first `outside` handler after boot is not part of the base contract.

`init()` is for one-time state/data initialization and may return one
**synchronous cleanup function**. That cleanup may release listeners, timers,
observers, streams, or subscriptions owned by the component. A
`Promise<cleanup>` is intentionally unsupported: asynchronous `init()` is
observed for state settlement, but durable resources must still be acquired
synchronously with a synchronous disposer. None of this expands the public
component API with mount, unmount, or destroy methods.

## Demos and gates

- `examples/counter.html`: state, actions, batching, and text.
- `examples/dropdown.html`: `show`, `bind`, outside-click, and Escape with a
  component definition containing only `{ open: false }`.
- `examples/form.html`: `model` coercion, IME-safe text input, dynamic `class`,
  ordinary component methods, and invalid-model fail-closed behavior.
- `examples/list.html`: template-only `if`, keyed `for`, add/remove/reorder by
  shallow array replacement, and retained DOM-local row state.
- `examples/dialog.html`: an external `$dialog` command, an origin-preserving
  callback, and two independently dirty component boundaries.
- `examples/preferences/index.html`: one sealed Kit artifact containing
  `storage@1.0.0` and `preferences@1.0.0`; the component reads in `init()` and
  persists only from trusted actions while HTML receives no service facade.
- `examples/hydrate-home.html` and `examples/hydrate-next.html`: the same
  component graph loaded directly or continued through Hydrate navigation,
  with retained counter state, dirty form value, focus, and node identity.
- `examples/drive-progress/index.html` and `next.html`: a retained
  `progress-bar@2.0.0` subscribed to `progress@1.0.0`. The service adapts the
  real `kit:navigation` lifecycle; the component owns only presentation,
  unsubscribe, and its hide timer. The demo covers indeterminate loading,
  trustworthy streamed byte progress, cancellation, recovery, direct route
  loads, and cleanup.
- `examples/request-form/index.html` and `design.html`: a model-driven form
  component calling `request@1.0.0`, which depends on `progress@1.0.0`; the
  retained `progress-bar@2.0.0` observes the same stream across Hydrate
  navigation. The demo covers JSON submit, same-key latest-wins, explicit
  abort, HTTP error normalization, direct loads, and component-owned
  cleanup without exposing a network primitive to authored HTML.
- `examples/shop/products.html`: a three-route Hydrate shop that keeps one cart
  boundary alive across Products, Cart, and Checkout; it also exercises keyed
  rows, modeled form fields, an external custom dialog command, lexical
  confirmation callback, direct route loads, and repeated history navigation.
  Serve `examples/shop/` over same-origin HTTP as described in its README;
  `file://` intentionally falls back to normal page loads. Its checked
  `hydrate.kit.0.5.0.*.js` is deliberately retained as a runnable historical
  artifact after newer runtime releases.

Browser tests exercise the authored HTML surface, the full grammar and security
rejection corpus, duplicate loading, live event candidates, compile-once
behavior, cleanup ownership, Hydrate navigation/fallback/progress, and
forced-GC release of detached boundaries. The tests never depend on a public
compiler or diagnostics API.
