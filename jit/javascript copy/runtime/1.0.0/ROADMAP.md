# Post-M1–M5 Capability Roadmap

The unified core is complete through Drive integration. Future work extends the same runtime; it must not create another kernel.

## Engine parity

- Run shared expression fixtures from Go.
- Add Go SSR scope serializer fixtures.
- Add template/compiler diagnostics for removed legacy syntax.

## Optional capabilities

- Teleport with logical ownership.
- Transition coordination for structural unmount.
- Live/SSE capability with owner-based cleanup.
- Remember persistence policy.
- Remote component loader and cache.
- Native browser/desktop/mobile adapters.

## Tooling

- Formatter for Named Map and Class Map syntax.
- Linter and source-span diagnostics.
- Runtime inspector panel.
- Bundle capability analysis and only-used emission.

All capabilities must register through the existing service/directive/lifecycle seams and use the same `AppRecord` ownership model.
