# Kitwork Client Runtime — Unified 1.0.0-draft

> One runtime. One author namespace. One lifecycle.
>
> **The page already exists. Runtime JS makes it alive.**

This package is the integrated M1–M5 implementation of the Kitwork HTML-first client runtime. The source is modular, while the browser receives a single generated file:

```text
dist/kitwork-runtime.js
```

Do not load this runtime together with the legacy KitJS kernel on the same page.

## What is complete

### M1 — Expression engine

- Seven parser modes: Named Map, Binding, Class Value, Action Program, Writable Path, Identity and Iterator.
- Private cached AST; no `eval`, no `new Function`, no public or serialized IR.
- Template literals, optional chaining, nullish coalescing, computed access and strict equality.
- Evaluation budget, call-depth limit and blocked member paths.

### M2 — Ownership and lifecycle

- Isolated `AppRecord`, `NodeRecord`, `ComponentRecord` and `BindingRecord` ownership.
- SSR-seeded `data-kit-scope` for application, lexical and component state.
- Per-host component instances, direct aliases, component-local refs and parent ownership.
- Mount, unmount, cleanup and task-abort lifecycle.

### M3 — Core directives and events

```text
data-kit-app
data-kit-component
data-kit-as
data-kit-scope
data-kit-ref

data-kit-text
data-kit-show
data-kit-model
data-kit-class
data-kit-style
data-kit-bind

data-kit-<event>:<modifier>
```

Event modifiers:

```text
:window :document :outside :enter :escape
:prevent :stop :once :debounce(ms) :throttle(ms)
```

### M4 — Structural runtime and scheduler

- Structural `data-kit-if`.
- Keyed `data-kit-for` + `data-kit-key`.
- Dirty-boundary scheduler; actions do not force a full-document render.
- MutationObserver subtree hydration and deferred cleanup.
- Same-app DOM moves preserve component identity; cross-app moves remount safely.

### M5 — Async, request and Drive

- `kit.task.run/latest/abort/delay` with owner-based cancellation.
- `kit.request` with CSRF, timeout, AbortSignal, JSON, FormData and normalized responses.
- `kit.drive` with HTML fetch, head reconciliation, lifecycle-aware morph, history hooks, scroll restoration, hover/focus prefetch and progressive GET/POST form enhancement.
- `data-kit-persist` keyed DOM reuse.
- Component state, input draft, focus, selection and keyed identity are preserved when the matching DOM survives a Drive swap.

## Install

```html
<script src="/kitwork-runtime.js"></script>
```

The runtime starts automatically after `DOMContentLoaded`. To configure it before loading:

```html
<script>
window.KITWORK_RUNTIME_OPTIONS = {
  development: true,
  drive: false,
  evaluationBudget: 10000,
  callDepthLimit: 64,
  prefetchDelay: 65,
  prefetchTtl: 30000,
  prefetchMax: 15
};
</script>
<script src="/kitwork-runtime.js"></script>
```

## Canonical example

```html
<main
  data-kit-app="main"
  data-kit-drive
  data-kit-scope="
    user: { name: 'Quoc' };
    open: false;
    saving: false;
  ">

  <div
    data-kit-component="dialog"
    data-kit-as="$paymentModal"
    data-kit-scope="count: 0;">

    <input data-kit-ref="input" data-kit-model="query">

    <section
      data-kit-if="open"
      data-kit-class="
        active: open;
        'opacity-50 pointer-events-none': saving;
      "
      data-kit-bind="
        aria-busy: saving;
        data-state: saving ? 'saving' : 'ready';
      ">
      <span data-kit-text="`Hello ${user?.name ?? 'Guest'}`"></span>
    </section>
  </div>

  <button data-kit-click="$paymentModal.show()">Open</button>
</main>
```

```js
kit.component("dialog", {
  open: false,
  query: "",

  show() {
    this.open = true;
  },

  close() {
    this.open = false;
  },

  mount() {
    return () => {
      // Optional cleanup.
    };
  }
});
```

## Runtime contexts

```text
$element   current directive element
$host      nearest component host
$event     current native event
$refs      nearest component ref registry
$component nearest component instance
$parent    parent component instance
$item      current loop item
$index     current loop index
$alias     direct component alias, e.g. $paymentModal
kit        curated service namespace
```

## Drive

Enable Drive per app root:

```html
<main data-kit-app="main" data-kit-drive>
```

Trusted JavaScript API:

```js
await kit.drive.visit("/settings");
await kit.drive.prefetch("/dashboard");
kit.drive.back();
kit.drive.forward();
kit.drive.reload();
```

Navigation events are emitted from the app root:

```text
kitwork:navigation-start
kitwork:before-swap
kitwork:after-swap
kitwork:load
kitwork:navigation-complete
kitwork:navigation-error
```

Drive does not inject a progress bar or CSS. Presentation belongs to application HTML/components.

## Build and test

```bash
npm test
```

Current integrated validation:

```text
Expression unit assertions:     49 / 49
Lexical ownership assertions:   10 / 10
Task/request unit assertions:   16 / 16
Shared conformance fixtures:    25 / 25
Browser integration assertions:137 / 137
----------------------------------------
Total:                         237 / 237
Page errors:                     0
Console errors:                  0
```

The browser suite covers core directives, multi-app isolation, lifecycle, async ownership, request behavior and Drive/morph integration.

## Source layout

```text
src/
├── core/
├── expression/
├── component/
├── directive/
├── service/
├── drive/
└── runtime.js
```

The single browser bundle is generated from these modules. **Single-file output does not require monolithic source.**

## Deliberately outside the always-loaded core

The following remain optional capabilities rather than unfinished M1–M5 work:

```text
teleport
transition
live/SSE
remember persistence policy
remote component loader/cache
native platform adapters
legacy KitJS compatibility adapter
```

The core exposes lifecycle and service seams for these additions without requiring a second runtime.
