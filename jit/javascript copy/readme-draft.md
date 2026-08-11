# KitJS

> **Status:** Architecture Draft 1.0 — Implementation Baseline  
> **Specification Version:** `1.0.0-draft`  
> **Canonical Runtime Global:** `kit`  
> **Canonical Directive Prefix:** `data-kit-*`  
> **Current Incubator Location:** `engine/jit/javascript/`  
> **Standalone Target:** `@kitwork/kitjs` and a CDN-distributed `kit.js`

> **“The page already exists. KitJS makes it alive.”**

KitJS is a small, HTML-first JavaScript runtime for server-rendered websites, browser applications, desktop and mobile WebViews, Chrome extensions, and server-controlled interfaces.

KitJS is designed to work as both:

1. **A standalone JavaScript runtime** that anyone can load from a CDN.
2. **The interface runtime of Kitwork Engine**, where the server can validate expressions, prerender HTML, extract CSS classes, select used components and services, and emit only the JavaScript required by each client.

KitJS does not depend on Kitwork Engine. Kitwork Engine may analyze, embed, optimize, or generate KitJS artifacts.

---

## 1. Core Philosophy

KitJS follows five principles:

1. **HTML First** — the server or author produces useful HTML before JavaScript runs.
2. **Component First** — components own reactive state, behavior, DOM lifecycle, and reusable interface logic.
3. **One `kit` Namespace** — runtime APIs and shared low-level services use the same public root.
4. **Closed Expressions** — authored directives run through a private zero-eval expression engine.
5. **Capabilities Stay Optional** — Drive, live updates, native APIs, editors, charts, maps, and other specialized features are packages rather than mandatory core code.

The normal data flow is:

```text
Server-rendered HTML / initial data
                ↓
          data-kit-scope
                ↓
     component or lexical state
                ↓
      methods and event actions
                ↓
 text / class / style / bind / model
                ↓
               DOM
```

---

## 2. The Single `kit` Model

KitJS exposes one global object:

```js
globalThis.kit
```

In browsers:

```js
window.kit === globalThis.kit
```

`kit` has two public responsibilities:

```text
kit
├── Runtime API
│   ├── component()
│   ├── directive()
│   ├── use()
│   ├── configure()
│   ├── start()
│   └── destroy()
│
└── Shared low-level services
    ├── storage
    ├── request
    ├── clipboard
    ├── display
    ├── files
    ├── camera
    ├── bridge
    └── other platform capabilities
```

There is no required second global such as:

```text
Kit
app
ctx
runtime
```

A component named `app` and an alias named `$app` remain ordinary component names. They do not receive special runtime semantics.

```html
<html data-kit-component="app" data-kit-as="$app">
```

The markup above only means:

1. Mount the component definition named `app` on `<html>`.
2. Expose that component instance under the alias `$app`.

It does not create a second global application object.

---

## 3. Trusted JavaScript vs. Authored HTML

KitJS deliberately separates trusted JavaScript from authored directive expressions.

### 3.1 Trusted JavaScript may use `kit`

```js
kit.component("account", {
    user: null,

    async load() {
        this.user = await kit.request.get("/account");
    }
});
```

### 3.2 HTML expressions may not use `kit`

This is not supported:

```html
<button data-kit-click="kit.storage.clear()">
    Clear
</button>
```

HTML invokes component methods or named component aliases instead:

```html
<div data-kit-component="account" data-kit-as="$account">
    <button data-kit-click="load()">Load</button>
</div>

<button data-kit-click="$account.logout()">
    Logout
</button>
```

This rule keeps markup independent from infrastructure and prevents authored HTML from calling runtime control APIs or platform services directly.

---

## 4. Quick Start

```html
<!doctype html>
<html>
<head>
    <meta charset="utf-8">
    <title>KitJS Counter</title>

    <script src="https://cdn.kitwork.io/kit/1.0.0/kit.js" defer></script>

    <script>
    document.addEventListener("DOMContentLoaded", function () {
        kit.component("counter", {
            count: 0,

            increment() {
                this.count += 1;
            }
        });
    });
    </script>
</head>
<body>
    <div data-kit-component="counter">
        <button data-kit-click="increment()">
            Count: <span data-kit-text="count">0</span>
        </button>
    </div>
</body>
</html>
```

The component method uses normal JavaScript `this` for its own instance state:

```text
this.count       → local component state
kit.storage      → shared low-level service
$account         → named component instance in markup
```

---

# 5. Services

Services are plain JavaScript objects attached to `kit`.

They are intended for low-level shared capabilities that do not need a DOM host or direct HTML binding.

Typical services include:

```text
kit.storage
kit.request
kit.clipboard
kit.display
kit.platform
kit.bridge
kit.files
kit.permissions
kit.camera
kit.location
kit.notification
kit.events
```

Reactive application features such as theme, account, profile, cart, sidebar, or dialog should normally be components, not services.

## 5.1 Canonical plain-object service

```js
// ============================================================================
// KitJS Service: Storage
// ============================================================================

(function (global) {
    "use strict";

    var kit = global.kit = global.kit || Object.create(null);

    if (kit.storage) return;

    kit.storage = {
        prefix: "kit:",

        async get(key, fallback) {
            if (typeof localStorage === "undefined") {
                return fallback;
            }

            var raw = localStorage.getItem(this.prefix + key);

            if (raw === null) {
                return fallback;
            }

            try {
                return JSON.parse(raw);
            } catch (_) {
                return raw;
            }
        },

        async set(key, value) {
            if (typeof localStorage !== "undefined") {
                localStorage.setItem(
                    this.prefix + key,
                    JSON.stringify(value)
                );
            }

            return value;
        },

        async remove(key) {
            if (typeof localStorage !== "undefined") {
                localStorage.removeItem(this.prefix + key);
            }
        },

        async clear() {
            if (typeof localStorage === "undefined") return;

            var keys = [];

            for (var i = 0; i < localStorage.length; i++) {
                var key = localStorage.key(i);

                if (key && key.indexOf(this.prefix) === 0) {
                    keys.push(key);
                }
            }

            for (var j = 0; j < keys.length; j++) {
                localStorage.removeItem(keys[j]);
            }
        }
    };

})(typeof globalThis !== "undefined" ? globalThis : window);
```

This authoring style is intentionally valid:

```js
kit.storage.get("theme");
kit.storage.set("theme", "dark");
```

No provider factory, dependency injection array, or application-context wrapper is required.

## 5.2 Optional registration helper

Official packages may use a small helper when they need metadata, deduplication, version checks, or runtime-managed lifecycle:

```js
kit.service("storage", storageService);
```

The observable result remains:

```js
kit.storage === storageService
```

`kit.service()` must not turn services into a dependency-injection framework. It is only a registration helper around a plain object.

## 5.3 Service lifecycle

A service may optionally implement:

```js
kit.network = {
    start() {
        // Begin subscriptions.
    },

    destroy() {
        // Release subscriptions and resources.
    }
};
```

KitJS may call `start()` during runtime boot and `destroy()` during runtime teardown when the service was installed through a managed package or registration helper.

A self-contained CDN service may initialize itself when loaded instead.

## 5.4 Platform adapters

The same service name may use a different implementation on each platform.

```text
kit.clipboard
├── Browser       → navigator.clipboard
├── Desktop app   → native window bridge
├── Mobile app    → Swift/Kotlin bridge
├── Extension     → extension adapter
└── Cloud         → unavailable or analysis-only
```

Components always call the same API:

```js
await kit.clipboard.copy(text);
```

The component does not need to know which platform adapter is active.

## 5.5 Reserved names

Services must not replace core runtime names such as:

```text
component
directive
use
configure
start
destroy
version
runtime
```

Runtime internals such as AST caches, node records, schedulers, observers, and raw native dispatchers must remain private in module closures or `WeakMap` instances. They must not be published on `kit`.

---

# 6. Components

Components are plain JavaScript objects registered through:

```js
kit.component(name, definition);
```

Example:

```js
kit.component("account", {
    user: null,
    loading: false,
    error: null,

    async load() {
        this.loading = true;
        this.error = null;

        try {
            this.user = await kit.request.get("/account");
        } catch (error) {
            this.error = error.message;
        } finally {
            this.loading = false;
        }
    },

    async logout() {
        await kit.request.post("/logout");
        this.user = null;
    }
});
```

Markup:

```html
<div
    data-kit-component="account"
    data-kit-as="$account"
    hidden>
</div>

<header>
    <span data-kit-text="$account.user?.name ?? 'Guest'">
        Guest
    </span>

    <button
        data-kit-show="$account.user"
        data-kit-click="$account.logout()">
        Logout
    </button>
</header>
```

## 6.1 Instance state

For each component host:

1. Non-function state values are cloned into a new instance.
2. Methods and accessors retain their behavior.
3. Assignments through `this` update that component instance.
4. Reactive state writes schedule a batched render.
5. Two hosts using the same component definition never share local state accidentally.

```js
kit.component("counter", {
    count: 0,

    increment() {
        this.count += 1;
    }
});
```

## 6.2 Local methods in HTML

Inside a component subtree, bare methods resolve against the nearest owning component:

```html
<div data-kit-component="theme">
    <button data-kit-click="toggle()">
        Toggle
    </button>
</div>
```

The explicit equivalent is:

```html
<button data-kit-click="$component.toggle()">
```

The short form is canonical inside the component that owns the method.

## 6.3 Shared and headless components

A component does not need visible UI.

```html
<div
    data-kit-component="account"
    data-kit-as="$account"
    hidden>
</div>
```

A headless component may own shared reactive state and methods while other parts of the page bind to its alias.

Recommended shared components include:

```text
theme
account
cart
profile
notifications
navigation
progress
```

These remain components because they expose reactive state and behavior that HTML may observe.

## 6.4 Theme as a component

```js
kit.component("theme", {
    key: "theme",
    mode: "system",

    get resolved() {
        if (this.mode !== "system") {
            return this.mode;
        }

        return (
            typeof matchMedia === "function" &&
            matchMedia("(prefers-color-scheme: dark)").matches
        ) ? "dark" : "light";
    },

    async mount() {
        this.mode = await kit.storage.get(this.key, "system");

        if (typeof matchMedia !== "function") return;

        var media = matchMedia("(prefers-color-scheme: dark)");
        var component = this;

        function onChange() {
            if (component.mode === "system") {
                component.mode = "system";
            }
        }

        media.addEventListener("change", onChange);

        return function () {
            media.removeEventListener("change", onChange);
        };
    },

    async set(mode) {
        if (
            mode !== "light" &&
            mode !== "dark" &&
            mode !== "system"
        ) {
            mode = "system";
        }

        this.mode = mode;
        await kit.storage.set(this.key, mode);

        return this.resolved;
    },

    toggle() {
        return this.set(
            this.resolved === "dark" ? "light" : "dark"
        );
    }
});
```

```html
<html data-kit-bind="data-theme: $theme.resolved;">
<body>
    <div
        data-kit-component="theme"
        data-kit-as="$theme">

        <button data-kit-click="toggle()">
            Toggle theme
        </button>
    </div>
</body>
</html>
```

The component owns `mode`, `resolved`, and `toggle()`.

The storage service only owns persistence:

```text
$theme        → reactive theme component
kit.storage   → low-level storage service
```

## 6.5 Communication between components

Use the least coupled mechanism that fits the requirement.

| Requirement | Mechanism |
| :--- | :--- |
| Local state and behavior | Current component and bare methods |
| A specific component instance | `$alias` |
| Shared low-level capability | `kit.<service>` inside trusted JavaScript |
| Loose cross-component notification | `kit.events` service |
| DOM element within a component | `$refs` |

Direct aliases are appropriate for imperative UI control:

```html
<button data-kit-click="$paymentModal.open()">
    Open payment
</button>
```

A small event service may be used for loosely coupled messages:

```js
kit.events.emit("account:updated", user);
```

```js
kit.component("account-header", {
    user: null,

    mount() {
        var component = this;

        return kit.events.on("account:updated", function (user) {
            component.user = user;
        });
    }
});
```

---

# 7. Component Lifecycle

A component may implement:

```js
kit.component("dialog", {
    mount() {
        // Host, state and refs are ready.

        return function cleanup() {
            // Optional cleanup returned by mount().
        };
    },

    unmount() {
        // Final component cleanup.
    },

    error(error, context) {
        // Return true to mark the error as handled.
    }
});
```

## 7.1 Mount sequence

The runtime must follow these observable phases:

1. Discover component hosts and scopes.
2. Create component instances.
3. Seed component state from `data-kit-scope`.
4. Register all component aliases.
5. Collect component-local refs.
6. Run the initial render.
7. Call `mount()` from parent to child.
8. Batch any state updates produced by `mount()` or its Promise.

Aliases must be registered before the first binding pass. This allows an early binding such as:

```html
<html data-kit-bind="data-theme: $theme.resolved;">
```

to reference a component that appears later in the document.

## 7.2 Unmount sequence

1. Mark the subtree as unmounting.
2. Unmount child components first.
3. Abort owned tasks and remove event resources.
4. Call `unmount()` while `$host` and `$refs` are still available.
5. Run the cleanup returned by `mount()`.
6. Remove refs and aliases.
7. Release component and node records.

Moving a node within the same runtime document must not be treated as an unmount. Cleanup occurs only when the node is truly detached after the current mutation cycle.

---

# 8. Component Aliases

Declare an alias with:

```html
<div
    data-kit-component="dialog"
    data-kit-as="$paymentModal">
</div>
```

Use it anywhere in the same runtime root:

```html
<button data-kit-click="$paymentModal.open()">
    Open
</button>
```

Rules:

1. Alias names must begin with `$`.
2. Alias names must match `/^\$[A-Za-z][A-Za-z0-9_]*$/`.
3. Alias names must be unique in the runtime root.
4. Built-in contexts such as `$element`, `$host`, `$event`, `$refs`, `$component`, and `$parent` are reserved.
5. Aliases are removed when their component instance unmounts.
6. `$app` is not reserved. It is an ordinary alias if the author chooses it.

---

# 9. Refs

```html
<div data-kit-component="search-box">
    <input data-kit-ref="input">

    <button data-kit-click="$refs.input.focus()">
        Focus
    </button>
</div>
```

In trusted component JavaScript:

```js
kit.component("search-box", {
    mount() {
        this.$refs.input.focus();
    }
});
```

Rules:

1. Refs belong to the nearest component instance.
2. Refs do not cross into descendant component boundaries.
3. A ref name must be unique within one component instance.
4. `$refs.name` is always one element in 1.0; it does not automatically change between an element and an array.
5. Refs are removed during subtree cleanup.

---

# 10. Scope and SSR Data

`data-kit-scope` uses Named Map syntax:

```html
<div data-kit-scope="
    open: false;
    count: 0;
    user: { id: 128, name: 'Quoc' };
"></div>
```

The outer map does not use `{}`. Objects and arrays inside a value remain normal expression literals.

## 10.1 Scope on a normal element

It creates a mutable lexical scope initialized exactly once.

```html
<section data-kit-scope="selected: null;">
    ...
</section>
```

## 10.2 Scope on a component host

It seeds and overrides the component instance state:

```html
<div
    data-kit-component="dialog"
    data-kit-scope="
        open: true;
        order: { id: 128 };
    ">
</div>
```

Rules:

1. Component blueprint state is cloned per instance.
2. Host scope overrides blueprint state by top-level key.
3. Objects are not deep-merged implicitly.
4. Scope must not replace component methods, accessors, refs, aliases, or runtime metadata.
5. A collision is a development error.
6. The resulting component has one state store, not a component store plus a separate host scope.
7. Scope initializers never run again during ordinary rerenders.

## 10.3 SSR contract

Server-rendered values needed by client behavior may be seeded through scope:

```html
<div data-kit-scope="
    articleId: 918;
    liked: true;
    likeCount: 27;
"></div>
```

The server remains the canonical authority. Client scope is only a browser-side snapshot and interaction state.

KitJS 1.0 does not define `data-kit-props` or `$props`.

---

# 11. Runtime Expression Context

Authored expressions may resolve:

| Context | Meaning |
| :--- | :--- |
| Bare names | Nearest lexical/component state, getters, and methods |
| `$element` | Element that owns the directive |
| `$host` | Host element of the nearest component |
| `$event` | Native event for the current event action |
| `$refs` | Refs of the nearest component |
| `$component` | Nearest owning component instance |
| `$parent` | Parent component instance |
| `$item`, `$index` | Iterator bindings |
| `$<alias>` | Named component instance |

The expression context does not include:

```text
kit
window
globalThis
document
app
ctx
```

The absence of global fallback is mandatory.

---

# 12. Expression Engine

KitJS expressions are never executed using:

```js
eval()
new Function()
```

The pipeline is:

```text
source
  ↓
lexer
  ↓
parser selected by directive mode
  ↓
private cached AST
  ↓
closed evaluator
```

The AST is an internal implementation detail. It is not a public ABI and is not serialized into author HTML.

## 12.1 Seven parser modes

| # | Mode | Directives |
| :-: | :--- | :--- |
| 1 | Named Map | `scope`, `style`, `bind` |
| 2 | Binding Expression | `text`, `show`, `if`, `key` |
| 3 | Class Value | `class` |
| 4 | Action Program | Event directives |
| 5 | Writable Path | `model` |
| 6 | Identity Literal | `component`, `as`, `ref`, `persist` |
| 7 | Iterator | `for` |

## 12.2 Supported language features

```text
null
undefined
boolean
number
string
array
plain object
template literal
```

```text
property access
computed access
optional chaining
nullish coalescing
strict equality
arithmetic
comparison
logical operators
ternary
method calls
```

Examples:

```html
<span data-kit-text="user?.profile?.name ?? 'Guest'"></span>

<span data-kit-text="`Hello, ${user.name}`"></span>
```

## 12.3 Forbidden language features

```text
==
!=
var
let
const
function
class
for
while
new
delete
await
try/catch
arrow functions
increment/decrement
compound assignment
```

Complex logic belongs in trusted component JavaScript.

## 12.4 Security

The evaluator must block dangerous member paths including:

```text
constructor
prototype
__proto__
ownerDocument
defaultView
contentWindow
window
globalThis
top
parent
self
```

The evaluator must enforce:

1. No global fallback.
2. An evaluation-node budget.
3. A method-call depth limit.
4. Writable-target validation.
5. Assignment only in Action Program and Writable Path modes.
6. Promise rejection in render bindings.

---

# 13. Directive Reference

## 13.1 Component

```html
<div data-kit-component="dialog"></div>
```

Creates one component instance from the registered component definition.

## 13.2 Alias

```html
<div
    data-kit-component="dialog"
    data-kit-as="$dialog">
</div>
```

## 13.3 Scope

```html
<div data-kit-scope="open: false; count: 0;"></div>
```

## 13.4 Ref

```html
<input data-kit-ref="searchInput">
```

## 13.5 Text

```html
<span data-kit-text="`Hello ${user.name}`">
    Hello
</span>
```

`data-kit-text` always writes through `textContent`. It never renders raw HTML.

## 13.6 Show

```html
<section data-kit-show="open"></section>
```

Toggles `hidden` while keeping the subtree mounted.

## 13.7 If

```html
<section data-kit-if="open"></section>
```

Mounts and unmounts the subtree. Unmount performs lifecycle cleanup.

## 13.8 For and key

```html
<li
    data-kit-for="$item, $index of items"
    data-kit-key="$item.id">

    <span data-kit-text="$item.name"></span>
</li>
```

Rules:

1. Use `of`, not `in`.
2. Access item properties explicitly through `$item`.
3. Stable keys preserve DOM identity.
4. Duplicate keys are development errors.
5. Index fallback is permitted only with a development warning.

## 13.9 Model

```html
<input data-kit-model="user.name">
```

`data-kit-model` accepts only a writable path.

Form coercion:

| Control | State value |
| :--- | :--- |
| Text / textarea | String |
| Number / range | Number or `null` |
| Single checkbox | Boolean |
| Checkbox group | Array |
| Radio group | Selected value |
| Select | String |
| Select multiple | Array |
| Date/time | Canonical string |
| File | `FileList`, DOM-to-state only |

IME composition must not update state until `compositionend`.

## 13.10 Class

Class Map is the canonical HTML syntax:

```html
<div data-kit-class="
    active: open;
    loading: saving;
    'md:grid-cols-6': desktop;
    'opacity-50 pointer-events-none': saving;
"></div>
```

Keys containing `:` or whitespace use quotes.

Class Value expressions are also accepted:

```html
<div data-kit-class="open ? 'active' : 'disabled'"></div>

<div data-kit-class="{
    active: open,
    loading: saving
}"></div>

<div data-kit-class="[
    'card',
    sizeClass,
    { active: open }
]"></div>
```

The runtime computes one desired dynamic-class set before changing the DOM. It removes only classes previously owned by the directive and never removes static author classes.

Tailwind and CSS extractors should prefer literal class names. Avoid dynamic construction such as:

```html
<div data-kit-class="`bg-${color}-500`"></div>
```

## 13.11 Style

```html
<div data-kit-style="
    width: width;
    opacity: opacity;
    --progress: progress;
"></div>
```

The runtime uses `style.setProperty()` and does not add `px` automatically.

`null`, `undefined`, and `false` remove the owned property.

## 13.12 Bind

```html
<button data-kit-bind="
    aria-expanded: open;
    aria-controls: 'main-menu';
    aria-label: `Open ${title}`;
    data-state: open ? 'open' : 'closed';
    disabled: saving;
    title: title;
"></button>
```

Attribute keys containing `-` do not require quotes.

A key containing a grammar-reserved `:` uses quotes:

```html
<use data-kit-bind="'xlink:href': href;"></use>
```

Boolean serialization:

| Attribute type | `null` / `undefined` | `false` | `true` |
| :--- | :--- | :--- | :--- |
| `data-*` | Remove | `"false"` | `"true"` |
| `aria-*` | Remove | `"false"` | `"true"` |
| HTML boolean | Remove | Remove | Empty attribute |
| Ordinary | Remove | Remove | `"true"` |

`data-kit-bind` must not own:

```text
class
style
on*
srcdoc
data-kit-*
value/checked/selected when model owns them
```

Unsafe URL schemes such as `javascript:` and `vbscript:` must be rejected.

## 13.13 Ignore

```html
<div data-kit-ignore></div>
```

The subtree is opaque to KitJS:

1. Directives inside are not hydrated.
2. Mutation reconciliation does not enter the subtree.
3. Drive/morph treats the subtree as externally owned.
4. The owning component remains responsible for cleanup.

This is intended for Monaco, CodeMirror, charts, maps, rich-text editors, canvas, WebGL, and third-party widgets.

## 13.14 Cloak

```html
<div data-kit-cloak data-kit-show="ready"></div>
```

Recommended CSS:

```css
[data-kit-cloak] {
    display: none !important;
}
```

The runtime removes `data-kit-cloak` after the first successful render of its owning root.

## 13.15 Persist

```html
<div data-kit-persist="global-progress"></div>
```

When Drive is active, an old and incoming node with the same persistence key may reuse the old node. The key does not make the node immortal; if the incoming tree does not contain the key, the old node is removed normally.

---

# 14. Event Directives

Canonical syntax:

```html
<form data-kit-submit:prevent="save()"></form>

<input data-kit-keydown:enter="search()">

<div data-kit-click:outside="open = false"></div>

<button data-kit-click:once="initialize()"></button>

<input data-kit-input:debounce(300)="search()">
```

Supported modifiers:

| Modifier | Meaning |
| :--- | :--- |
| `:window` | Listen on `window` |
| `:document` | Listen on `document` |
| `:outside` | Run when the event occurs outside `$element` |
| `:self` | Run only when `event.target === $element` |
| `:enter` | Enter-key filter |
| `:escape` | Escape-key filter |
| `:prevent` | Call `preventDefault()` synchronously |
| `:stop` | Call `stopPropagation()` synchronously |
| `:once` | Consume the binding before running the action |
| `:debounce(ms)` | Wait for a quiet period |
| `:throttle(ms)` | Limit execution frequency |

Canonical processing order:

```text
resolve source
→ validate filters
→ prevent / stop
→ consume once
→ debounce / throttle
→ execute action program
→ observe returned Promises
→ invalidate render boundary
```

Invalid combinations must produce diagnostics, including:

```text
:window + :document
:outside + :window
:outside + :document
:debounce + :throttle
:enter on non-keyboard events
:outside on keyboard events
```

## 14.1 Action Program

An event may contain several action expressions separated by `;`:

```html
<button data-kit-click="
    error = null;
    saving = true;
    save();
">
    Save
</button>
```

All top-level Promises created by the action are observed.

The runtime maintains a pending counter per event binding and may expose:

```html
data-busy="true"
aria-busy="true"
```

until all observed Promises settle.

---

# 15. Rendering and Scheduling

KitJS should not rescan and rerender the entire document after every action.

Invalidation target order:

```text
nearest component boundary
→ nearest lexical scope boundary
→ runtime root
```

The scheduler must:

1. Batch invalidations in a microtask or animation frame.
2. Remove dirty child boundaries when a dirty parent already covers them.
3. Run structural directives before content directives.
4. Hydrate only newly added subtrees.
5. Defer removal cleanup to distinguish a real removal from a DOM move.

Recommended render phases:

```text
1. Structure     → if / for
2. Ownership     → components / scopes / aliases / refs
3. Content       → text / show / class / style / bind
4. Form          → model reconciliation
5. Lifecycle     → mount settlement / cloak removal / errors
```

---

# 16. Errors and Diagnostics

Error pipeline:

```text
component.error(error, context)
        ↓
kit.onError(error, context)
        ↓
kitwork:error CustomEvent
        ↓
development console diagnostics
```

Recommended error codes include:

```text
KIT_PARSE_INVALID_TOKEN
KIT_PARSE_UNEXPECTED_EOF
KIT_UNKNOWN_DIRECTIVE
KIT_INVALID_MODIFIER
KIT_INVALID_DIRECTIVE_COMBINATION
KIT_COMPONENT_MISSING
KIT_COMPONENT_STATE_COLLISION
KIT_DUPLICATE_ALIAS
KIT_DUPLICATE_REF
KIT_DUPLICATE_KEY
KIT_DUPLICATE_PERSIST_KEY
KIT_MODEL_INVALID_PATH
KIT_ASYNC_BINDING
KIT_BIND_UNSAFE_ATTRIBUTE
KIT_BIND_OWNERSHIP_CONFLICT
KIT_EVALUATION_BUDGET
KIT_CALL_DEPTH
KIT_PACKAGE_INCOMPATIBLE
KIT_CAPABILITY_UNSUPPORTED
```

Each diagnostic should carry:

```js
{
    code,
    message,
    directive,
    source,
    element,
    component,
    phase,
    cause
}
```

---

# 17. Packages and CDN Components

KitJS core must remain small. Specialized components are distributed as optional packages.

Examples:

```text
code editor
rich-text editor
chart
map
virtual list
data grid
file uploader
media player
account feature
profile feature
headless dialog/menu/tabs
```

## 17.1 Direct CDN script

A simple package may register itself:

```html
<script
    type="module"
    src="https://cdn.kitwork.io/components/chart/1.0.0/index.js">
</script>
```

```js
kit.component("chart", {
    mount() {
        // Mount specialized library into this.$refs.mount.
    }
});
```

## 17.2 Package contract

```js
export default {
    name: "@kitjs/chart",
    version: "1.0.0",
    kit: "^1.0.0",
    services: ["request"],

    install(kit) {
        kit.component("chart", chartComponent);
    }
};
```

Install through:

```js
import chartPackage from "https://cdn.kitwork.io/components/chart/1.0.0/index.js";

kit.use(chartPackage);
```

`kit.use()` should:

1. Validate package name and version.
2. Check runtime compatibility.
3. Deduplicate installation.
4. Record service and asset requirements.
5. Call `install(kit)` once.
6. Hydrate hosts waiting for registered components.
7. Report package errors through the normal error pipeline.

A CDN component package must not embed a second KitJS runtime.

## 17.3 Specialized DOM ownership

```html
<div data-kit-component="code-editor">
    <div data-kit-ref="mount" data-kit-ignore></div>
</div>
```

Third-party DOM stays inside the ignored mount region while KitJS owns the outer component lifecycle.

## 17.4 Trust boundary

Imported JavaScript packages are trusted code with the authority of the page.

Untrusted packages should run in an isolated iframe or another security realm. The zero-eval expression engine does not sandbox arbitrary CDN JavaScript.

---

# 18. Browser, Desktop, Mobile, Extension, and Cloud

## 18.1 Browser

Load the normal DOM runtime:

```html
<script src="https://cdn.kitwork.io/kit/1.0.0/kit.js"></script>
```

## 18.2 Desktop and mobile apps

Desktop and mobile WebView applications use the same HTML directives and components.

Platform services are connected to a native bridge:

```text
component
  ↓
kit.camera / kit.files / kit.clipboard
  ↓
native bridge adapter
  ↓
Go / Swift / Kotlin / Rust / C# host
```

## 18.3 Chrome extensions

Extension pages use the same DOM runtime with extension-specific service adapters.

## 18.4 Cloud and server tools

Cloud environments do not need to emulate a browser DOM.

A server/analyzer build may provide:

```text
expression validation
HTML directive scanning
component/service dependency extraction
CSS class extraction
pure-expression prerendering
client manifest generation
```

Platform services such as clipboard, camera, or fullscreen are not executed on the server.

---

# 19. Kitwork Engine Integration

KitJS remains independent from Kitwork Engine.

Kitwork Engine may use the KitJS contract to:

1. Scan `data-kit-*` directives.
2. Validate expressions with matching parser modes.
3. Seed `data-kit-scope` from SSR data.
4. Prerender pure text/show/class/style/bind results.
5. Extract static dynamic-class candidates for CSS JIT.
6. Detect used components and their package dependencies.
7. Detect trusted JavaScript service usage such as `kit.storage` or `kit.request`.
8. Select browser/native service adapters.
9. Emit only the runtime, services, components, and capabilities required by the page.
10. Produce the same standalone `kit.js` contract as the CDN distribution.

The dependency direction is:

```text
KitJS does not import Kitwork Engine.
Kitwork Engine may embed, analyze, optimize, or generate KitJS.
```

Private AST or IR formats may exist internally for server/client parity, but they are not author APIs or compatibility contracts.

---

# 20. Suggested Source Layout

```text
kitjs/
├── src/
│   ├── core/
│   │   ├── runtime.js
│   │   ├── components.js
│   │   ├── packages.js
│   │   ├── lifecycle.js
│   │   ├── scheduler.js
│   │   └── diagnostics.js
│   │
│   ├── expression/
│   │   ├── lexer.js
│   │   ├── parser.js
│   │   ├── evaluator.js
│   │   ├── modes.js
│   │   └── cache.js
│   │
│   ├── dom/
│   │   ├── directives.js
│   │   ├── events.js
│   │   ├── model.js
│   │   ├── structure.js
│   │   └── observer.js
│   │
│   ├── server/
│   │   ├── scanner.js
│   │   ├── analyzer.js
│   │   └── extractor.js
│   │
│   └── index.js
│
├── services/
│   ├── storage/
│   ├── request/
│   ├── clipboard/
│   ├── platform/
│   └── bridge/
│
├── capabilities/
│   ├── drive/
│   ├── live/
│   ├── remember/
│   ├── teleport/
│   └── transition/
│
├── components/
│   └── official/
│
├── dist/
│   ├── kit.js
│   ├── kit.core.js
│   └── kit.server.js
│
└── test/
    ├── conformance/
    ├── browser/
    ├── lifecycle/
    └── performance/
```

The public browser experience remains one file:

```html
<script src="https://cdn.kitwork.io/kit.js"></script>
```

Modular source does not require a modular user experience.

---

# 21. Core vs. Optional Capabilities

## 21.1 Core

```text
expression engine
scope and component state
component lifecycle
aliases and refs
text/show/if/for/key/model
class/style/bind
event delegation and modifiers
scheduler and cleanup
package/component registry
```

## 21.2 Optional capabilities

```text
Drive and DOM morph
request helpers
task orchestration
live/SSE/WebSocket
remember/persistence policy
teleport
transition
remote component loader
native platform adapters
virtualization
rich editors
```

A standalone distribution may bundle selected common services, but the architecture must allow Kitwork JIT or a custom build to omit unused capabilities.

---

# 22. Explicit Non-Goals for 1.0

KitJS 1.0 does not add:

```text
A second global App or Kit object
kit or app access in HTML expressions
data-kit-props / $props
data-kit-watch
data-kit-effect
data-kit-html
data-kit-hidden
data-kit-action
public AST or serialized IR
automatic loop property unwrapping
automatic ref arrays
mandatory dependency injection
mandatory component factories
virtual DOM
client-side HTML template ownership
```

The absence of these features is intentional.

---

# 23. Migration from Earlier Runtime Drafts

| Earlier form | KitJS 1.0 form |
| :--- | :--- |
| `window.kitwork` | `window.kit` |
| `$el` | `$element` |
| `$root` | `$host` when referring to component host |
| `data-kit-component="dialog=$modal"` | `data-kit-component="dialog" data-kit-as="$modal"` |
| `data-kit-action="..."` | `data-kit-<event>="..."` |
| `data-kit-away="close()"` | `data-kit-click:outside="close()"` |
| `data-kit-guard="prevent stop"` | `:prevent:stop` |
| `data-kit-debounce="300"` | `:debounce(300)` |
| `data-kit-data`, `data-kit-aria`, `data-kit-attribute` | `data-kit-bind` |
| Generic `data-kit-*` mirroring | Explicit `data-kit-bind` |
| Service calls in HTML | Component methods in HTML; service calls in trusted JavaScript |
| `app.storage` / `Kit.storage` | `kit.storage` |
| Theme as `kit.theme` service and `$theme` state | Theme as one shared component `$theme`; persistence remains `kit.storage` |

A migration build may rewrite legacy syntax, but the 1.0 core should not permanently carry every historical parser branch.

---

# 24. Implementation Invariants

An implementation claiming compatibility with this specification must preserve these properties:

1. One canonical global root: `kit`.
2. No `eval()` or `new Function()` for directive expressions.
3. No global fallback from authored expressions.
4. `kit` is not exposed to authored expressions.
5. Component state is isolated per instance.
6. Scope initializers run once.
7. Aliases are registered before the first binding pass.
8. Refs are component-local.
9. Static classes and attributes are not removed by unrelated directives.
10. Structural removal runs lifecycle cleanup.
11. DOM moves are not mistaken for unmounts.
12. Async actions use pending counters rather than one fragile boolean.
13. Unknown directives and invalid combinations produce diagnostics.
14. Third-party ignored subtrees remain opaque.
15. Kitwork JIT and standalone KitJS use the same author-facing semantics.

---

# 25. Conformance and Freeze Gate

Before declaring KitJS 1.0 stable, the project should pass shared coverage for:

```text
expression grammar
scope ownership
component state isolation
aliases and refs
class map and class values
bind boolean semantics
form model controls
event modifiers
Promise observation
if/for keyed identity
mount/unmount cleanup
ignored DOM ownership
Drive/morph preservation
service adapters
package loading
security cases
memory regression
performance budgets
```

For Kitwork integration, Go and JavaScript implementations should share observable conformance fixtures even when their internal AST or IR representations differ.

---

# 26. Complete Example

```js
// storage.js
(function (global) {
    "use strict";

    var kit = global.kit = global.kit || Object.create(null);

    if (!kit.storage) {
        kit.storage = {
            async get(key, fallback) {
                var raw = localStorage.getItem("kit:" + key);
                if (raw === null) return fallback;

                try {
                    return JSON.parse(raw);
                } catch (_) {
                    return raw;
                }
            },

            async set(key, value) {
                localStorage.setItem(
                    "kit:" + key,
                    JSON.stringify(value)
                );

                return value;
            }
        };
    }
})(globalThis);

// components.js
kit.component("theme", {
    mode: "system",

    get resolved() {
        if (this.mode !== "system") return this.mode;

        return matchMedia("(prefers-color-scheme: dark)").matches
            ? "dark"
            : "light";
    },

    async mount() {
        this.mode = await kit.storage.get("theme", "system");
    },

    async toggle() {
        this.mode = this.resolved === "dark" ? "light" : "dark";
        await kit.storage.set("theme", this.mode);
    }
});

kit.component("account", {
    user: null,
    loading: false,

    async load() {
        this.loading = true;

        try {
            this.user = await kit.request.get("/account");
        } finally {
            this.loading = false;
        }
    }
});

kit.start();
```

```html
<!doctype html>
<html
    data-kit-bind="
        data-theme: $theme.resolved;
    ">
<head>
    <meta charset="utf-8">
    <title>KitJS Application</title>

    <style>
        [data-kit-cloak] {
            display: none !important;
        }
    </style>
</head>
<body data-kit-cloak>
    <div
        data-kit-component="theme"
        data-kit-as="$theme">

        <button data-kit-click="toggle()">
            Toggle theme
        </button>
    </div>

    <div
        data-kit-component="account"
        data-kit-as="$account"
        hidden>
    </div>

    <header>
        <span data-kit-text="$account.user?.name ?? 'Guest'">
            Guest
        </span>

        <button
            data-kit-show="!$account.user"
            data-kit-click="$account.load()"
            data-kit-bind="
                disabled: $account.loading;
                aria-busy: $account.loading;
            ">
            Load account
        </button>
    </header>
</body>
</html>
```

---

# 27. Final Model

```text
kit
= runtime API
+ shared low-level services

component
= reactive state
+ methods
+ DOM lifecycle

$alias
= a named component instance

HTML
= component state/methods
+ runtime DOM contexts
+ aliases
= never direct runtime/service access
```

The intended developer experience is:

```js
kit.component("account", {
    async load() {
        this.user = await kit.request.get("/account");
    }
});
```

```html
<div data-kit-component="account" data-kit-as="$account">
    <button data-kit-click="load()">Load</button>
</div>
```

KitJS should remain simple on the outside, extensible through services and component packages, and optimizable by Kitwork Engine without becoming dependent on Kitwork itself.
