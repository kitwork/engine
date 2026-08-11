# KitJS sealed services

KitJS services are small, trusted platform primitives sealed into one Kit or
Hydrate artifact. Production uses `kit.0.8.0.<sha256>.js` or
`hydrate.kit.0.8.0.<sha256>.js`; `kit.js` and `hydrate.kit.js` are development
aliases. Services are not directives, components, a runtime package manager, or
a generic native bridge.

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
- Trusted component JavaScript may capture `kit.*` lexically. Authored HTML
  expressions never receive `kit`, a service locator, or native browser
  objects.
- Only one exact version of a service name may exist in one graph.
- Browser, desktop, and mobile builds may use different implementations of the
  same versioned contract. The selected adapter remains private and sealed;
  there is no public `kit.bridge` dispatcher.

Services do not own visible reactive UI. Components subscribe to service state
and own presentation plus cleanup. `announce` is the narrow accessibility
exception: it owns hidden ARIA live regions, never application UI.

## Catalog

The Vanilla 0.8.0 catalog contains ten services, all currently at `1.0.0`:

| Namespace | Exact public surface | Purpose | Exact dependency |
|---|---|---|---|
| `kit.announce` | `say`, `polite`, `assertive`, `clear` | Bounded ARIA live announcements | none |
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

## Behavioral boundaries

### `announce@1.0.0`

`say(message, mode)` accepts `polite` or `assertive`; the convenience methods
select a channel. Messages are non-empty strings up to 1024 characters. A newer
announcement supersedes the pending announcement in the same channel. `clear`
clears one channel or both. Detached live-region nodes are not retained.

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
`error`, and `fallback`. `subscribe` returns an idempotent cleanup. A component
such as `progress-bar` presents that state and owns its cleanup.

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

The Vanilla assembler accepts services and exact dependency edges explicitly:

```text
go run ./jit/javascript/vanilla/cmd/assemble -profile hydrate \
  -service progress=1.0.0=./jit/javascript/vanilla/service/progress/1.0.0.js \
  -service request=1.0.0=./jit/javascript/vanilla/service/request/1.0.0.js \
  -service-require request=progress=1.0.0 \
  -component request-form=1.0.0 \
  -component-require request-form=request=1.0.0 \
  -script request-form=./jit/javascript/vanilla/examples/request-form/request-form.js \
  -canonical-dir ./public/assets
```

The output name includes runtime `0.8.0` and the full SHA-256 of the exact
bytes. Existing canonical files are never replaced, so older pages and open
tabs can continue using their original closed graph.

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
