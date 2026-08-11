# Migration from KitJS Runtime v2

This runtime is deliberately not a compatibility monolith. Migrate syntax once in the Go compiler/template layer or load a separate compatibility adapter during transition.

| KitJS v2 / legacy | Runtime Next |
| :--- | :--- |
| `window.kitwork` | `window.kit` |
| `data-kitwork-*` source/IR | `data-kit-*` source only |
| Public `kit.compile()` / `kit.run()` | Private AST; no public IR ABI |
| `$el` | `$element` |
| `$root` | `$host` when the intended meaning is component host |
| `data-kit-component="dialog=$modal"` | `data-kit-component="dialog" data-kit-as="$modal"` |
| `data-kit-alias="$modal"` | `data-kit-as="$modal"` |
| `data-kit-scope="count = 0"` | `data-kit-scope="count: 0;"` |
| Outer object scope wrapper | Named map without outer `{}` |
| `data-kit-action` | `data-kit-<event>` |
| `data-kit-away` | `data-kit-click:outside` |
| `data-kit-escape` | `data-kit-keydown:escape:document` |
| `data-kit-guard="prevent stop"` | `:prevent:stop` modifiers |
| `data-kit-debounce="300"` | `:debounce(300)` |
| `data-kit-data` / `data-kit-aria` / `data-kit-attribute` | `data-kit-bind` |
| `data-kit-hidden` | `data-kit-show="!hidden"` |
| Multiple scope forms / inline lambdas | One named-map state grammar; methods live in component JavaScript |

## Recommended migration order

1. Compile templates to the new attribute syntax while keeping the existing runtime.
2. Replace the expression engine and run shared fixtures on Go and JavaScript.
3. Switch core directives/components to Runtime Next.
4. Port Drive/morph through the new lifecycle seams.
5. Remove the compatibility adapter only after production templates no longer emit legacy syntax.
