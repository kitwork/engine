# Migration to the Unified Kitwork Runtime

Do not load the legacy KitJS kernel and the unified runtime together.

## Author syntax

| Legacy | Unified runtime |
|---|---|
| `data-kitwork-*` source/IR | `data-kit-*` source only |
| `$el` | `$element` |
| `$root` | `$host` for component host semantics |
| `data-kit-alias="$modal"` | `data-kit-as="$modal"` |
| `data-kit-action` | `data-kit-<event>` |
| `data-kit-away` | `data-kit-click:outside` |
| `data-kit-escape` | `data-kit-keydown:escape:document` |
| `data-kit-guard="prevent stop"` | `:prevent:stop` modifiers |
| `data-kit-debounce="300"` | `:debounce(300)` |
| `data-kit-data` / `data-kit-aria` / `data-kit-attribute` | `data-kit-bind` |
| `data-kit-scope="count = 0"` | `data-kit-scope="count: 0;"` |
| public `kit.compile/run` | removed; AST is private |

## Drive

Legacy hydration/navigation markers should become an explicit app root:

```html
<main data-kit-app="main" data-kit-drive>
```

Use `data-kit-drive="false"` or `data-kit-no-drive` to opt out for a link/form subtree.

## Recommended migration order

1. Switch aliases and runtime contexts.
2. Convert scope declarations to Named Map syntax.
3. Replace attribute directives with `data-kit-bind`.
4. Replace legacy event companion attributes with modifiers.
5. Add `data-kit-app`; enable `data-kit-drive` only after page responses contain the matching app root.
6. Remove the legacy runtime bundle.
