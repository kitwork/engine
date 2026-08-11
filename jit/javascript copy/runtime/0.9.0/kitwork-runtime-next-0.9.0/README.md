# Kitwork Client Runtime Next — M2 Core

> **Status:** Runtime Draft `0.9.0`  
> **Specification:** `0.7.0-draft`  
> **Purpose:** A runnable, modular implementation of the HTML-first Kitwork client runtime.

> “The page already exists. Runtime JS makes it alive.”

This package turns the completed expression milestone into an actual browser runtime. It is not another isolated reference kernel: the source is split into ownership, expression, component, directive, service and scheduler modules, then composed into one browser bundle by the build script.

## What is implemented

### Runtime ownership

- Multiple isolated `data-kit-app` roots.
- `AppRecord`, `NodeRecord`, `ComponentRecord` and `BindingRecord` contracts.
- Logical ownership preserved during moves inside one app.
- Cross-app moves perform an explicit unmount/remount.
- Deferred removal cleanup avoids treating a same-app DOM move as an unmount.

### Zero-eval expression engine

- Seven parser modes: named map, binding, class value, action program, writable path, identity and iterator.
- Private cached AST; no `eval`, no `new Function`, no public/serialized IR.
- Template literals, optional chaining, nullish coalescing, strict equality and computed access.
- Evaluation budget, call-depth limit and blocked global/member paths.
- Shared JSON conformance fixtures intended for both JavaScript and Go.

### Scope and components

- SSR data and client state enter through `data-kit-scope`.
- Scope declarations initialize once and do not reset on re-render.
- Scope on a component host seeds one component instance state.
- Per-instance component state, methods, refs, parent context and direct aliases.
- `$element`, `$host`, `$event`, `$refs`, `$component`, `$parent`, loop locals and `$alias` contexts.
- `mount()`, cleanup returned by `mount()`, `unmount()` and `error()`.
- Component-owned async tasks are aborted on real unmount.

### Core directives

```text
data-kit-app
data-kit-component
data-kit-as
data-kit-scope
data-kit-ref

data-kit-text
data-kit-show
data-kit-if
data-kit-for
data-kit-key
data-kit-model

data-kit-class
data-kit-style
data-kit-bind

data-kit-persist
data-kit-<event>:<modifier>
```

`data-kit-class` prefers the HTML-friendly class map form:

```html
<div data-kit-class="
    active: open;
    'md:grid-cols-6': desktop;
    'opacity-50 pointer-events-none': saving;
"></div>
```

Object, string, array and ternary class expressions remain accepted.

`data-kit-bind` is the only general attribute binding directive:

```html
<button data-kit-bind="
    aria-expanded: open;
    data-state: status;
    disabled: saving;
    title: `State ${status}`;
"></button>
```

### Events and async actions

Supported modifiers:

```text
:window
:document
:outside
:enter
:escape
:prevent
:stop
:once
:debounce(ms)
:throttle(ms)
```

Action Programs may mutate state and call methods. Top-level Promises are tracked per binding. While pending, the actor receives `data-busy="true"` and `aria-busy="true"`; values owned by the author are restored after settlement.

### Built-in services

- `kit.task`: `run`, `latest`, `abort`, `delay` for trusted component JavaScript.
- `kit.request`: `request`, `get`, `post`, `submit`, `abort`; safe request members are granted to authored expressions.
- `kit.service(name, implementation, { expression: [...] })` for explicitly granted authored-expression access.

Task orchestration stays out of markup because `run()`/`latest()` require task callbacks. `kit.request` includes CSRF header discovery, timeout/abort composition, JSON/FormData handling and response normalization. `kit.task.latest()` implements latest-wins cancellation and component unmount automatically aborts owned tasks.

### Rendering and hydration

- Dirty-boundary scheduler: component host → local scope boundary → app root.
- Structural pass before content/form bindings.
- Keyed list rows are moved rather than rebuilt.
- MutationObserver hydrates added subtrees and reconciles changed `data-kit-*` attributes.
- Runtime-owned DOM output mutations do not trigger a render feedback loop.
- Static classes/styles/attributes remain outside the ownership of dynamic bindings.

## Browser use

```html
<html data-kit-app="main">
<body>
  <div data-kit-scope="count: 0;">
    <span data-kit-text="count">0</span>
    <button data-kit-click="count = count + 1">Increase</button>
  </div>

  <script src="/kitwork-runtime.js"></script>
</body>
</html>
```

The bundle installs `window.kit` and starts automatically unless:

```js
window.KITWORK_RUNTIME_OPTIONS = {
  autoStart: false,
  development: true
};
```

Manual start:

```js
kit.start();
```

Runnable starting points are included in `examples/counter.html` and `examples/payment-dialog.html`.

## Component example

```js
kit.component("dialog", {
  open: false,

  show() {
    this.open = true;
  },

  close() {
    this.open = false;
  },

  mount() {
    return () => {
      // release component-owned resources
    };
  }
});
```

```html
<div
  data-kit-component="dialog"
  data-kit-as="$paymentModal"
  data-kit-scope="open: false;">

  <section data-kit-if="open">
    <button data-kit-click="$component.close()">Close</button>
  </section>
</div>

<button data-kit-click="$paymentModal.show()">Open</button>
```

## Source layout

```text
src/
├── runtime.js
├── core/
│   ├── runtime.js
│   ├── records.js
│   ├── scheduler.js
│   ├── errors.js
│   └── utils.js
├── expression/
│   ├── lexer.js
│   ├── parser.js
│   ├── evaluator.js
│   ├── modes.js
│   ├── lexical-environment.js
│   └── index.js
├── component/
│   └── manager.js
├── directive/
│   ├── registry.js
│   ├── core.js
│   ├── events.js
│   ├── model.js
│   └── structural.js
└── service/
    ├── registry.js
    ├── task.js
    └── request.js
```

The source is modular. `dist/kitwork-runtime.js` is a generated single-file delivery artifact, not the source architecture.

## Build and test

```bash
npm run build
npm run check
npm run test:unit
npm run test:conformance
npm run test:browser
```

Browser tests use Python Playwright. Set `CHROMIUM_PATH` when Chromium is not discoverable automatically.

## Current validation

- Expression engine unit assertions: **49/49**.
- Lexical ownership assertions: **10/10**.
- Task/request service assertions: **16/16**.
- Shared expression conformance: **25/25**.
- Browser runtime/service/lifecycle assertions: **95/95**.
- Page errors: **0**.
- Console errors: **0**.

See `validation-report.json` and `test/browser-report.json` for machine-readable details.

## Intentionally deferred

The runtime is usable now for HTML-first interactive applications, but these product capabilities remain separate milestones:

- Drive navigation and full morph algorithm.
- Head reconciliation, history, scroll/focus restoration and prefetch.
- `data-kit-teleport` and `data-kit-transition` capability modules.
- KitJS v1 compatibility adapter and Go-side template rewriter.
- Go parser/validator execution of the shared conformance suite.
- Native platform adapters and only-used capability emission.

`data-kit-persist` and `kit.internal` already expose the ownership seams required to integrate Drive without putting navigation policy back into the always-loaded core.
