# KitJS directives

This is a practical reference for KitJS `0.9.0-next.12`. The normative browser
contract is [KITJS_SPEC.md](https://github.com/kitwork/kit.js/blob/master/KITJS_SPEC.md);
the Kitwork delivery model is described in the [runtime README](../README.md).
Unknown directives, events, modifiers, and invalid combinations fail closed.

## Boundary metadata

| Attribute | Contract |
|---|---|
| `data-kit-scope="count: 3; open: true"` | Creates one anonymous shallow store, or seeds the component on the same host. Values use the bounded pure-data grammar. |
| `data-kit-component="counter@1.0.0"` | Creates one isolated managed instance and asserts its exact closed-graph identity. Direct client registrations use an unversioned name. It never selects, downloads, or upgrades code in the browser. |
| `data-kit-as="$counter"` | Gives a component an action-only alias. Bindings cannot observe alias state, except the exact canonical App 1.1 loader fields `$app.loader.visible` and `$app.loader.value`. |
| `data-kit-retain="app-counter"` | In Hydrate, preserves this exact component host and live store across a compatible Morph. The key is unique and is not an HTML `id`. |

`data-kit-scope` and `data-kit-component` cannot be placed on `<template>`.
Put a boundary inside the template content instead. A retained host must be a
component, cannot be nested, and cannot live in a template or structural
region. Retention still reconciles the host's attributes and children; it is
not an opaque subtree.

Managed component names and exact versions share one canonical spec:

```html
<section
  data-kit-component="progress-bar@2.0.0"
  data-kit-retain="app-progress">
</section>
```

Ranges, `latest`, and a `v` prefix are invalid. The browser never resolves
package versions.

Prefer a content-addressed tenant package declared with
`router.jitjs({ components: { name: { version, source } } })`. Small trusted
client-defined components outside that graph use an unversioned host and
direct registration; they cannot shadow either the embedded catalog or a
tenant package, receive service grants, or be retained across Morph. The split
`data-kit-version` attribute and empty `data-kit-local` marker remain deprecated
compatibility inputs for one 0.9 release and are removed in 1.0.

Use `data-kit-ignore` only when another owner controls a DOM subtree:

```html
<section data-kit-ignore class="rounded-2xl border border-slate-200 p-4">
  <div id="third-party-widget"></div>
</section>
```

Kitwork's scanner and the browser runtime both skip the marked host and every
descendant. Hydrate does not reconcile the opaque subtree. The marker is not a
reactive boundary or a stable identity and does not retain a component store;
use `data-kit-retain` for application-owned component continuity. Do not place
KitJS directives that must run inside an ignored subtree.

## Hydrate navigation metadata

`data-kit-drive` has two exact, element-specific policies:

| Value and host | Contract |
|---|---|
| `data-kit-drive="false"` on a link, same-origin GET form, submitter, or ancestor | Leaves that otherwise eligible navigation to the browser. |
| `data-kit-drive="stable"` on an authored `<script>` | Allows one same-origin external classic direct child of `head` with `defer` to participate in Drive's executable topology without SRI. |

The stable script must keep the same resolved URL, ordered position, and full
attribute set in the current and incoming documents. It can be an ordinary
authored script in standalone or Kitwork-staged HTML, or the self-hosted
standalone Hydrate tag itself. A cross-origin script must omit the stable marker
and carry valid SRI; stable plus a cross-origin URL is incompatible even when
SRI is present. The marker does not make inline, body, module, import-map,
speculation-rule, `async`, or `nomodule` scripts compatible, and it never
weakens the sealed URL/hash/SRI contract of engine-managed staged assets.

Hydrate checks the initial executable topology before Drive installs navigation
listeners or claims history and scroll restoration. If it is invalid, Drive is
disabled for that document: links and forms remain native, no Drive fetch or
`kit:navigation` event occurs, and the browser document loader continues to own
script execution. One console warning identifies KitJS Drive as disabled. A
mismatch discovered only after a valid Drive start and destination fetch still
falls back to normal navigation before live mutation.

## Bindings and models

| Directive | Contract |
|---|---|
| `data-kit-text="count"` | Writes the synchronous result through `textContent`. |
| `data-kit-show="open"` | Toggles the element's `hidden` property without removing it. |
| `data-kit-bind="aria-expanded: open;"` | Owns safe attributes and the permitted form properties. Unsafe sinks and URL schemes are rejected. |
| `data-kit-class="open ? 'block opacity-100' : 'hidden opacity-0'"` | Owns dynamic class tokens while preserving static classes. |
| `data-kit-style="width: progress + '%'; opacity: visible ? 1 : 0;"` | Owns bounded continuous CSS values without replacing the authored style attribute. |
| `data-kit-model="name"` | Two-way binds one existing writable top-level field matching `[A-Za-z_][A-Za-z0-9_]*` on the nearest boundary. `$` names and nested paths are invalid. |

Keep every Tailwind candidate literal and complete. Do not construct utility
names from string fragments.

Use classes for discrete design states. Style bindings are for continuous
values and have one grammar—no outer braces:

```html
<div data-kit-style="width: progress + '%'; --meter: progress + '%';"></div>
```

The final semicolon is optional. Names are fixed longhand or custom CSS
properties, entries must be unique, and the map is bounded to 128 entries and
16,384 UTF-16 code units. Shorthands are rejected because they implicitly own
multiple longhands.
The browser validates all results before writing any of them. Strings and
finite numbers set a property; nullish values, `false`, and empty strings
restore its authored inline baseline. Unsafe CSS values, `var()`/`attr()`
indirection, and raw `data-kit-bind="style: ..."` fail closed.
Kitwork validates this map during generation but leaves its authored inline
fallback intact; dynamic style evaluation begins in the browser runtime.

```html
<section
  data-kit-scope="count: 3; open: true"
  class="rounded-2xl border border-slate-200 p-6">
  <button
    type="button"
    data-kit-click="count = count + 1"
    data-kit-bind="'aria-expanded': open;"
    class="rounded-lg bg-indigo-600 px-4 py-2 font-semibold text-white">
    Increment
  </button>

  <output data-kit-text="count" class="ml-3 font-mono text-slate-900">3</output>
  <p
    data-kit-show="open"
    data-kit-class="open ? 'block text-emerald-700' : 'hidden text-slate-500'"
    class="mt-4">
    Ready
  </p>
</section>
```

Models support text inputs, textareas, single and multiple selects, checkbox
booleans or arrays, radio groups, and finite number/range values. Invalid or
empty numeric input becomes `null`; text input respects IME composition.

## Structural templates

`data-kit-if` and `data-kit-for` are valid only on `<template>`:

```html
<section data-kit-scope="items: [{ id: 1, name: 'Alpha' }, { id: 2, name: 'Beta' }]">
  <ul class="grid gap-2">
    <template data-kit-for="item, index of items" data-kit-key="item.id">
      <li class="rounded-lg bg-slate-100 px-3 py-2 text-slate-900">
        <span data-kit-text="index + 1"></span>.
        <span data-kit-text="item.name"></span>
      </li>
    </template>
  </ul>
</section>
```

`data-kit-for` also accepts `item of items`. `data-kit-key` is optional, but a
key should be used when rows can move; it must evaluate to a unique string or
finite number. Row locals are read-only. A template cannot combine `if` and
`for`, and `key` is invalid without `for`.

## Events and modifiers

The exact delegated event set is:

```text
click dblclick submit input change keydown keyup
pointerdown pointerup focusin focusout
```

Write an action as `data-kit-<event>="program"`. Available modifiers are
`self`, `prevent`, `stop`, `once`, `outside`, `enter`, `escape`, and
`debounce(ms)`:

```html
<form
  data-kit-scope="query: ''; saved: false"
  data-kit-submit:prevent:once="saved = true">
  <input data-kit-model="query" data-kit-input:debounce(250)="query = query.trim()">
  <button type="submit">Save</button>
  <p data-kit-show="saved" hidden>Saved</p>
</form>
```

The debounce delay is an integer from 1 through 60,000 milliseconds. `enter`
and `escape` apply only to `keydown`/`keyup` and cannot be combined. `outside`
applies only to `click`, `dblclick`, `pointerdown`, `pointerup`, and `focusin`,
and cannot be combined with `self`.

Bindings are read-only. Actions may assign an existing writable top-level
field and sequence expressions with semicolons. This is the closed KitJS
expression language, not JavaScript: page globals, declarations, member
assignment, implicit fields, template literals, and arbitrary method calls are
not available.
