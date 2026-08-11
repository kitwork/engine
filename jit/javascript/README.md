# KitJS

KitJS is a small browser runtime for making server-rendered HTML interactive.

The public model is deliberately narrow:

```text
declare a component
→ call its methods from data-kit-* events
→ mutate component state
→ update the bound DOM
```

KitJS targets browsers only. Go is the scanner, dependency resolver, composer,
and release tool. The JavaScript source does not require Node, npm, CommonJS,
ES modules, a browser package manager, or runtime code loading.

## Status

The current release gate is intentionally small: the counter below must work
through both standalone JavaScript and Kitwork with the same component source,
HTML semantics, and generated runtime. Morph/Drive, sealed services, and the
wider component catalog remain optional graph inputs; none enter the counter
artifact. The sealed catalog gates ten exact `1.0.0` service packages without
expanding the base artifact: only packages selected by a component graph enter
its bytes or public surface. The independent browser runtime release is
`0.8.0`; this is not a stable 1.0 release yet.

## Documentation

- [docs/DIRECTIVES.md](docs/DIRECTIVES.md) — Full specification and practical examples for all 13 core `data-kit-*` directives.
- [docs/SERVICES.md](docs/SERVICES.md) — Sealed service catalog and package contract (`kit.storage`, `kit.request`, etc.).
- [docs/EXAMPLES.md](docs/EXAMPLES.md) — Production-grade markup and JavaScript examples for all 16 canonical headless components.
- [docs/SPEC.md](docs/SPEC.md) — Normative architecture and implementation baseline for KitJS Clean 1.0 Standard.





Kitwork production integration is an explicit per-site preview. A site opts in
from its root router:

```js
router.kitjs(true);
```

The default is `false`; tenants that do not opt in retain the legacy
`jit/js + jit/hydrate` render and `/kit.js` behavior byte-for-byte. Opt-in is
snapshotted with the pending generation and cannot be changed after activation.

Only modules listed by the Go `DefaultRegistry` are canonical build inputs.
Files that still exist outside that registry are examples or migration input,
not code silently included in a release.

## Authoring surface

Define state in ordinary browser JavaScript:

```js
kit.component("counter", {
  count: 0
});
```

Bind that state directly from HTML:

```html
<section data-kit-component="counter">
  <button type="button" data-kit-click="count = count - 1">−</button>
  <output data-kit-text="count">0</output>
  <button type="button" data-kit-click="count = count + 1">+</button>
</section>
```

That is the complete component. The generated script starts automatically; the
page does not call a boot, mount, or teardown function.

## Two profiles, one runtime release

KitJS `0.8.0` has two profiles. Their unversioned filenames are development
aliases; production uses a release plus the full lowercase SHA-256 of the exact
bytes:

| Profile | Development alias | Canonical immutable artifact |
|---|---|---|
| Kit | `kit.js` | `kit.0.8.0.<sha256>.js` |
| Hydrate | `hydrate.kit.js` | `hydrate.kit.0.8.0.<sha256>.js` |

Choose one profile. Hydrate contains the Kit profile plus private Morph and
Drive continuity; it is not a separately versioned framework.

From `engine/`, the Vanilla assembler can create a closed counter graph:

```text
go run ./jit/javascript/vanilla/cmd/assemble -profile kit \
  -component counter=1.0.0 \
  -script counter=./jit/javascript/component/counter/1.0.0.js \
  -canonical-dir <outdir>
```

It writes `kit.0.8.0.<sha256>.js`. Use `-profile hydrate` for
`hydrate.kit.0.8.0.<sha256>.js`. An existing canonical path is never replaced
with different bytes, and old artifacts remain available for old pages, open
tabs, browser caches, and rollback.

The first sealed service example adds storage as an exact package input:

```text
go run ./jit/javascript/vanilla/cmd/assemble -profile kit \
  -service storage=1.0.0=./jit/javascript/vanilla/service/storage/1.0.0.js \
  -component preferences=1.0.0 \
  -component-require preferences=storage=1.0.0 \
  -script preferences=./jit/javascript/vanilla/examples/preferences/preferences.js \
  -canonical-dir <outdir>
```

The component source receives `kit.storage` lexically from that sealed graph;
the HTML declares only `preferences` and its component version.

The existing full-registry exporter remains a separate supported build entry:

```text
go run ./cmd/kitjs-dist 0.5.0 <outdir> --component counter@1.0.0
```

`counter@1.0.0` above is a package selector accepted by that CLI. The `@` form
is never valid inside `data-kit-component`.

Kitwork is the server adapter. After `router.kitjs(true)`, authors write the
same component HTML and no script tag; Go scans the prepared document, resolves
the graph, and injects the immutable artifact.

The canonical HTML keeps name and version separate:

```html
<section
  data-kit-component="counter"
  data-kit-version="1.0.0">
</section>
```

`data-kit-version` is optional. When present, it must be exact SemVer without a
leading `v`, range, or whitespace. Go/build resolves the requested package and
its dependencies. The browser only verifies the declaration against the
artifact's private manifest; it never fetches a component or chooses `latest`.
Inline HTML such as `data-kit-component="counter@1.0.0"` is rejected. One closed
graph may contain only one exact version for each component name.


Most components remain plain state plus a few methods. `init()` is optional and
reserved for work that genuinely begins after the host exists, such as reading
`kit.storage`; it may return cleanup for the private runtime to own. Authors
never add a matching public mount/unmount pair.

Trusted component JavaScript may use `kit.*` services and its raw
`this.$host`/`this.$refs` handles. Authored HTML expressions only see component
state and methods, curated read-only event/element snapshots, and explicit
aliases; they never receive `kit`, `window`, raw DOM nodes, or
native `Event` objects.

## Activation and navigation profile

Ordinary components and directives require no root marker. In Kitwork, the Go
scanner emits KitJS when the final HTML contains executable `data-kit-*`
behavior. In standalone/CDN use, including the generated script is the opt-in;
boot starts on `document.documentElement` automatically.

Choose navigation by loading the Hydrate profile:

```html
<script defer src="/assets/hydrate.kit.0.8.0.<sha256>.js"></script>
```

Hydrate uses `body` as its document replacement root and fingerprints the exact
resolved external script URL together with its closed graph. Incoming HTML may
morph only when that artifact is compatible; a missing or different artifact
falls back to normal browser navigation before mutation. Runtime `0.8.0` does
not require `data-kit-app`, `data-kit-hydrate`, a plan attribute, or a
navigation-root marker. Use `data-kit-drive="false"` on a link, form, or ancestor
when a navigation must remain browser-native.

Hydrate emits one non-cancelable `kit:navigation` event on `document` for each
Drive visit. The frozen detail has `start`, optional measured `progress`, and
exactly one `finish` for the same visit ID; finish outcomes are `loaded`,
`cancelled`, `error`, or `fallback`. Byte progress is available only when an
identity-encoded response provides a trustworthy `Content-Length` and readable
stream. It measures the fetched HTML response only, not later image, font, or
stylesheet loading. Unknown or encoded lengths remain indeterminate, and 100%
is reserved for a successful Morph commit.

## Public JavaScript

The core exposes one browser global:

```text
globalThis.kit
```

The normal authoring surface is intentionally small:

```js
kit.version
kit.component(name, plainObject)
```

`core/boot.js` starts the runtime automatically after selected services and
components are loaded. Start, subtree disposal, full teardown, MutationObserver
ownership, and Drive hooks remain inside the temporary private core capsule;
boot removes that capsule from `kit`. There are no public `kit.start()`,
`kit.destroy()`, `kit.use()`, `mount()`, or `unmount()` controls.

The base artifact has exactly `version,component`. A sealed service artifact
adds only its selected frozen namespaces. `storage@1.0.0` adds `kit.storage`;
`progress@1.0.0` adds `kit.progress` with `snapshot`, `subscribe`, `start`,
`update`, and `finish`. Exact service versions are non-enumerable. The temporary
`kit.service()` registrar is available only while trusted service packages
assemble, then is removed before component packages run and before
`globalThis.kit` is published.

Go resolves exact service versions and dependencies, topologically orders the
packages, and seals their source into the same immutable artifact. The private,
frozen graph records service and component manifests. The browser never fetches
or resolves a service package. Authored HTML can call a component method that
uses lexical `kit.storage`, but cannot access `kit` itself.

Component JavaScript always keeps the registration-only, two-argument
`kit.component(name, plainObject)` form. It has no one-argument registry getter.
Exact versions belong to Go/build metadata and, when authored explicitly, the
separate `data-kit-version` attribute; they do not complicate component source.

## Canonical services and components

Services are small browser primitives available only to trusted JavaScript.
They do not own visible presentation or component hosts.

Vanilla `0.8.0` release-gates the complete catalog below as sealed services.
This describes available packages, not a default payload: no entry is included
unless the exact build graph selects it.

| Service | Public surface | Purpose | Exact dependency |
|---|---|---|---|
| `kit.announce` | `say`, `polite`, `assertive`, `clear` | Accessible live-region announcements | none |
| `kit.clipboard` | `writeText`, `readText` | Native clipboard text access with explicit browser errors | none |
| `kit.cookie` | `get`, `set`, `remove`, `has` | Cookie primitives | none |
| `kit.fullscreen` | `request`, `exit`, `active` | Fullscreen browser capability | none |
| `kit.navigation` | `back`, `forward`, `reload` | History and reload primitives | none |
| `kit.network` | `online`, `snapshot`, `subscribe` | Connectivity state and owned subscriptions | none |
| `kit.progress` | `snapshot`, `subscribe`, `start`, `update`, `finish` | Shared operation progress and Hydrate navigation adaptation | none |
| `kit.request` | `send`, `get`, `post`, `abort` | Same-origin bounded requests with explicit cancellation | `progress@1.0.0` |
| `kit.share` | `open`, `canShare` | Web Share with clipboard fallback | `clipboard@1.0.0` |
| `kit.storage` | `get`, `set`, `remove`, `has`, `clear` | Local persistence | none |

The canonical component catalog currently contains:

| Component | Small public surface | Exact service dependency |
|---|---|---|
| [`accordion`](component/accordion/example.html) | `activeItem`, `toggle`, `open`, `close`, `isOpen` | none |
| [`announce`](component/announce/example.html) | `message`, `mode`, `speak`, `clear` | `announce` |
| [`clipboard`](component/clipboard/example.html) | `copied`, `error`, `copy`, `reset` | `clipboard` |
| [`combobox`](component/combobox/example.html) | query/value state, search, choose, clear | none |
| [`command-palette`](component/command-palette/example.html) | open/query state, show, close, search, run | none |
| [`counter`](component/counter/example.html) | `count` | none |
| [`dialog`](component/dialog/example.html) | `open`, `title`, `message`, `show`, `close`, `toggle` | none |
| [`drawer`](component/drawer/example.html) | `open`, `title`, `show`, `close`, `toggle` | none |
| [`dropdown`](component/dropdown/example.html) | `open` | none |
| [`menu`](component/menu/example.html) | open/active/selected state, show, close, select | none |
| [`popover`](component/popover/example.html) | `open` | none |
| [`progress-bar`](component/progress-bar/example.html) | value/status state, start, set, inc, done, reset | `progress` |
| [`tabs`](component/tabs/example.html) | `activeTab`, `select`, `is` | none |
| [`theme`](component/theme/example.html) | `mode`, `resolved`, `init`, `set`, `toggle` | `storage` |
| [`toast`](component/toast/example.html) | visible/message/tone state, show, close | `announce` |
| [`tooltip`](component/tooltip/example.html) | `visible`, `show`, `hide`, `toggle` | none |

Overlay and disclosure components are deliberately custom. Their examples use
ordinary server-rendered `div`, `section`, and `aside` elements with ARIA roles;
they do not depend on native `<dialog>`, `<details>`, or popover behavior.
Shared DOM behavior belongs to core directives, not repeated component code.
For example, `dropdown` owns only `open`; its HTML uses
`data-kit-click="open = !open"`, `data-kit-click:outside="open = false"`, and
`data-kit-keydown:escape="open = false"`. Its disclosure button is simply
`data-kit-ref="trigger"`; after a successful Escape action the delegated core
returns focus to that trusted ref. Modal focus containment, timers, and
capability subscriptions remain private only where the behavior cannot be
expressed safely by existing directives. `component:tab` remains a standalone
compatibility source, while new Go-composed pages resolve the canonical
`component:tabs`.

Progress deliberately has two layers. `progress@1.0.0` is a reusable operation
state service: it normalizes manual, bridge, and Hydrate navigation producers
into frozen snapshots. `progress-bar@2.0.0` is intentionally small reactive
presentation: it subscribes, renders only visibility and percentage, and
returns one synchronous cleanup for its subscription and hide timer. Graphs
that do not select either package still pay for neither. Theme remains a
component because its mode and resolved appearance are UI state rather than a
reusable operation primitive.

There is no fake `kit.window` service. Trusted component code uses browser APIs
directly when it owns the behavior, or a narrowly named capability such as
`kit.fullscreen` or `kit.navigation` when the primitive is reusable.

## Source layers

```text
core/
  global.js       one `kit` global and the temporary assembly capsule
  expression.js   closed parser and bounded evaluator
  component.js    component instances, scopes, aliases, and refs
  dom.js          compiled bindings, directives, and scheduler
  lifecycle.js    events, init cleanup, disposal, start, and destroy
  morph.js        optional lifecycle-aware DOM reconciliation
  drive.js        optional same-origin application navigation
  boot.js         closes the capsule and starts the document

service/<name>/<version>.js
  Small reusable browser capabilities. A service does not represent reactive
  UI state and never owns a component host.

component/<name>/<version>.js
  Plain reactive state, optional `init()`, computed getters, and methods.
  Markup and styling stay in server HTML and Tailwind utility classes.
```

Dependency direction is one-way:

```text
component -> service -> core
```

Core never depends on a named service or component. Services never depend on
components. A component may depend on services or other components declared in
the Go registry.

The sealed Vanilla implementations described here live under
`vanilla/service/<name>/1.0.0.js`. The sibling compatibility-registry service
tree remains migration input for the existing exporter and is not silently
substituted into a Vanilla artifact.

## Delegated event modifiers

Event attributes use colon-separated modifiers:

```html
<button data-kit-click:prevent:once="save()">Save</button>
<section data-kit-click:outside="open = false"></section>
<section data-kit-keydown:escape:stop="open = false"></section>
```

The core modifiers are `self`, `enter`, `escape`, `prevent`, `stop`, `once`,
`outside`, `debounce`, and `debounce(ms)`. Modifiers are part of the delegated
event contract; component files do not add equivalent document listeners.

The modifier set is exact. During Kitwork generation, an unsupported modifier,
an unknown reserved `data-kit-*` attribute, or any author-written
`data-kitwork-*` attribute fails composition instead of degrading to another
behavior. When the core files are loaded as separate classic scripts, the same
mistake emits `KIT_UNSUPPORTED_ATTRIBUTE` through `kit:error` and the invalid
action remains inactive. A typo such as `data-kit-click:typo` never becomes a
plain click handler.

After a `keydown:escape` action program evaluates without a synchronous error,
core schedules focus to the nearest owning component's trusted
`data-kit-ref="trigger"`. The ref must still be connected, enabled, visible,
and keyboard-focusable. A missing or unusable ref is a no-op. Lookup stops at
the nearest component boundary, so a nested disclosure never falls back to an
outer component's trigger. Raw ref elements are used only by trusted core code
and are not exposed to authored expressions.

## Classic-script module contract

Every service and component source file:

1. Is complete classic browser JavaScript; it uses an IIFE only when private helpers need a scope.
2. Uses no `import`, `export`, `require`, `eval`, or `new Function`.
3. Creates no replacement `kit` namespace when core is missing.
4. Registers its definition exactly once without probing or deduplicating the registry.
5. Assumes core and exact dependencies were ordered by Go, or loaded in the documented order for separate-script development.
6. Keeps top-level names out of the global scope.
7. Can be loaded through an ordinary `<script>` tag.
8. Can be concatenated by Go without transforming its source.

Load order, dependencies, versions, and deduplication are graph concerns owned
by Go. A duplicate browser registration is rejected centrally by
`kit.component()`; individual component files never implement a second loader.

For direct development, load dependencies in order:

```html
<script src="/core/global.js"></script>
<script src="/core/expression.js"></script>
<script src="/core/component.js"></script>
<script src="/core/dom.js"></script>
<script src="/core/lifecycle.js"></script>
<script src="/service/storage/1.0.0.js"></script>
<script src="/component/theme/1.0.0.js"></script>
<script src="/core/boot.js"></script>
```

Go emits the same files in this order:

```text
core/global.js
-> core/expression.js
-> core/component.js
-> core/dom.js
-> core/lifecycle.js
-> optional core/morph.js + core/drive.js
-> resolved services
-> resolved components
-> core/boot.js
```

The composer inserts only `;\n` between modules. Running the separate scripts
and running their composed output must have the same behavior.

Every component name in the catalog table links to a runnable `example.html`.
The examples contain only ordinary relative `<script>` tags and literal
Tailwind utility classes. No Node build, JavaScript import graph, Tailwind CDN,
or runtime module loader is involved.

## Runtime invariants

- Component definitions are cloned per host while preserving getters and
  methods.
- All component hosts and aliases are discovered before bindings are compiled.
- Bindings are compiled once into live records; a render flush does not rescan
  the document.
- State writes are batched into one microtask and DOM writes are diffed.
- `init()` is optional and runs once per component instance after aliases and
  refs exist. It may return one synchronous cleanup function. The runtime
  observes an async result for state settlement, but does not accept a
  `Promise<cleanup>`.
- Component state writes during binding evaluation are rejected; render-facing
  getters and methods must be free of side effects.
- Reactivity is shallow by design: replace a nested value or call
  `this.$invalidate()` after mutating it in place.
- A binding may read an alias outside the component host that owns it.
- Authored `$element`, `$host`, and `$refs` values are frozen facades rather
  than raw DOM nodes; native browser objects stay on the trusted component side.
- Curated element facades expose only `tagName`, `id`, `name`, `type`, `value`,
  `checked`, and `disabled`. Focus, measurement, and other DOM work belongs in a
  component method using trusted `this.$refs`.
- Runtime disposal invokes an `init()` cleanup exactly once when present, then
  releases aliases, refs, parent links, bindings, actions, and host ownership.
  Structural removal, Morph replacement, and direct DOM removal share this
  contract; same-document moves do not dispose. The direct-removal observer is
  private, lazy, and disconnected when no cleanup owner remains. Cleanup errors
  are reported while release continues. There is no authored
  `mount()`/`unmount()` pair.
- Every top-level action Promise is observed. Promise-valued render bindings
  are reported, rejection-safe, and never coerced into DOM values.
- Aliases are unique within one runtime root and are removed on disposal.
- Listeners, timers, observers, and asynchronous work must have explicit
  ownership and cleanup.
- Custom modal overlays (`dialog`, `drawer`, and `command-palette`) share one
  private core coordinator. Opening another overlay hands off ownership, closes
  the prior instance, preserves the original return-focus target, and keeps one
  body-scroll lock across the handoff. The final close or owner disposal
  restores the exact prior inline `overflow` value.
- `dropdown` and `popover` are generic disclosures. `menu` is the sole
  canonical ARIA menu-button component and owns its roving-focus/typeahead
  keyboard contract.
- Invalid expressions cannot execute dynamic code; runtime failures are
  reported and isolated to their binding, action, or component lifecycle.
- Only one exact version of a component or service name may exist in one
  composed generation.
- Every component source performs one registration. Duplicate runtime/module
  execution fails at the central `kit.component()` gate instead of silently
  keeping whichever definition ran first.
- The browser never resolves versions or fetches executable modules.
- The artifact profile selects navigation: `kit` has no Drive, while `hydrate`
  adds private Morph and Drive continuity.
- `data-kit-drive="false"` opts a link, form, or subtree out of Drive without
  disabling component behavior.
- `data-kit-app` and `data-kit-hydrate` are not part of the Vanilla `0.8.0`
  activation contract.

## Existing Kitwork adapter preview

The server-adapter mechanics below document the existing preview implementation
while it migrates to the Vanilla `0.8.0` profile and filename contract. Its
legacy `data-kit-app`, plan-marker, and `kitjs.*` details are not the activation
contract for `kit.js` or `hydrate.kit.js`; new standalone markup follows the
no-marker profile contract above.

### Go ownership

Go owns module metadata and source selection. A generation scan produces a
closed module graph, resolves exact versions, topologically orders dependencies,
composes one immutable script, and addresses it by content hash.

```text
prepared HTML
-> scan executable data-kit-* directives and component hosts
-> union every known route with the same positive data-kit-app identity
-> resolve the exact Go registry graph
-> include Morph/Drive only for a positive data-kit-app boundary
-> compose classic scripts
-> content hash
-> one kit.js asset
```

For an opted-in site, every known route is assembled and scanned while the
generation is prepared. Routes with the same exact, opaque `data-kit-app`
identity contribute to one deterministic union graph and receive the same plan
fingerprint. Conflicting exact versions abort preparation. Different app
identities remain separate, while documents without an app marker keep their
per-document only-used graph and do not select Drive. Composer or injection
errors abort preparation. The generation's bounded preparation store is frozen
before publication. Activation
copies its immutable artifacts into a second bounded CAS owned by the site
runtime. Published generations pin their hashes until retirement; unpinned
hashes remain for a short hand-off window because the browser requests the
external script after receiving HTML. The private preparation store still closes
when its generation drains, while the site CAS survives generation replacement
and serves only exact lowercase SHA-256 paths. Requests cannot create new graph
variants.

The preview intentionally requires static `data-kit-*` attribute values and
generation-prepared route pages. A request-time `ctx.view("override")` cannot
create a new KitJS asset and fails closed. Put such overrides into the prepared
route graph before enabling KitJS. This constraint avoids expanding Render's
legacy string-only error API during the preview cutover.

Standalone/CDN output is a predefined graph emitted by the same Go composer.
It is not a second runtime or build pipeline.

The browser therefore receives one already-closed program:

```text
ordered core fragments
-> optional Morph/Drive
-> only the resolved services
-> only the resolved components
-> boot
```

It never scans a package catalog, chooses `latest`, dynamically imports a
component, or performs a second dependency-resolution pass.

The presence of the emitted script is the activation contract. Kitwork inserts
it automatically when the final HTML uses KitJS. A standalone/CDN user opts in
by adding the generated script directly; neither mode requires a positive
`data-kit-hydrate` flag. A Kit-profile production response uses the canonical
immutable artifact name:

```html
<script
  data-kitwork-runtime
  src="/assets/kit.0.8.0.<sha256>.js"
  defer></script>
```

The Hydrate profile uses the same release and naming rule:

```text
/assets/hydrate.kit.0.8.0.<sha256>.js
Cache-Control: public, max-age=31536000, immutable
ETag: "<sha256>"
```

The SHA-256 is computed over the complete emitted bytes, including the private
service/component manifests and packaged sources. `kit.js` and
`hydrate.kit.js` remain development aliases; they are not canonical production
URLs. Published hashed artifacts are immutable and retained while any release
may reference them.

The existing registry exporter still selects a predefined graph through its
own supported CLI:

```text
go run ./cmd/kitjs-dist 0.5.0 <outdir>
go run ./cmd/kitjs-dist 0.5.0 <outdir> --drive \
  --component theme --component dialog
```

With no component arguments the standalone script contains core only. Repeated
`--component` flags select exact roots; their service/component dependency
closure comes exclusively from `DefaultRegistry`. The release argument must be
an exact SemVer 2.0.0 value (for example `1.0.0` or
`1.0.0-rc.1+build.7`); a leading `v`, ranges, whitespace, and partial versions
are rejected before the value can enter an artifact banner.

Every successful export writes five files:

```text
kitjs.js                              convenience readable alias
kitjs.min.js                          convenience minified alias
kitjs.<source-artifact-hash>.js       canonical readable artifact
kitjs.<min-artifact-hash>.min.js      canonical minified artifact
kitjs.snippet.html                    copy-ready canonical script tag
```

These `kitjs.*` names are exporter-specific compatibility outputs. Each
artifact hash is SHA-256 over that complete emitted file, including its banner,
and all hashes use the canonical 64 lowercase hexadecimal form. New Vanilla
profile delivery uses the `kit.0.8.0.<sha256>.js` or
`hydrate.kit.0.8.0.<sha256>.js` contract above. A basic registry export needs
only its canonical minified artifact because loading the script is the
activation opt-in:

```html
<script src="./kitjs.<min-artifact-hash>.min.js" defer></script>
```

Deploy the snippet beside the canonical file, or rewrite its public prefix once
before copying the same tag to every page. The two unversioned names are
deployment conveniences, not canonical URLs.

`--drive` adds Morph/Drive to the one predefined standalone graph. Unlike the
Kitwork adapter, standalone output cannot discover and union a site's routes:
the selected component roots must already cover every navigable document.
Every such document must use the same positive `data-kit-app` identity and
exactly one copy of the generated Drive runtime tag. Only this Drive-enabled
snippet carries `data-kitwork-runtime` and the unchanged `data-kitwork-plan`:

```html
<script
  data-kitwork-runtime
  data-kitwork-plan="<graph-hash>"
  src="./kitjs.<min-artifact-hash>.min.js"
  defer></script>
```

Missing, empty, duplicate, or different plan markers fail closed: Drive refuses
the morph and the browser performs a full navigation instead.

## Styling

Examples and components use server-authored HTML and complete Tailwind utility
class names. Component JavaScript supplies state and behavior; it does not ship
a second CSS component system.

## Verification

From `engine/`:

```text
go test ./jit/javascript
go test ./cmd/kitjs-dist ./render ./site ./work
go test ./jit/hydrate ./jit/js
go test ./...
```

The `theme` example covers an alias declared below a binding on `<html>`, small
component state, async `init()`, Promise settlement, persistence, and disposal.
The clipboard and progress examples cover service-backed components; progress
keeps shared operation state in `kit.progress` and presentation in its component.
The composed counter browser contract proves
that including a standalone artifact auto-starts initial and mutation-added
subtrees without `data-kit-app` or `data-kit-hydrate`. `test/components.html` mounts the complete
canonical catalog in a real browser and verifies custom overlay/disclosure
behavior, Escape return-focus boundaries, overlay scroll-lock handoff/cleanup,
keyboard interaction, state rendering, dependency-backed methods, and
disposal.

`test/browser/theme.html` runs the same source files as separate classic
scripts and verifies `init()`, curated authored-expression surfaces,
Tailwind's literal `.dark` selector, and remove/reinsert cleanup in a real
browser. `test/drive/contract.html` verifies real-assembly Morph/Drive
lifecycle, same-plan validation, keyed identity, form/focus preservation,
script blocking, JIT CSS replacement, cancellation, history, and teardown.
The Go browser test also executes a content-hashed composed bundle, so separate
scripts and Go concatenation are both release-gated. Exact-version selection
remains covered by the composer tests.
