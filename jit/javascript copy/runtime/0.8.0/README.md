# Kitwork Client Runtime Next — Milestone 1: Expression Engine

> **Status:** Implemented and tested  
> **Specification:** `0.7.0-draft`  
> **Scope:** Expression compilation, evaluation, and lexical ownership adapter  
> **Not a second runtime:** this package is the first replacement module for the existing runtime.

## 1. Why development starts here

The current reference kernel already points in the right direction: seven parser modes, zero-eval expressions, runtime contexts, lexical ownership, scope seeding, directives, events, and Promise observation. The uploaded source, however, ends while building the `compile()` cache key, so the first implementation milestone completes and hardens that layer before any DOM refactor.

This package does **not** introduce another full client runtime. It replaces one subsystem at a time:

```text
Existing KitJS runtime
        │
        ├── replace expression compiler/evaluator first  ← this milestone
        ├── introduce AppRecord / NodeRecord ownership
        ├── migrate directives to a registry
        ├── replace full-root rendering with dirty boundaries
        └── connect existing Drive/morph behavior to the new lifecycle
```

## 2. Source layout

```text
src/
├── constants.js             Modes, blocked members, helpers
├── errors.js                Stable expression diagnostics
├── source-scanner.js        Quote/template/nesting-aware source scanner
├── lexer.js                 Tokenizer with source positions
├── parser.js                Binding and Action AST parser
├── modes.js                 Named Map, Class, Writable Path, Identity, Iterator
├── evaluator.js             Closed evaluator, budget, calls, assignment
├── object-environment.js    Small object-backed test environment
├── lexical-environment.js   Runtime lexical/component/app ownership adapter
├── index.js                 Engine factory
└── expression.js            Compatibility entry to `index.js`

dist/
└── kitwork-expression.js    Single-file review/reference bundle

test/
├── expression.test.js
├── lexical-environment.test.js
├── conformance.json         Shared observable fixtures for JavaScript and Go
├── run-conformance.js
└── run-all.sh
```

`dist/kitwork-expression.js` exists only for review and browser experiments. Production source should compose the modular files.

## 3. Seven parser modes

| Mode | Canonical input |
|---|---|
| `named-map` | `aria-expanded: open; --progress: progress;` |
| `binding` | `` `Hello ${user?.name ?? 'Guest'}` `` |
| `class-value` | `active: open; 'md:grid-cols-6': desktop;` or an object/string/array expression |
| `action` | `saving = true; $component.save();` |
| `writable-path` | `form.name` |
| `identity` | `$paymentModal` |
| `iterator` | `$item, $index of items` |

## 4. Canonical usage

```js
const {
  MODES,
  createExpressionEngine
} = require("./src/index.js");

const {
  createLexicalEnvironment
} = require("./src/lexical-environment.js");

const expression = createExpressionEngine();

const environment = createLexicalEnvironment({
  contexts: {
    $element: button,
    $component: component
  },
  localScopes: [
    { scope: localState, boundary: scopeElement }
  ],
  component,
  componentBoundary: componentHost,
  appScope,
  appBoundary: appRoot,
  aliases,
  kit: publicKitSurface,
  onDirty(boundary) {
    scheduler.invalidate(boundary);
  }
});

const compiled = expression.compile(
  MODES.ACTION,
  "saving = true; $component.save();"
);

const result = expression.execute(compiled, environment);
for (const promise of result.effects) {
  promise.finally(() => scheduler.invalidate(componentHost));
}
```

The AST is private. Runtime code stores the returned `compiled` object but must not serialize or expose its AST shape.

## 5. Improvements made directly from the uploaded draft

- `?.` is preserved in the AST and has distinct semantics from `.`.
- Required delimiters use `expect()` rather than silently continuing.
- Number parsing rejects malformed values such as `1e` and `1.2.3`.
- Template interpolation handles quotes, braces, and nested templates.
- Binding mode rejects assignment.
- Action mode supports semicolon-separated programs.
- Named maps split only at top-level delimiters.
- `aria-expanded` and `data-state` do not require quotes.
- CSS custom properties such as `--progress` do not require quotes.
- Class Map is canonical while object/string/array/ternary expressions remain accepted.
- Writable paths are validated before model assignment.
- Dangerous members are blocked for reads, calls, writes, and object/map keys.
- Evaluation budget and call-depth limits are enforced.
- Lexical write-to-owner behavior is isolated behind an Environment contract.
- Missing identifiers resolve to `undefined`, not numeric `0`.

## 6. Lexical ownership order

The supplied runtime adapter resolves names in this order:

```text
reserved contexts
→ direct component aliases
→ loop frames
→ nearest local scope
→ parent local scopes in the current component
→ current component instance
→ app/root scope
→ undefined
```

Writes follow ownership:

```text
nearest local scope owning the key
→ current component owning the key
→ app scope owning the key
→ otherwise nearest writable local/component/app owner
```

The adapter calls `onDirty(boundary, mutation)` whenever authored code mutates state. This is the seam for the future dirty-boundary scheduler.

## 7. Security contract implemented

- No `eval`.
- No `new Function`.
- No global fallback.
- No loose equality.
- No arrow functions or declarations in markup.
- No assignment in bindings or templates.
- Blocked member paths include `constructor`, `prototype`, `__proto__`, DOM-to-window escape paths, and global objects.
- `$element`, `$host`, `$event`, `$refs`, `$parent`, `$index`, and `kit` cannot be mutation roots.
- Custom runtime environments can impose stricter member-write policy.

## 8. Validation

```text
Expression unit tests:       49 / 49
Lexical ownership tests:     10 / 10
Shared conformance fixtures: 25 / 25
Total checks:                84 / 84
Syntax checks:               passed
```

Run:

```bash
./test/run-all.sh
```

## 9. Integration sequence into Kitwork

### Step 1 — land this package

Place the modular files at:

```text
engine/jit/javascript/expression/
```

Keep the current expression engine active initially.

### Step 2 — instantiate privately

Inside the existing kernel closure:

```js
const expression = createExpressionEngine();
```

Do not add public `kit.compile()` or `kit.run()`.

### Step 3 — add a runtime Environment adapter

Build it from the future records:

```text
AppRecord
NodeRecord
ComponentRecord
BindingRecord
```

The provided `createLexicalEnvironment()` already defines the observable lookup/write contract.

### Step 4 — migrate one directive at a time

Recommended order:

```text
text → show → style → bind → class → model → event actions → if → for
```

For each directive:

```text
legacy behavior test
→ new compiler/evaluator path
→ parity test
→ remove the legacy branch
```

### Step 5 — connect Go

Port `test/conformance.json` to a Go runner. JavaScript and Go must agree on results and error codes, not AST shape.

## 10. Next milestone

**Milestone 2: Runtime Ownership Records**

```text
AppRecord
NodeRecord
ComponentRecord
BindingRecord
multi-app isolation foundation
component host scope seeding
alias/ref lifecycle
subtree hydration and deferred cleanup
```

Drive is not rewritten from scratch. Existing KitJS Drive/morph behavior will be ported after lifecycle ownership is stable.
