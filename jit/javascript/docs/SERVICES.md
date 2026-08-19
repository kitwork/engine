# KitJS sealed services

KitJS services are small, trusted platform primitives sealed into one Kit or
Hydrate artifact. The current browser runtime is `0.9.0-next.12`; production
uses `kit.0.9.0-next.12.<sha256>.js` or
`hydrate.kit.0.9.0-next.12.<sha256>.js`. `kit.js` and `hydrate.kit.js` are
development aliases. Services are not directives, components, a runtime
package manager, or a generic native bridge.

Go selects exact package versions, resolves dependencies, orders their classic
scripts, and hashes the complete artifact. The browser never chooses a version
or fetches service code. A graph that does not select a service does not ship
its source or expose its namespace.

## Runtime contract

- The base runtime exposes only `kit.version` and `kit.component`.
- A selected service adds one frozen namespace such as `kit.request`.
- Every namespace carries a non-enumerable exact `version`.
- The temporary `kit.service()` registrar exists only while the artifact is
  assembled. It is removed before component packages run and before
  `globalThis.kit` is published.
- Trusted component JavaScript may capture every selected `kit.*` namespace
  lexically. Authored HTML never receives the trusted `kit` root, a generic
  service locator, or native browser objects. A sealed `app@1.1.0=$app` graph
  may grant only the direct static action calls documented below.
- Only one exact version of a service name may exist in one graph.
- Browser, desktop, and mobile builds may use different implementations of the
  same versioned contract. The selected adapter remains private and sealed;
  there is no public `kit.bridge` dispatcher.

Services do not own visible reactive UI. Components subscribe to service state
and own presentation plus cleanup. `app@1.1.0` is the canonical application
adapter for progress; `progress-bar@2.0.0` remains its standalone alternative.
`announce` owns only hidden ARIA live regions. `appearance` is the
document-presentation exception: it owns the root color mode, but no visible
application markup.

## Catalog

The KitJS `0.9.0-next.12` closed-package catalog contains eleven services. Their
service contract versions are independent of the runtime version and are all
currently `1.0.0`:

| Namespace | Exact public surface | Purpose | Exact dependency |
|---|---|---|---|
| `kit.announce` | `say`, `polite`, `assertive`, `clear` | Bounded ARIA live announcements | none |
| `kit.appearance` | `mode`, `resolved`, `snapshot`, `subscribe`, `set`, `toggle`, `system` | Document light/dark/system presentation state | none |
| `kit.clipboard` | `writeText`, `readText` | Text clipboard capability with normalized errors | none |
| `kit.cookie` | `get`, `set`, `remove`, `has` | Bounded script-readable cookie primitives | none |
| `kit.fullscreen` | `request`, `exit`, `active` | Fullscreen browser capability | none |
| `kit.navigation` | `back`, `forward`, `reload` | Browser history traversal and reload | none |
| `kit.network` | `online`, `snapshot`, `subscribe` | Browser-reported online/offline state | none |
| `kit.progress` | `snapshot`, `subscribe`, `start`, `update`, `finish` | Latest-visible operation progress stream | none |
| `kit.request` | `send`, `get`, `post`, `abort` | Same-origin bounded HTTP client | `progress@1.0.0` |
| `kit.share` | `open`, `canShare` | Native Web Share with text clipboard fallback | `clipboard@1.0.0` |
| `kit.storage` | `get`, `set`, `remove`, `has`, `clear` | JSON local persistence in the `kit:` namespace | none |

`kit.drive` is deliberately absent. Morph and Drive are private parts of the
Hydrate profile, selected at artifact build time rather than exposed as a
service API.

## Authored `$app` commands

The canonical `app@1.1.0` component closes only the services with a
non-empty authored action surface. Mount it under the exact `$app` alias:

```html
<html
  data-kit-component="app@1.1.0"
  data-kit-as="$app">
  <body>
    <button data-kit-click="$app.appearance.toggle()">Toggle theme</button>
    <button data-kit-click="$app.storage.set('draft', draft)">Save draft</button>
  </body>
</html>
```

The exact canonical allowlist is:

| Service | Authored action methods |
|---|---|
| `announce` | `say`, `polite`, `assertive`, `clear` |
| `appearance` | `set`, `toggle`, `system` |
| `clipboard` | `writeText` |
| `cookie` | `set`, `remove` |
| `fullscreen` | `request`, `exit` |
| `navigation` | `back`, `forward`, `reload` |
| `progress` | `start`, `update`, `finish` |
| `share` | `open` |
| `storage` | `set`, `remove` |

Only a direct static call shaped as `$app.service.method(...)` is valid, and
only in an action attribute. The exact app identity, exact `$app` alias,
component-to-service grant, selected service version, and method grant must all
match the frozen graph. `$app.service` resolves to the exact frozen
`kit.service` namespace while the call runs; it is virtual action authority,
not a wrapper, copied component field, or reactive store.

Bindings, reads, computed or optional service members, extracted methods,
assignment, passing a namespace/method as a value, and any other component
identity or alias fail closed. This keeps `get`, `has`, `clear`, `snapshot`,
`subscribe`, every `network` and `request` operation, and the progress read and
subscription APIs in trusted JavaScript. Trusted component packages still
receive each selected service's complete namespace.

App's `loader` field is not a service projection. It is ordinary reactive
component state containing a frozen `{ visible, value }` object, replaced as a
whole on each progress event. Read-only authored bindings may observe exactly
`$app.loader.visible` and `$app.loader.value`; no binding may read
`$app.progress` or assign loader state. `value` is null while indeterminate or
an integer from 0 through 100. Active measured work is floored and capped at
99. Loaded completion shows 100 then resets after 300 ms; cancelled, error,
and fallback completion reset immediately. App's lifecycle cleanup clears the
timer and unsubscribes. The immutable empty `app@1.0.0` remains selectable by
an exact pin and has no loader adapter.

## Behavioral boundaries

### `announce@1.0.0`

`say(message, mode)` accepts `polite` or `assertive`; the convenience methods
select a channel. Messages are non-empty strings up to 1024 characters. A newer
announcement supersedes the pending announcement in the same channel. `clear`
clears one channel or both. Detached live-region nodes are not retained.

### `appearance@1.0.0`

Appearance is the single document-lifetime owner of color mode. It normalizes
the stored preference to `system`, `light`, or `dark`, resolves system mode
through `(prefers-color-scheme: dark)`, applies the document root's `dark`
class and `style.colorScheme`, and persists the exact bare key `theme` in local
storage. Media changes update system mode and storage events synchronize tabs.
Unavailable storage or media APIs degrade without disabling manual commands.

`mode` and `resolved` are read-only getters. `snapshot()` returns the current
frozen `{ mode, resolved }` value. `subscribe(listener)` immediately delivers
it and returns an idempotent cleanup. `set(mode)`, `toggle()`, and `system()`
publish synchronously and return that current snapshot. The service does not
mutate `meta[name=theme-color]` or application markup.

`theme@3.0.0` is the reactive adapter: it depends on appearance, mirrors the
two fields through one subscription, and delegates its three commands. The
legacy independent owner `theme@2.0.0` remains selectable by itself, but a
graph containing both it and `appearance@1.0.0` is invalid.

### `clipboard@1.0.0`

`writeText(value)` and `readText()` use the browser Clipboard API. Text is
bounded to 1,048,576 JavaScript UTF-16 code units. Capability, permission,
cancellation, and other failures become sanitized `KitClipboardError` values
with stable codes; raw platform errors do not cross the service boundary.

### `cookie@1.0.0`

Cookie names, encoded values, paths, and the final assignment are bounded.
`set` accepts only `path`, `sameSite`, `secure`, and `maxAge`; `remove` accepts
only `path`, `sameSite`, and `secure`. Defaults are `Path=/`, `SameSite=Lax`,
and `Secure` on HTTPS. There is no `domain`, raw-cookie, `expires`, or
`HttpOnly` option. Authentication cookies should remain server-owned and
`HttpOnly`; this service is for ordinary client preferences.

### `fullscreen@1.0.0`

`request(target?)` defaults to `document.documentElement` and accepts only an
Element owned by the current document. `request` and `exit` return
`Promise<boolean>`; `active()` is synchronous. Failures use a sanitized
`KitFullscreenError` contract.

### `navigation@1.0.0`

`back`, `forward`, and `reload` are direct browser primitives. They do not
expose Drive, URL mutation, prefetching, Morph, or history-state ownership.

### `network@1.0.0`

`snapshot()` returns a frozen `{ online }` value and `subscribe(listener)`
immediately delivers the current snapshot. The service attaches browser
listeners on the first subscriber and removes them after the last idempotent
unsubscribe. Only an explicit `navigator.onLine === false` means offline;
`true` is not proof that an API endpoint is reachable.

### `progress@1.0.0`

Progress is a latest-visible state stream for navigation, request, manual, and
future platform producers. It is not an aggregate job manager and renders no
UI. `snapshot()` and each subscription deliver a frozen
`{ id, phase, source, url, loaded, total, outcome }` value. Phases are `idle`,
`start`, `progress`, and `finish`; terminal outcomes are `loaded`, `cancelled`,
`error`, and `fallback`. `subscribe` returns an idempotent cleanup.
`app@1.1.0` presents only `{ visible, value }`; `progress-bar@2.0.0` provides
the same presentation role for an application that does not mount App.

### `request@1.0.0`

The request service is same-origin, JSON-oriented, cancellable by a bounded
string key, and limits parsed responses to eight MiB. Its exact calls are
`send(url, { method, headers, data, key, timeout })`, `get(url, options)`,
`post(url, data, options)`, and `abort(key)`. A completed request resolves a
frozen result containing `status`, `url`, and parsed `data`; transport and
decode failures reject with a normalized `KitRequestError`. Trustworthy
streamed response-byte progress is published through `progress@1.0.0`. No raw
`Response` object crosses the service boundary.

### `share@1.0.0`

`canShare(input)` reports native capability and `open(input)` uses Web Share
when available. Input may be omitted for the current page, a URL string, or a
strict `{ title, text, url, files }` object. Title, text, and URL are bounded to
512, 65,536, and 4,096 code units; files are bounded to 16 entries and 256 MiB
total. Payloads without files may fall back to `clipboard@1.0.0`; file payloads
never do. A native permission denial or user cancellation is authoritative and
never triggers a clipboard fallback.

### `storage@1.0.0`

Storage serializes JSON under the `kit:` key prefix. `clear()` removes only
keys in that namespace, not all origin storage. Methods resolve safe values or
status: `get` returns the decoded value or fallback; `set`, `remove`, and `has`
return booleans; `clear` returns the number of removed keys. Applications
needing tenant-specific isolation should include that tenant identity in their
authored key.

## Building a closed graph

The KitJS assembler accepts services and exact dependency edges explicitly:

```text
go run ./jit/javascript/cmd/assemble -profile hydrate \
  -service progress=1.0.0=./jit/javascript/service/progress/1.0.0.js \
  -service request=1.0.0=./jit/javascript/service/request/1.0.0.js \
  -service-require request=progress=1.0.0 \
  -component request-form=1.0.0 \
  -component-require request-form=request=1.0.0 \
  -script request-form=./jit/javascript/examples/request-form/request-form.js \
  -canonical-dir ./public/assets
```

Use repeatable `-service-action service=method` flags to place authored method
grants in a custom graph. They are effective only through an exact app grant,
for example `-component-require app=storage=1.0.0`; they never publish a global
HTML service locator. Service actions, component grants, and dependency edges
all participate in the artifact identity.

The output name includes runtime `0.9.0-next.12` and the full SHA-256 of the
exact bytes. Existing canonical files are never replaced, so older pages and
open tabs can continue using their original closed graph.

## Package source shape

A service package is a classic script with one private registration call:

```js
;(function (kit) {
"use strict";

function available() {
  return true;
}

kit.service("capability", {
  available: available
});
})(kit);
```

Package source does not inspect `globalThis.kit`, self-deduplicate, publish its
own version, resolve dependencies, or fetch another package. Version and graph
identity are owned by Go and the sealed runtime.
