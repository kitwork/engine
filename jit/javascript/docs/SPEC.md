# KitJS integration contract

This file is an integration map, not a second KitJS specification. The
canonical source-candidate contract for `0.9.0-next.12` is the package-tree
`packages/kit.js/KITJS_SPEC.md`. The version identifies locally checked source;
it does not establish npm or CDN availability. The flattened engine copy and
Kitwork-specific delivery behavior are documented in the
[KitJS runtime README](../README.md).

## Public browser surface

KitJS publishes one frozen `globalThis.kit` object:

```text
kit.version
kit.component(name, plainObject)
```

Kit and Hydrate are mutually exclusive delivery profiles. Kit provides the
closed expression/directive/component runtime. Hydrate contains the exact Kit
profile plus private Morph and Drive continuity. Neither profile exposes its
parser, evaluator, renderer, lifecycle, Morph, Drive, graph manifest, or
package loader.

The authored surface is `data-kit-*`. HTML expressions execute in the bounded
KitJS language, never through `eval()` or `Function`, and cannot resolve page
globals or the trusted `kit` root. A sealed canonical `app@1.1.0=$app` graph may
grant only exact direct service commands in actions. Bindings cannot read
service namespaces; their one narrow App path is the read-only ordinary state
`$app.loader.visible` and `$app.loader.value`. Trusted component scripts are
ordinary JavaScript and remain part of the application's trusted computing
base.

`data-kit-ignore` is one shared ownership boundary across Kitwork preflight,
the browser runtime, and Hydrate Morph. Its host and complete subtree are inert
to KitJS, and Morph treats that subtree as opaque. It is not a component store,
retention key, or package boundary.

## Kitwork delivery

`router.jitjs(true)` is the shorthand that opts one prepared site generation
into KitJS. A site with tenant components uses
`router.jitjs({ components: { ... } })`. Kitwork scans every prepared document and resolves its exact closed Hydrate package
graph. The generation emits a static, engine-owned script sequence rather than
one route-union artifact:

```text
runtime -> hydrate -> graph -> services (topological order)
        -> optional common components bundle -> individual components
```

Every entry is a classic `defer` script with exact `data-kitwork-jit` role,
`data-kitwork-hash` SHA-256, `/jit/<hash>.<suffix>.js` source, matching
`integrity="sha256-..."`, and `crossorigin="anonymous"`. Fixed-role suffixes
are `runtime`, `hydrate`, `graph`, and `components`; a service or individual
component uses its package name, such as `.progress.js` or `.dialog.js`. The
graph opens the private manifest before packages register and publishes/boots
only after the final expected package registers successfully. Missing,
duplicated, reordered, metadata-mismatched, or failed packages leave the SSR
document inert.

`router.jitjs({ components: { name: { version, source } } })` may overlay
tenant-managed components on the pending generation. Names, exact SemVer
versions, and relative source paths are literal values; hyphenated names are
quoted object keys. The object manifest is valid only in the site-root
`router.kitwork.js`. Each path is resolved and confined relative to that root
router folder; source bytes are bounded, snapshotted, canonicalized,
watched for reload, and content-addressed. One tenant name has one exact
version and cannot shadow an embedded component. Tenant packages declare no
services or grants in this contract. A document that does not use the name
receives no package chunk.

```js
router.jitjs({
  components: {
    counter: {
      version: "1.0.0",
      source: "./components/counter.js"
    }
  }
});
```

These attributes and URLs belong to the engine namespace; authored HTML cannot
select or override them. Staged delivery has no `data-kitwork-plan` identity.
The sealed graph plus the exact ordered role/hash/URL/SRI metadata is the
delivery identity. Authored HTML does not choose the profile and does not need
`data-kit-app`, `data-kit-hydrate`, or a navigation root.

Generation injection preserves effective charset and meta
`Content-Security-Policy` declarations before the staged sequence and places
that sequence before any active or potentially active `<base>`. Authored or
dynamic head ordering that cannot preserve this boundary is a generation
error, not a request-time rewrite.

```html
<section data-kit-scope="count: 0">
  <button type="button" data-kit-click="count = count + 1">Increment</button>
  <output data-kit-text="count">0</output>
</section>
```

The generation owns each route graph. Runtime, Hydrate, services, components,
and identical graphs deduplicate by content hash. If at least two exact
component name/version pairs occur in every prepared document, that intersection
is emitted once as the stable `components` chunk. Those packages are removed
from the individual component set, so the two chunk classes never overlap.

A Drive response with the identical ordered staged delivery is compatible when
its document checks pass. A response with a different graph may also be
compatible, but only as a component-only handoff: runtime, Hydrate, the entire
service set, authored service-action sets, and service assets must be exact.
Every component present in both graphs must have the same name, exact version,
raw source hash, and grants. Added and removed component names are permitted.

Drive must verify the target graph and load only missing immutable component
chunks. A component cache identity consists of name, exact version, and raw
source hash; the cache is bounded at 256 identities. An inactive cached package
has no registry presence or service authority. A common `components` bundle
may be reused whole or loaded whole, but partial overlap is incompatible.

Every dynamically loaded component package is a registration-only installer.
Installers execute only after every required asset and target-document check
succeeds, as part of the synchronous atomic switch of active registry, graph,
and delivery immediately before Morph. Package-scope DOM writes, listeners,
timers, network work, and unrelated global mutation are outside this contract.

The ordered effective meta `Content-Security-Policy` values must still exactly
equal the active document. Any `Content-Security-Policy` or
`Content-Security-Policy-Report-Only` response header forces ordinary browser
navigation because Fetch cannot install a document policy. Runtime, Hydrate,
service, CSP, root, retain, overlapping component version/source/grant,
URL/hash/SRI, malformed-delivery, load, and active-visit cancellation failures
fall back before Morph; superseded visits cannot commit. The public `kit`
object and document roots retain identity, while normal retained component and
form-state rules still apply.

No executable script node from fetched HTML is inserted or run. A compatible
handoff may create fresh temporary engine-owned same-origin graph/component
nodes from sealed metadata. Those nodes require exact SRI, carry
`data-kitwork-handoff`, and are removed after registration. This path never
loads runtime, Hydrate, or service scripts.

Before either identical-delivery Morph or component-only handoff, authored
executable scripts must also match. A compatible authored script is a classic
external direct child of `head` with `defer` and the same resolved URL, ordered
position, and full attribute set. It must carry valid SRI, unless it is
same-origin and carries the exact `data-kit-drive="stable"` marker. This
ordinary-authored-script exception works in standalone and staged documents,
including a self-hosted standalone Hydrate tag, but never relaxes the exact
URL/hash/SRI rules for engine-owned runtime, Hydrate, graph, service, or
component assets. A cross-origin script must omit the stable marker and carry
valid SRI; stable plus a cross-origin URL is incompatible even when SRI is
present. The stable marker is an author promise: Drive compares the tag
identity but does not fetch or hash that script. Replacing bytes behind its
unchanged URL violates the contract.

Inline/body/module/import-map/speculation-rule scripts, `async`, `nomodule`, an
unknown script policy, and any addition, removal, reorder, or attribute change
are incompatible. Inert data scripts do not participate. Hydrate validates the
initial executable topology before Drive installs navigation listeners or
claims history/scroll ownership. An invalid initial topology disables Drive and
leaves navigation native without a Drive fetch or lifecycle event. A mismatch
found in a fetched destination after a valid start uses normal browser
navigation before live mutation. The disabled initial state emits one console
warning identifying KitJS Drive; the exact message is not API. Drive never
evaluates fetched source.

Explicit same-document fragment clicks remain native, including `#`, so the
browser retains `hashchange` and CSS `:target`; fragment-only Back/Forward does
not fetch or Morph. Cross-route Drive preserves the requested fragment through
Fetch redirects, then resolves exact raw and decoded Unicode `id`/`a[name]`
targets without selector parsing or normalization. Malformed encoded UTF-8
uses replacement semantics without throwing. For cross-route fragments Drive
guarantees URL, focus, and scroll, not native CSS `:target` activation.

Private scroll history writes are deduplicated and limited to one per 250 ms,
with an exact synchronous flush before outgoing visits and `pagehide`. A
cross-route popstate cancels the old page's pending write rather than
clobbering the already-active destination entry.

## Closed package graph

Go/build selects exact component and service versions, validates dependency
edges, orders each classic package script once, and hashes every chunk plus the
complete graph identity. The browser never resolves versions, upgrades a
package, chooses a package, or dynamically loads a service. Component-only
handoff may retrieve only exact missing component assets already selected by
the sealed target graph.

```html
<section
  data-kit-component="progress-bar@2.0.0"
  data-kit-retain="app-progress">
</section>
```

Managed component identity uses `data-kit-component="name@exact-semver"` and
is checked against the artifact's private manifest. Ranges, `latest`, and a
`v` prefix are invalid. The split `data-kit-version` form is a deprecated
compatibility input for one 0.9 release and is removed in 1.0.
`data-kit-retain` is a Hydrate Morph identity for an application-owned
component; it is neither a package selector nor an HTML `id`.

Trusted client code registers an unknown page component against an unversioned
host. It may not carry `data-kit-retain`, shadow a managed catalog name, or
receive graph grants. The empty `data-kit-local` marker remains a deprecated
compatibility input for one 0.9 release and is removed in 1.0; canonical direct
registration does not use it.

Services are sealed platform primitives exposed only when selected. Their
component-facing namespaces are frozen, carry an exact non-enumerable version,
and disappear entirely when omitted. The canonical `$app` facade can expose
only a graph-declared static action allowlist; it never copies a namespace into
reactive state or exposes reads, subscribers, network work, computed members,
or extracted methods. App 1.1.0 separately adapts progress into the frozen
ordinary-state snapshot `{ visible, value }`; it does not expose a progress
snapshot through bindings. See [SERVICES.md](SERVICES.md) for the current
catalog.

## Ownership and lifecycle

The connected DOM owns bindings and reactive boundary records. Each scope or
component has one shallow dirty flag and schedules at most one render in the
next microtask. KitJS does not build a property-read dependency graph or a
virtual DOM.

Structural templates own only their clones. Removed private boundaries dispose
deepest-first; moving a host within the same document is not removal. A
component's synchronous `init()` may return one cleanup function. No
page-lifetime collection may keep detached hosts or stores alive.

Dynamic classes own complete class tokens. Dynamic styles use only the bounded
semicolon-map `data-kit-style="property: expression;"` grammar and commit all
per-property CSSOM writes transactionally. CSS shorthands, outer object braces,
raw style binds, unsafe CSS sinks, `var()`/`attr()` indirection, and unbounded
maps are outside the contract.

Hydrate reconciles `body`. It retains a component only through a valid unique
`data-kit-retain` key and preserves dirty form state only for a compatible
control with a non-empty `id`. A retained component keeps its live store; a
changed scope declaration on an ordinary boundary causes disposal and a fresh
mount. A `data-kit-ignore` subtree remains externally owned and is not
reconciled as KitJS UI.

## Version and change policy

- `0.9.0-next.12` identifies this prerelease browser contract.
- An npm version identifies the immutable standalone base artifacts.
- A closed artifact additionally uses the SHA-256 of its exact runtime,
  packages, versions, and dependency edges.
- Component and service versions are independent exact SemVer values.
- Any observable contract change requires a KitJS release version change; any
  changed graph bytes require a new content identity.

Practical syntax and runnable routes live in [DIRECTIVES.md](DIRECTIVES.md) and
[EXAMPLES.md](EXAMPLES.md). Those guides defer to the canonical contract when
wording differs.
