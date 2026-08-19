# KitJS browser runtime

This directory is Kitwork's flattened consumer and Go-delivery copy of the
standalone KitJS browser library. The canonical package release tree is
`packages/kit.js`; this copy is kept byte-identical for Kitwork integration,
closed-graph assembly, and examples. The browser artifacts still have no Node
runtime, package loader, Go server, or SSR dependency. The same readable source
fragments produce two delivery profiles. The current locally checked source
candidate is `0.9.0-next.12`; this version string does not establish npm or CDN
availability:

| Profile | Development alias | Content-addressed candidate artifact | Contract |
|---|---|---|---|
| Kit | `kit.js` | `kit.0.9.0-next.12.<sha256>.js` | scope, component, expression, directive, event, and dirty-boundary runtime |
| Hydrate | `hydrate.kit.js` | `hydrate.kit.0.9.0-next.12.<sha256>.js` | the exact Kit profile plus private Morph and Drive continuity before boot |

Choose one artifact; never load both on the same page.

Standalone applications load one of those complete files themselves. The
generic standalone artifacts contain no application services or component manifest;
an application that adds trusted definitions loads them after its chosen
profile and preserves classic-script order.

```html
<section data-kit-scope="count: 0;">
  <button data-kit-click="count = count - 1">-</button>
  <output data-kit-text="count">0</output>
  <button data-kit-click="count = count + 1">+</button>
</section>

<script src="/kit.js"></script>
```

`data-kit-scope` is enough for data-only local state. No component registration
or application-root marker is required for this counter.

The examples may use the stable development aliases while source is changing:

```html
<script src="/kit.js"></script>
```

Choose the Hydrate profile when the site wants safe same-document navigation:

```html
<script src="/hydrate.kit.js"></script>
```

## Kitwork staged delivery

`router.jitjs(true)` is the shorthand that opts the prepared site generation
into the Kitwork-native adapter. Sites that declare tenant components use
`router.jitjs({ components: { ... } })` instead. Kitwork scans each prepared document, closes its exact Hydrate package
graph, and emits independently cacheable classic scripts in one valid `defer`
order:

```text
runtime
-> hydrate
-> graph opener
-> services in dependency order
-> optional common components bundle
-> individual page components
```

Every generated script carries an engine-owned role, the SHA-256 of its exact
bytes, a root-relative content-addressed URL, and matching Subresource
Integrity metadata:

```html
<script data-kitwork-jit="runtime" data-kitwork-hash="<runtime-sha256>"
  src="/jit/<runtime-sha256>.runtime.js" integrity="sha256-..."
  crossorigin="anonymous" defer></script>
<script data-kitwork-jit="hydrate" data-kitwork-hash="<hydrate-sha256>"
  src="/jit/<hydrate-sha256>.hydrate.js" integrity="sha256-..."
  crossorigin="anonymous" defer></script>
<script data-kitwork-jit="graph" data-kitwork-hash="<graph-sha256>"
  src="/jit/<graph-sha256>.graph.js" integrity="sha256-..."
  crossorigin="anonymous" defer></script>
<script data-kitwork-jit="service" data-kitwork-hash="<service-sha256>"
  src="/jit/<service-sha256>.progress.js" integrity="sha256-..."
  crossorigin="anonymous" defer></script>
<script data-kitwork-jit="components" data-kitwork-hash="<bundle-sha256>"
  src="/jit/<bundle-sha256>.components.js" integrity="sha256-..."
  crossorigin="anonymous" defer></script>
<script data-kitwork-jit="component" data-kitwork-hash="<component-sha256>"
  src="/jit/<component-sha256>.dialog.js" integrity="sha256-..."
  crossorigin="anonymous" defer></script>
```

Service and component filenames use the exact package name as their suffix;
the other suffixes are their role names. The graph script opens the private
manifest before any selected package registers. Publication and boot happen
only after every expected package has loaded and registered successfully.
Authored HTML cannot supply or override these tags. Staged delivery does not
emit or interpret `data-kitwork-plan`; `data-kitwork-hash` plus the sealed graph
and ordered asset metadata are the identity.

Injection preserves effective authored charset and meta
`Content-Security-Policy` declarations before the staged scripts, then places
the complete staged sequence before any active or potentially active `<base>`
that could change URL resolution. A head whose dynamic or authored order cannot
satisfy that security sequence fails generation preparation instead of moving
policy metadata or trusting a changed base.

The graph is exact per prepared document, not one union forced onto every
route. Runtime, Hydrate, service, and component bytes deduplicate naturally by
content hash. When at least two component name/version pairs occur in every
prepared document, Kitwork places that exact intersection in one stable
`components` chunk and omits those packages from every individual component
chunk, so a page never receives the same component twice. All remaining
components stay individually cacheable.

Drive accepts an identical ordered staged delivery directly. It may also hand
off to a different **component-only** graph when runtime, Hydrate, the complete
service set, authored service-action sets, and every service asset remain
exact. A component present in both graphs must keep the same name, exact
version, raw component-source hash, and component-to-service grants. Added and
removed component names are allowed.

For that compatible handoff, Drive opens the target graph metadata and loads
only immutable component chunks missing from its cache. The exact component
cache identity is name, version, and raw source hash, and the cache is bounded
at 256 identities. A cached package outside the active graph remains inert: it
has no active component-registry entry and no service authority. A common
`components` bundle may be reused whole or loaded whole; a target bundle that
partly overlaps cached packages is incompatible.

Component chunks expose registration-only installers. Drive stages every
required installer, validates the target document, then executes the
installers and switches the exact active registry, graph, and delivery in one
synchronous commit immediately before Morph. Package scope must not mutate the
DOM or other globals, add listeners, start timers, or perform network work;
those effects belong to mounted component lifecycle. A missing or failed later
chunk therefore cannot partially install an earlier one.

Any runtime, Hydrate, service, service-action, service-asset, overlapping
component version/source/grant, CSP, document-root, retain, URL/hash/SRI,
ordering, malformed-delivery, load, or active-visit cancellation mismatch
falls back to normal browser navigation before Morph. A superseded visit cannot
commit. The public `kit` object and document roots retain their identity, and
ordinary retained component/form state follows the existing Morph rules.

No script node discovered in fetched HTML is inserted or executed. During a
compatible handoff, Drive creates fresh temporary engine-owned same-origin
graph/component nodes from the sealed metadata, requires exact SRI, marks them
with `data-kitwork-handoff`, and removes them after registration. It never
loads a new runtime, Hydrate, or service script through this path. This split
is a Kitwork delivery adapter; the public `SourceForProfile`/`Build` contracts
and the package-tree `kit.js` and `hydrate.kit.js` one-file profiles remain
supported.

A tenant may add generation-managed components without extending the embedded
engine catalog. Declare this object form once, in the site-root
`router.kitwork.js`; its source paths are relative to that root router folder:

```js
router.jitjs({
  components: {
    "profile-form": {
      version: "1.0.0",
      source: "./components/profile-form.js"
    }
  }
});
```

Each map entry snapshots a confined site-relative file, canonicalizes its
classic-script bytes, and overlays one name with one exact version on that
generation's catalog. Only documents that use the name receive its immutable
component chunk. Tenant components cannot shadow embedded names, declare
services or grants, or publish multiple versions of one name in a generation.

The script profile selects the behavior. Neither profile requires `data-kit-app`,
`data-kit-hydrate`, a plan attribute, or a navigation-root marker. Hydrate uses
`body` as its document replacement root. Morph and Drive remain private
implementation details; they do not expand the public `kit` object.

For standalone delivery, Hydrate treats the exact resolved external script URL,
including its query, as the compatibility fingerprint. An incoming document
must carry the same Hydrate artifact URL before Drive may mutate the current
document. A missing or different artifact falls back to normal browser
navigation. Production standalone delivery uses a canonical immutable filename,
for example
`/hydrate.kit.0.9.0-next.12.<sha256>.js`. A changed runtime, service/component
pin, or package source produces a new SHA-256 and therefore a new URL.
Previously generated canonical files remain byte-for-byte unchanged so old
pages, open tabs, caches, and rollbacks can keep requesting them.

Drive also falls back before mutation when the incoming document changes its
explicit `<base>` semantics, changes the exact ordered effective meta
`Content-Security-Policy` set, declares an unregistered component, or contains
an active embedded document such as `iframe`, `object`, `embed`, `frame`, or a
meta refresh. Any `Content-Security-Policy` or
`Content-Security-Policy-Report-Only` response header on the fetched navigation
also forces normal browser navigation because Fetch cannot install that policy
on the current document.

Authored executable scripts have their own fail-closed compatibility fence.
Only an identical ordered set of persistent scripts is compatible: every item
must be a classic external direct child of `head`, use `defer`, and retain the
same resolved URL, ordered position, and complete attributes. Valid SRI proves
its identity. An ordinary authored same-origin script may instead use the exact
`data-kit-drive="stable"` marker to participate without SRI, in either
standalone or staged delivery. The same policy may identify a self-hosted
standalone Hydrate tag. A cross-origin script must omit the stable marker and
carry valid SRI; stable plus a cross-origin URL is incompatible even when SRI
is present.

The stable marker is an author promise, not a content check. Drive compares the
tag identity but does not fetch or hash that file; replacing bytes behind an
unchanged stable URL violates the contract.

The stable marker never authorizes inline/body/module/import-map/
speculation-rule scripts, `async`, `nomodule`, or an added, removed, reordered,
or changed script. It also never relaxes Kitwork's engine-owned staged assets:
runtime, Hydrate, graph, service, and component tags retain their sealed
URL/hash/SRI contract. Inert data scripts such as `application/json` are
ignored. Fetched scripts are never inserted or executed.

Hydrate validates the current document's executable topology before Drive
installs navigation listeners, changes scroll restoration, or emits lifecycle
events. An incompatible initial topology disables Drive for that document, so
links and forms remain native and no Drive fetch occurs. One console warning
identifies KitJS Drive as disabled. If a valid initial document later fetches
an incompatible destination, Drive performs normal browser navigation before
live mutation; the destination document loader then executes its scripts.

An explicit fragment link for the currently rendered path and query, including
`href="#"`, remains native browser navigation. Drive flushes the leaving scroll
position without preventing the click, so the browser still owns `hashchange`,
scrolling, and CSS `:target`. Back/Forward between those fragment entries
restores saved coordinates, or the fragment target when no coordinates exist,
without another fetch or Morph.

For a cross-route visit, Drive preserves the requested fragment across Fetch
and followed redirects even though `Response.url` omits a hash. It resolves the
exact raw identifier before the UTF-8 percent-decoded identifier; for each,
`id` precedes `<a name>`, while `name` on another element is not a target.
Lookup uses exact DOM identity rather than CSS selectors and does not normalize
Unicode, so `#á`, encoded UTF-8, and distinct NFC/NFD identifiers retain their
exact meaning. Invalid percent syntax stays literal. Ill-formed encoded UTF-8
uses replacement decoding without throwing. An empty fragment and unshadowed,
case-insensitive `top` target the document root.

Cross-route Drive navigation guarantees the committed URL, focus, and scroll
destination. It does not emulate native CSS `:target` activation for a history
entry committed with `pushState()`.

Hydrate navigation operates inside a closed component/script graph. Every
component definition needed by an incoming body must already be registered by
the initial document or its exact identity-bound persistent shared bundle. A
standalone site should therefore put shared definitions in that bundle.
Kitwork compares its staged delivery and may additionally perform the sealed
component-only handoff above. An initial standalone runtime/script mismatch
keeps Drive disabled. A standalone or Kitwork staged-delivery mismatch found in
a fetched destination must hard-navigate; it must never partially activate a
document with a different executable graph. No authored application marker is
required for this check.

Morph preserves dirty form state only for a control whose non-empty `id`, tag,
and input type remain compatible. Unidentified controls are replaced so data
cannot drift into an unrelated field on the next route. Select state is matched
by option value rather than its previous numeric index.

An application-owned component that must survive route layout changes can use
one explicit Morph identity:

```html
<section
  data-kit-retain="app-progress"
  data-kit-component="progress-bar@2.0.0">
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

Use the presence-only `data-kit-ignore` marker when another client runtime owns
an entire DOM region. KitJS leaves that host and subtree inert: it does not
prepare scopes, components, aliases, directives, events, models, or structural
templates there. When current and incoming hosts of the same element kind both
carry the marker, Morph preserves the current host, attributes, properties,
and descendants untouched. Adding or removing the marker replaces that
boundary; omitting the node from the incoming document still removes it.
`data-kit-ignore` is an ownership/Morph boundary, not a sanitizer or security
boundary. Scripts, resources, and other active content in the initially
authored DOM continue to follow ordinary browser semantics.

Drive emits one non-cancelable `kit:navigation` event on `document`. Its frozen
detail uses `start`, optional measured `progress`, and exactly one `finish` per
visit ID. Finish outcomes are `loaded`, `cancelled`, `error`, or `fallback`.
Byte progress is emitted only for a trustworthy identity-encoded
`Content-Length` response stream and is capped below 100 until Morph commits;
unknown or encoded response sizes remain indeterminate. This measures the HTML
navigation response, not later image, font, or stylesheet loading.

Drive deduplicates scroll-state writes and schedules at most one per 250 ms.
It synchronously flushes the latest exact coordinates before an outgoing visit
and on `pagehide`. When cross-route `popstate` has already activated a history
destination, Drive instead cancels the old rendered page's pending write so it
cannot overwrite that destination entry.

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

The complete KitJS 0.9.0-next.12 catalog currently contains eleven exact packages:

| Service | Public surface | Purpose |
|---|---|---|
| `announce@1.0.0` | `say`, `polite`, `assertive`, `clear` | Bounded ARIA live announcements |
| `appearance@1.0.0` | `mode`, `resolved`, `snapshot`, `subscribe`, `set`, `toggle`, `system` | Document light/dark/system ownership |
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

  async init(context) {
    this.mode = await kit.storage.get("theme", "system");
    context.afterRender(() => {
      // The boundary has committed its next complete render.
    });
  },

  async choose(mode) {
    await kit.storage.set("theme", mode);
    this.mode = mode;
  }
});
```

Authored expressions can call `choose('dark')`, but cannot read or call the
trusted `kit.storage` namespace directly. A closed artifact may separately
grant exact direct service commands through the canonical `$app` facade
described below. The browser never fetches a service package at runtime; its
source and exact version are already sealed into the artifact. The private,
frozen graph records services, components, dependency edges, component grants,
and action sets, so loading the same artifact is a no-op while a different
graph fails before any package source runs.

`progress@1.0.0` is intentionally a latest-visible operation stream, not an
aggregate task manager. `request@1.0.0` reports trustworthy streamed response
bytes through it. `network@1.0.0` reports only browser connectivity state;
online does not prove that an endpoint is reachable. `cookie@1.0.0` deliberately
cannot create `HttpOnly` authentication cookies. `kit.drive` is not a service:
Morph and Drive remain private parts of the Hydrate profile.

`appearance@1.0.0` is the single document-lifetime color-mode owner. It applies
the root `dark` class and `style.colorScheme`, persists the exact bare key
`theme`, follows the system preference while in system mode, and synchronizes
storage changes between tabs. Its getters and snapshots are read-only; its
subscription cleanup is idempotent. It owns no application markup and does not
mutate `meta[name=theme-color]`.

The versioned service API is also the portability boundary. A browser profile
may implement `request@1.0.0` with `fetch`, while a future desktop or mobile
profile may provide the same four methods through a native transport. The
selected implementation is still sealed at build time; HTML never receives a
generic bridge or runtime service locator.

## Runtime and package versions

Runtime and package versions have different jobs:

- `0.9.0-next.12` identifies the compatible KitJS runtime release.
- The full lowercase SHA-256 in the canonical filename identifies the exact
  artifact bytes.
- A managed `data-kit-component` contains `name@exact-semver`.
- An unversioned name is reserved for direct client registration.

The canonical exact declaration is:

```html
<section
  data-kit-component="dialog@1.0.0">
</section>

<script defer src="/kit.0.9.0-next.12.<sha256>.js"></script>
```

Version ranges, `latest`, and a `v` prefix are forbidden. These declarations
fail closed:

```html
<section data-kit-component="dialog@latest"></section>
<section data-kit-component="dialog@^1.0.0"></section>
<section data-kit-component="dialog@v1.0.0"></section>
```

Go/build resolves package dependencies and embeds private exact-version
manifests for services and components in the artifact. The browser checks the
exact suffix in `data-kit-component` against that manifest before it prepares
the host. It never uses authored HTML to fetch a package, choose `latest`, or expose the manifest
through `window.kit`. A graph may contain only one exact version of each
component name.

The split `data-kit-component="name" data-kit-version="1.2.3"` form remains a
deprecated compatibility input for one 0.9 release and is removed in 1.0. New
managed markup must embed the exact version in `data-kit-component`.

Trusted client code registers an otherwise unknown page component against an
ordinary unversioned host. It cannot use `data-kit-retain`, shadow an embedded
or tenant-managed component, or receive a graph service grant:

```html
<section data-kit-component="page-counter"></section>
<script>
  document.addEventListener("DOMContentLoaded", () => {
    kit.component("page-counter", { count: 0 });
  }, { once: true });
</script>
```

The host remains SSR-visible until registration and a missing definition is
reported after `DOMContentLoaded`. The inline script makes the initial
executable topology incompatible, so Drive stays disabled and later navigation
is native. To keep the direct component while preserving Drive eligibility,
move the same `kit.component("page-counter", ...)` registration into one shared
same-origin file loaded in the same direct-head position on every compatible
route:

```html
<script defer
  src="/assets/page-counter.js"
  data-kit-drive="stable"></script>
```

The external file must remain identical by resolved URL, order, and complete
attributes. A cross-origin file must omit the stable marker and use valid SRI.
Use `router.jitjs({ components: { ... } })` when Kitwork should manage the
content-addressed component graph and component-only handoff. The empty
`data-kit-local` marker remains a deprecated compatibility input for one 0.9
release and is removed in 1.0; do not author it in new markup.

## Embedded UI component catalog

The flattened Kitwork delivery catalog currently closes these browser
components. Component versions are independent from the `0.9.0-next.12` runtime
release:

| Component | Canonical exact version | State and purpose | Service dependency |
| --- | --- | --- | --- |
| `accordion` | `1.0.0` | single or multiple disclosure state | none |
| `app` | `1.1.0` | application identity, authored service-command facade, and loader view-model | `announce`, `appearance`, `clipboard`, `cookie`, `fullscreen`, `navigation`, `progress`, `share`, `storage` at `1.0.0` |
| `alert` | `1.0.0` | persistent message, tone, and dismissal | none |
| `carousel` | `1.0.0` | ordered, wrapping slide selection | none |
| `dialog` | `1.0.0` | small overlay-state controller; enhanced `2.0.0` is an exact-pin opt-in | none |
| `drawer` | `1.0.0` | edge-surface open and side state | none |
| `dropdown` | `1.0.0` | small disclosure/selection controller; enhanced `2.0.0` is an exact-pin opt-in | none |
| `pagination` | `1.0.0` | bounded one-based page operations | none |
| `popover` | `1.0.0` | disclosure state and placement preference | none |
| `progress-bar` | `2.0.0` | navigation/request progress presentation | `progress@1.0.0` |
| `shortcut` | `1.0.0` | exact `mod+k` activation for one clickable host | none |
| `switch` | `1.0.0` | guarded binary setting state | none |
| `tabs` | `1.0.0` | small ordered-selection controller; enhanced `2.0.0` is an exact-pin opt-in | none |
| `theme` | `3.0.0` | reactive adapter for light, dark, or system mode | `appearance@1.0.0` |
| `toast` | `1.0.0` | one explicit timer-free status message | none |
| `tooltip` | `1.0.0` | supplementary description visibility | none |

Except for `app`, `progress-bar`, `shortcut`, and `theme`, the listed canonical stateful packages
deliberately contain only data and methods. They install no DOM listeners,
retain no element references, and do not create portals, position surfaces,
move or trap focus, make a background inert, or schedule hidden timers. The
`app@1.1.0` keeps exact graph-granted services virtual while subscribing once
to `progress@1.0.0` and replacing its frozen `{ visible, value }` loader
snapshot. `progress-bar@2.0.0` remains the standalone presentation adapter for
an application without App. `theme@3.0.0` subscribes to `appearance@1.0.0`,
mirrors `mode` and `resolved`, and delegates its three commands; the appearance
service remains the one document-lifetime owner. The immutable empty
`app@1.0.0` component remains available only by an exact pin.
`shortcut@1.0.0` owns one lifecycle-bound document key listener, accepts only
the literal `data-shortcut="mod+k"` contract, and delegates activation to the
host's native `click()` behavior. It owns no navigation, focus target, state,
methods, service, or browser-global API.
Semantic HTML and authored directives own every other presentation policy.

Legacy `theme@2.0.0` remains available as an exact standalone pin for an
artifact that does not contain appearance. It is an independent document
owner, so the assembler rejects any graph containing both `theme@2.0.0` and
`appearance@1.0.0`. New graphs use the canonical `theme@3.0.0` adapter.

`dialog@2.0.0`, `dropdown@2.0.0`, and `tabs@2.0.0` remain available as enhanced
controllers for applications that explicitly want KitJS to own the complete
interaction policy. They are never selected as an upgrade merely because they
exist: use `data-kit-component="dialog@2.0.0"` (or the corresponding component
name) so the HTML visibly opts into their DOM
listeners, focus management, keyboard behavior, and lifecycle cleanup.
Applications choosing the smaller controller author its exact `1.0.0` spec.

The catalog is a build input, not a browser package registry. Naming a
component in HTML selects source while Go prepares the generation; it never
causes the browser to fetch that component. The standalone npm base profiles
also do not contain this closed catalog. A standalone application that uses
these packages must build and serve an artifact that has sealed the selected
component sources and exact manifest.

## Scope data boundaries

`data-kit-scope` accepts a semicolon-separated shorthand or one object:

```html
<section data-kit-scope="count: 3; open: true"></section>
<section data-kit-scope='{ count: 3, map: { "kebab-key": 1 } }'></section>
```

The final shorthand semicolon is optional. Top-level fields must be unique,
safe identifiers matching `[A-Za-z_][A-Za-z0-9_]*`; `$` fields are reserved.
Nested unquoted keys follow the same rule. Nested quoted keys may be arbitrary
JSON strings, including empty, `$`-prefixed, or hyphenated keys, except blocked
prototype names. Values are pure data only: `null`, booleans, finite signed
numbers, strings with valid UTF-16 pairing, arrays, and null-prototype objects.
Calls, identifiers as values, lambdas, and computed expressions fail closed.
Source is capped at 16,384 UTF-16 code units, 32 data levels, and 1,024 nodes.
Scope and component boundaries cannot use a `<template>` as their host; place
the boundary on an element inside the template content.

A scope-only host owns one anonymous shallow reactive store. Actions and models
may update its existing top-level fields, but it has no methods, `init()`,
cleanup, alias, component version, or retention key. On a host that also has
`data-kit-component`, the declaration seeds that component's same store before
`init()`. It may override only declared own writable non-function data fields;
unknown fields, methods, accessors, and `init` are rejected.

This source candidate intentionally has no `data-kit-props` or `$props`. A server may
serialize a scope as public initial state, then HTML-attribute-escape the whole
completed declaration for its quote context. It must never interpolate
untrusted source text or place secrets in HTML. The snapshot is neither a live
server binding nor a parent-to-child subscription. A retained component keeps
its live store and ignores an incoming seed; a changed seed on a non-retained
boundary causes a fresh mount.

## Authored directive surface

The base runtime has eleven directive families. `key` is the identity companion
for `for`, not a separate family:

| Family | Syntax | Contract |
|---|---|---|
| scope | `data-kit-scope="count: 0; open: true"` | creates one anonymous local-state boundary or seeds the component on the same host |
| component | `data-kit-component="counter@1.0.0"` | creates one isolated managed instance at an exact packaged version; optional `data-kit-as="$name"` exposes an action-only handle; unversioned names are for direct client registration |
| text | `data-kit-text="count"` | writes synchronous expression results through `textContent` |
| show | `data-kit-show="open"` | toggles the `hidden` property without removing the node |
| bind | `data-kit-bind="aria-expanded: open;"` | writes safe attributes and a small form-property allowlist |
| class | `data-kit-class="open ? 'block' : 'hidden'"` | owns dynamic class tokens while preserving authored static classes |
| style | `data-kit-style="width: progress + '%'; opacity: visible ? 1 : 0;"` | transactionally owns fixed CSS properties with continuous binding values |
| model | `data-kit-model="name"` | two-way binds one existing writable field on the nearest reactive boundary to a supported form control |
| event | `data-kit-click="count = count + 1"` | runs an action through the generic delegated event pipeline |
| if | `<template data-kit-if="ready">` | owns one conditional clone of the template content |
| for + key | `<template data-kit-for="item, index of items" data-kit-key="item.id">` | reconciles keyed clone groups while preserving retained DOM identity |

`data-kit-ignore` is a presence-only ownership marker rather than another
reactive family. Its host and complete subtree are opaque to KitJS preparation,
validation, rendering, event dispatch, alias lookup, and structural ownership.

`data-kit-class` accepts a complete class string or an object whose truthy keys
are complete class-token groups. Keep Tailwind candidates literal and complete;
do not assemble class names from fragments.

`data-kit-style` is the bounded escape hatch for percentages, coordinates,
transforms, and CSS custom properties. Its only grammar is a semicolon-separated
`property: expression` map; braces are invalid. It accepts longhand and custom
properties, rejecting CSS shorthands because they implicitly own multiple
longhands. It evaluates the whole map before writing, preserves unrelated
static styles, and restores a property's incoming authored baseline when its
value becomes nullish, `false`, or empty.
Unsafe property names, asynchronous/non-scalar values, CSS networking or
declaration-breaking strings, duplicate names, and oversized maps fail closed.
Generation preflight validates this source but does not evaluate browser scope;
authored inline styles remain the first-paint fallback until KitJS boots.

`data-kit-model` accepts one bare boundary field matching
`[A-Za-z_][A-Za-z0-9_]*`, not a `$`-prefixed name or nested path. It supports
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
Top-level boundary fields, row locals, lambda parameters, and assignment
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

The base intentionally has no named page scope, page `$`, target selector
directive, public Morph or Drive API, application marker, or hydration marker.
Navigation is enabled only by choosing `hydrate.kit.js`; no authored marker
silently changes the profile.

The generic Kit and Hydrate base files do not install `$app`, services, action
grants, or a component manifest. A sealed artifact may include the canonical
`app@1.1.0` package and mount it under the exact alias:

```html
<html
  data-kit-component="app@1.1.0"
  data-kit-as="$app">
  <body>
    <button data-kit-click="$app.appearance.toggle()">Toggle theme</button>
  </body>
</html>
```

This is a closed command facade, not global reactive application state. During
an authored action, `$app.<service>` resolves to the exact frozen
`kit.<service>` namespace only when the component identity, `$app` alias,
component-to-service grant, selected service version, and static method action
grant all match the graph. The namespace is not wrapped, copied, or assigned to
the app instance.

The canonical authored surface is:

```text
announce    say polite assertive clear
appearance  set toggle system
clipboard   writeText
cookie      set remove
fullscreen  request exit
navigation  back forward reload
progress    start update finish
share       open
storage     set remove
```

Only direct static calls shaped as `$app.service.method(...)` are valid.
Service bindings and reads, computed or optional service members, extracted
methods, assignment, passing the namespace/method as a value, chained calls,
and any other component identity or alias fail closed. Trusted component
JavaScript retains every member of each selected `kit.*` namespace, including reads,
snapshots, subscriptions, and asynchronous workflows. This keeps broad or
result-bearing authority in trusted code while allowing small HTML commands.
For progress specifically, `snapshot` and `subscribe` stay trusted-only.

App separately exposes the ordinary reactive presentation snapshot
`$app.loader`. Bindings may read exactly `$app.loader.visible` and
`$app.loader.value`; they cannot assign either field or read `$app.progress`.
The loader object is frozen and replaced atomically. Start maps to visible,
indeterminate state; measured progress is floored and capped at 99 while
active; a loaded finish shows 100 then hides after 300 ms; cancelled, error,
and fallback finishes hide immediately. The component cleanup clears its timer
and unsubscribes. An App-based shared shell can therefore render progress
without a retained `progress-bar` host:

```html
<div
  data-kit-show="$app.loader.visible"
  data-kit-style="width: $app.loader.value === null ? '18%' : $app.loader.value + '%';"
  hidden>
</div>
```

## Source layout

`kit.js` is generated. Kitwork's byte-identical consumer fragments are split by
responsibility:

| File | Owns |
|---|---|
| `src/core.js` | single-install guard, private assembly capsule, security primitives, scheduler |
| `src/lexer.js` | source text to closed-language tokens |
| `src/parser.js` | tokens to the private bounded syntax tree |
| `src/evaluator.js` | safe scope/member/call semantics and the 256-entry compile cache |
| `src/scope.js` | bounded pure-data parsing for anonymous scopes and component seeds |
| `src/component.js` | definition registry, per-host state, dirty-boundary ownership, action-only aliases, frozen `init(context)`, and owned lifecycle cleanup |
| `src/directives.js` | the reserved directive surface, exact event grammar, and private render hooks |
| `src/dom.js` | node-owned compiled records plus `text`, `show`, and `bind` rendering |
| `src/structure.js` | template-only `if` ownership and keyed `for` reconciliation |
| `src/class.js` | dynamic class ownership while preserving authored static classes |
| `src/style.js` | bounded per-property CSSOM ownership with transactional value validation |
| `src/model.js` | form-control coercion, IME handling, and two-way field synchronization |
| `src/events.js` | generic delegated dispatch, modifiers, `$event` snapshots, and model events |
| `src/service.js` | optional private service registrar, namespace snapshotting, and final sealing |
| `src/morph.js` | Hydrate-only private DOM reconciliation and boundary lifecycle bridge |
| `src/drive.js` | Hydrate-only same-origin navigation, measured lifecycle signal, compatibility, history, and document continuity |
| `src/boot.js` | final validation, auto-boot, and the only `globalThis.kit` publication |

Every fragment is an ordinary classic browser script. Loading them in the
listed order has the same behavior as loading the concatenated artifact.
`scope.js` occurs immediately after `evaluator.js` and before `component.js` in
both profiles. The Go assembler performs no transformation; it joins exact
fragment bytes through two explicit deterministic manifests. `morph.js` and
`drive.js` occur only in the Hydrate manifest, immediately before `boot.js`:

```powershell
go run ./jit/javascript/cmd/assemble ./jit/javascript/kit.js
go run ./jit/javascript/cmd/assemble -profile hydrate -output ./jit/javascript/hydrate.kit.js
```

The first command is retained as the backwards-compatible shorthand for
`-profile kit`. Those commands write the two development aliases without a
package manifest. For an immutable production artifact, pass exact service and
component pins with their classic-script packages:

```powershell
go run ./jit/javascript/cmd/assemble `
  -profile kit `
  -service storage=1.0.0=./jit/javascript/service/storage/1.0.0.js `
  -component preferences=1.0.0 `
  -component-require preferences=storage=1.0.0 `
  -script preferences=./jit/javascript/examples/preferences/preferences.js `
  -canonical-dir ./dist
```

The assembler writes `kit.0.9.0-next.12.<sha256>.js` without replacing an existing
different file at that path. Use `-profile hydrate` to produce
`hydrate.kit.0.9.0-next.12.<sha256>.js`. Repeat
`-service-require owner=dependency=version` for exact service dependencies.
Repeat `-component-require owner=service=version` for exact component-to-service
dependencies. Repeat `-service-action service=method` for any method a custom
app graph deliberately grants to authored actions. Dependency edges,
component grants, and action sets all participate in the immutable graph
identity.
Package and CLI identifiers may use their own notation; the HTML contract
remains the two separate component attributes above. Services have no HTML
version attribute.

This assembler is the Kitwork delivery seam: any future CDN publication and Go
delivery must ship these same bytes, not two implementations of KitJS.

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
| update | `++name`, `name++`, `--name`, `name--` (actions only) |
| unary | `!`, `-`, `+` (including `!!value`) |
| postfix | `.name`, `[key]`, `(args)`, `?.name`, `?.[key]`, `?.(args)` |

Primary values are finite decimal numbers (including exponents), quoted
strings with escapes, booleans, `null`, identifiers, arrays, null-prototype
objects, parenthesized expressions, and expression lambdas such as
`(item) => item.name`.

Ordinary property, computed, and call links are strict when their receiver or
callee is `null` or `undefined`. Optional links `?.name`, `?.[key]`, and
`?.(args)` short-circuit a continuous chain, skip computed keys or arguments,
and produce `undefined`; parentheses end the chain. A present non-callable value
remains an error. Calls support component methods, own object methods, the non-mutating array
methods `join`, `includes`, `indexOf`, `slice`, `map`, `filter`, `find`, `some`,
`every`, the string methods `includes`, `startsWith`, `endsWith`, `trim`,
`toLowerCase`, `toUpperCase`, and number `toFixed`. Receiver identity is
preserved.

Bindings are read-only. Actions can assign or apply prefix/postfix `++`/`--` to
a direct writable identifier (a boundary data field or expression-lambda local)
and can sequence expressions with semicolons. Member assignment/update, page
scope `$`, implicit field creation, comma
expressions, declarations, statements, loops, `new`, `typeof`, templates,
compound/bitwise operators are rejected.

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
element-owned `WeakMap` records. Each scope or component boundary owns one
shallow dirty bit: changing its state queues that boundary once for the next
microtask, then KitJS queries and renders only elements whose nearest reactive
boundary host is that owner. Parent, child, sibling, and unrelated boundaries
are not evaluated. This is a boundary scheduler, not dependency tracking:
there are no effects, signals, watchers, or property-read subscriptions.

Structural records own only the nodes cloned
from their template: `if` disposes its branch when false, and `for` reuses,
moves, or disposes whole clone groups by key. Retained keys retain their real
DOM nodes and DOM-local state; moving them does not dispose them. Removed
branches dispose private descendants deepest-first. When retained row locals
change, ownership propagation marks only nested reactive boundaries in that
row; it still does not build a read graph. One microtask batches repeated writes
to the same owner, and DOM writes are diffed against their last value. There is
no virtual DOM.

`data-kit-bind` writes attributes plus a small form-state property allowlist. It
rejects event handlers, `data-kit-*`, raw style, `srcdoc`, `innerHTML`, `outerHTML`,
text-replacement sinks, and unsafe URL schemes. HTML content belongs to authored
markup or `data-kit-text`, never to an expression sink.

`data-kit-style` does not relax that bind boundary. It accepts only fixed
`property: binding-expression;` entries, writes each through CSSOM after the
entire map validates, and never owns `cssText`, CSS shorthands, `var()`/`attr()`
indirection, or an undifferentiated style attribute.

Events use one document listener per supported event type, plus composition
start/end listeners for IME-safe models. Outside handlers query their current
candidates at event time. Every top-level Promise returned by an action is
observed against the boundary that produced it; repeated references to the same
Promise are grouped across their owners. Pending observations retain only
primitive tokens and resolve owners from the connected DOM, never from a global
component registry.

No page-lifetime collection owns an element or scope. Components without owned
lifecycle work remain DOM/WeakMap-owned and need no observer. A synchronous
cleanup returned from `init()`, a listener/cleanup registered through the
lifecycle context, or a pending `afterRender` callback enables one private,
lazy, move-aware removal observer for lifecycle owners only. Structural
removal, Morph replacement, and direct DOM removal dispose owned work exactly
once; moving a host within the same document does not. The observer disconnects
when the last lifecycle owner is released. Cleanup errors are reported while
disposal continues.

Explicit application references, such as a callback stored by another
component, remain the application's responsibility and must be cleared when
finished. A pending debounce retains only its compiled event state until the
bounded timer settles; it does not retain the element or boundary store. The
compile cache is capped at 256 sources. Loading the same runtime twice is a
no-op; a conflicting runtime fails before listeners are installed.

Directive source, boundary kind, scope source, and component identity are
immutable after an element is first prepared. Removing a value/event attribute
disables it; restoring it reuses the original compiled program. Structural
`if`, `for`, and `key` attributes and boundary metadata must not be changed or
removed after preparation. This prevents DOM mutation from becoming a second
compiler channel.

The base assumes directive-bearing templates are authored before boot. Direct
events inside structural clones are resolved lazily, but inserting the page's
first `outside` handler after boot is not part of the base contract.

`init(context)` is for one-time trusted initialization. Its frozen context has
exactly five enumerable keys and never enters component state or authored
expressions:

| Context key | Engine contract |
|---|---|
| `host` | Getter for the current host; returns `null` after disposal rather than pinning detached DOM. |
| `owned(selector)` | Fresh frozen owned-match snapshot, including the host if it matches and pruning `data-kit-ignore` plus nested scope/component boundaries. It queries again after structures or Morph change descendants. |
| `listen(target, type, fn, options)` | Native listener plus idempotent disposer and automatic removal. Removal snapshots capture even if an options object is later mutated; an already-aborted signal remains harmless. |
| `cleanup(fn)` | Register an owned disposer. Its returned idempotent function runs and unregisters it; disposal drains the remainder in LIFO order. |
| `afterRender(fn)` | One-shot callback after the boundary's next complete render, with an idempotent cancellation function. An `init` registration runs after the initial render; recurring work must re-arm itself. |

Context lifecycle callbacks run with the component store as `this`. After
disposal all context operations fail closed and schedule no work. Compatible
`data-kit-retain` Morph reconciliation preserves the context, while dynamic
`owned()` calls observe the reconciled DOM.

For compatibility, `init()` may also return one **synchronous cleanup
function**. A `Promise<cleanup>` is intentionally unsupported: asynchronous
`init()` is observed for state settlement, but its resolved value cannot become
a disposer. Durable resources should use `context.listen()` or
`context.cleanup()`. None of this expands the public component API with mount,
unmount, or destroy methods.

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

Browser tests exercise pure-data scope parsing, server-escaped snapshots,
nearest-boundary ownership, component seeding, the full expression grammar and
security rejection corpus, duplicate loading, live event candidates,
compile-once behavior, cleanup ownership, Hydrate navigation/fallback/progress,
and forced-GC release of detached boundaries. The tests never depend on a
public compiler or diagnostics API.
