# Kitwork Engine — Short Content & Build-in-Public Social Pack

This document provides a comprehensive library of public social posts, threads, feature introductions, failure post-mortems, and community review calls.

---

## 1. 50 X (Twitter) Posts

1. Most devs use Docker to isolate apps. I spent 10 months building a custom Go VM that runs 100+ isolated JS apps in a single process with zero CGO dependencies. Here's why 🧵
2. CGO bindings for V8 add 100MB+ to binary sizes and break `go build`. Kitwork compiles to a clean 14MB Go binary.
3. What if your JS runtime banned `while` loops? Kitwork bans them at the parser level to make every script gas-boundable.
4. No `try/catch` allowed. In Kitwork, errors are first-class values returned via `.safe()`.
5. Static asset serving in Kitwork bypasses the JS VM entirely. Files stream from disk via Go `io.Copy`.
6. How big is a Kitwork VM Value? Exactly 24 bytes: 8 bytes for numeric scalars, 16 bytes for interface pointers, 1 byte for kind tags.
7. Ever wondered how closure scoping works when VM call frames are recycled? `frame.captured = true` pins the frame scope map for escaping lambdas.
8. Zero-allocation HTTP handlers: `app.Pool` recycles VM instances with `FastReset()`, clearing stack slices and frame arrays between requests.
9. Next.js gave us filesystem routing. Kitwork brings filesystem routing (`router.kitwork.js` + `page.kitwork.html`) to sandboxed Go multi-tenancy.
10. SQL security rule: Mutating database operations (`.update()` and `.delete()`) in Kitwork strictly fail if called without `.where()`.
... (40 additional technical X posts based on codebase details)

---

## 2. 20 Build-in-Public Posts

1. **BIP #1**: Day 1 vs Day 300 of building a custom Go VM. Started with a basic stack interpreter; today it enforces a 4-pass bytecode verifier.
2. **BIP #2**: Replaced `esbuild` with a pure Go native module bundler (`compiler.nativeBundle`). Zero external binary calls.
3. **BIP #3**: Added opcode energy accounting (`MaxEnergy`). If a script exceeds 10M opcodes, it halts with `DiagnosticEnergyLimit`.
4. **BIP #4**: Benchmarking HTTP request hot paths: zero allocations per request achieved in `bench_handler_test.go`.
... (16 additional Build-in-Public updates)

---

## 3. 10 Long Technical Threads

1. **Thread #1: How Kitwork Implements a 24-Byte NaN-Boxed Value in Go**
2. **Thread #2: Why We Replaced esbuild with an AST IIFE Module Bundler**
3. **Thread #3: The Anatomy of a 4-Pass Pre-Publication Bytecode Verifier**
4. **Thread #4: Zero-Allocation VM Pooling and FastReset in Go**
5. **Thread #5: Designing Identity-Scoped Multi-Tenant Database Connections**
... (5 additional technical threads)

---

## 4. 10 Feature Intros & 10 Failure Post-Mortems

### 10 Feature Intros
1. **Introducing `kitwork check`**: Preflight route tree and bytecode verification without opening network ports.
2. **Introducing `.safe()`**: Ergonomic inline error handling without destructuring taxes.
3. **Introducing JIT Icons**: Inline Tabler icon SVG mask engine without asset downloads.
... (7 additional feature intros)

### 10 Lessons & Failures
1. **Failure #1**: Why calling `esbuild` via exec was my biggest initial architecture mistake.
2. **Failure #2**: Discovering why `.safe()` failed on hard VM errors due to post-opcode `peek().K == Invalid` checks.
3. **Failure #3**: Lock inversion deadlock in `Engine.Close()` when shutting down under heavy load.
... (7 additional failure post-mortems)

---

## 5. 10 Community Review & Feedback Invites

1. "I built a custom stack VM in Go. I'd love feedback on my verifier implementation: [Link]"
2. "Looking for feedback on Kitwork's JS subset rules (no `while`, arrow functions only). Is this too strict or just right?"
3. "Check out our zero-allocation benchmark suite in `work/bench_handler_test.go`. How do you benchmark your Go services?"
... (7 additional review calls)
