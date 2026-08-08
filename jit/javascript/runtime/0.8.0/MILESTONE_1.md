# Milestone 1 Completion Record

## Completed

- Replaced the cut-off `compile()` draft with a complete mode-aware compiler.
- Corrected optional chaining semantics.
- Added strict delimiter diagnostics.
- Added robust number, string, and nested-template scanning.
- Implemented all seven parser modes.
- Implemented a closed evaluator with mutation/effect reporting.
- Implemented the lexical/component/app Environment adapter.
- Added shared JSON fixtures intended for both JavaScript and Go.
- Preserved a single-file review bundle while making production source modular.

## Deliberately not included

- DOM directive registry.
- AppRecord / NodeRecord / ComponentRecord / BindingRecord.
- Render scheduler.
- MutationObserver lifecycle.
- `kit.task` and `kit.request`.
- Drive and morph.
- Legacy `data-kitwork-*` compatibility.

## Merge gate

This milestone is ready to land when:

1. The package is placed under `engine/jit/javascript/expression/`.
2. CI runs `test/run-all.sh`.
3. The Go conformance runner consumes `test/conformance.json`.
4. No public AST or `compile/run` API is exposed from `window.kit`.
