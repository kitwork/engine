# Contributing to Kitwork Engine

Kitwork is a multi-tenant engine: one process runs many people's code at once, and a mistake here is
not a bug in one site but a hole in everyone's. The rules below exist for that reason, not for
ceremony.

Read `docs/STABILITY.md` before anything else. It is the contract this codebase is held to, and most
review comments are just a pointer back into it.

---

## Before you open a pull request

**1. Licence.** The engine is AGPL-3.0 with a commercial licence alongside it. That second half only
works if one party holds the rights to the whole codebase, so a first contribution needs the
agreement in `CLA.md`. You keep your copyright; you grant the right to relicense. Typo and
formatting fixes are exempt.

**2. Run everything.** `docs/STABILITY.md` §6 requires all four, in this order:

```bash
go build ./... && go vet ./... && go test ./... && go test -race ./...
```

Then, from the host repository, the preflight over real applications:

```bash
go run . check
```

CI runs the same commands. Running them first only saves you the round trip.

**3. Dependencies.** The engine is standard-library-only by design — one binary, no supply chain.
A new `go.mod` entry needs a discussion before code, not after.

---

## The rules that actually catch people

### A change is not finished until an invariant has a test on the production path

`docs/STABILITY.md` opens with this and means it. A unit test that asserts an adapter is non-nil
proves nothing; contract tests execute real `.kitwork.js` fixtures through the real compiler, VM,
router and response path. Isolation claims must be tested concurrently, not sequentially.

### Your test must be able to fail

Write the test, watch it pass, then **break the code it covers and watch it go red.** A test that
stays green with its subject disabled is not a test, and this codebase has caught several.

Two habits that follow from it:

- **Assert the reason, not the symptom.** "the page returned 200" survives almost any bug; "the
  handler logged the name it was given" can only be produced by the real value arriving.
- **Keep a control case.** If you assert that a duplicate is dropped, also assert that a
  non-duplicate is kept. Otherwise "one row" also passes when nothing is stored at all.

### Changing an opcode touches five things in one change

From `docs/BYTECODE.md`:

1. Update `InstructionSpec` — the single source of truth for name, operand widths, stack effect and
   energy. Do not add a parallel table.
2. Implement it once, in `runtime.execute`. Not in a wrapper, not in a second switch.
3. Extend the verifier's operand and control-flow rules.
4. Add valid, malformed, and energy regression tests.
5. Run build, test, vet, race and `kitwork check`.

Opcode numbers are append-only. A removed instruction keeps its slot reserved forever, so old
bytecode can never be reinterpreted by a new opcode.

Nothing may bypass `runtime.NewProgram`. Bytecode is not executable because it is a byte slice; it is
executable because it was copied, verified and fingerprinted.

### Changing a public JS API touches three places in one commit

Anything reachable through `import { … } from "kitwork"` is public API. Renaming or removing one
requires, in the **same commit**:

1. the engine change;
2. `kitwork.d.ts` in the host repository;
3. the matching skill under `.claude/skills/`.

This is not tidiness. When the engine renamed its database surface and the type definitions were
left behind, the definitions kept advertising a method that no longer existed — and the sites that
followed them rendered blank pages with no error anywhere. The engine's own tests were green the
whole time, because the engine was fine. Only the sites were broken, and nothing was watching them.

`docs/STABILITY.md` §5 also asks for a deprecation period before removing a public method.

### Silence is a bug

A wrong call that returns `null` and renders an empty page costs more to find than a crash. When you
add a failure path, give it a `runtime.Diagnostic` with a stable code and a real source location —
the same treatment `ENERGY_LIMIT` and `STACK_OVERFLOW` get. The language removed `try`/`catch` to
have one visible error path; a new quiet one takes that back.

---

## Style

**Comments explain the decision, not the mechanism.** The code already says what it does. Say why it
is this way, what the alternative was, and what breaks if someone innocently changes it back. When a
line is load-bearing, say so — several comments here record that the author verified it by removing
the line and watching a test fail.

**Commit messages state the change and its reason**, in the imperative, with the consequence made
concrete. Read `git log` for the register — a message like *"safe() carries .ok — closes a silent
failure the habit walks into"* is the house style. `work 82 - refactor VM` is not.

**No abbreviations in names.** `button`, not `btn`. `kitwork`, not `kw`. The established `jit`,
`jitcss`, `jitjs` are the exception, because they are names rather than shortenings.

---

## Where things live

`docs/` holds the contracts: `STABILITY.md` (invariants), `BYTECODE.md` (instruction format),
`RUNTIME_ARCHITECTURE.md`, `ARCHITECTURE.md` (the application RFC — mostly not implemented).

The host repository holds `kitwork.d.ts`, the skills, and the specifications for durable background
work (`scheduler.md`, `queue.md`).

---

## Reporting a security issue

Do not open a public issue. Write to support@kitwork.org. A tenant-escape, a way past the energy
ceiling, or a path out of an application's own directory is the class that matters most here.
