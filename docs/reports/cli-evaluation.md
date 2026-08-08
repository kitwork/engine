# Kitwork Engine — CLI Evaluation & Specification

This document audits the current Command Line Interface (CLI) capabilities of Kitwork and defines the minimal essential command set for the v1.0.0-RC1 release.

---

## 1. Current CLI State & Audit

```text
[ Existing CLI Commands ]
  ├── go run .             # Boots HTTP server (main.go -> engine.Run())
  └── go run . check       # Runs preflight static validation (core.Engine.Check())
```

### Audit Findings
- **Strengths**: `go run . check` is extremely fast and effective at validating syntax, native imports, route trees, and bytecode verifier round-trips without opening network ports or starting schedulers.
- **Weaknesses**: Forcing developers to pass `ALLOW_LOCAL=true PORT=8080 go run .` via environment variables is verbose and error-prone for beginners.

---

## 2. Minimal Proposed CLI Command Set for v1.0.0-RC1

Rather than building a large, complex CLI toolchain, Kitwork v1.0.0-RC1 will provide a minimal binary wrapper (`kitwork`) supporting four essential subcommands:

| Command | Function / Purpose | Implementation Hook | Exit Code Behavior |
|---|---|---|---|
| `kitwork dev` | Boots local development server with `ALLOW_LOCAL=true` and hot reload enabled. | Calls `core.New(".", 10M, true, "localhost").ServeHTTP` | `0` on SIGINT/SIGTERM, `1` on port conflict. |
| `kitwork check` | Runs preflight route tree, template, and bytecode verification across all tenants. | Calls `core.Engine.Check()` | `0` if all tenants pass, `1` if any compilation error found. |
| `kitwork init <domain>` | Scaffolds a new starter application folder under `apps/` or `./`. | Copies `kitwork-starter` template files. | `0` on creation success, `1` if directory exists. |
| `kitwork version` | Displays Kitwork engine version, Go version, and bytecode contract version. | Prints `BytecodeVersion = 2`, `ProgramEncodingVersion = 1`. | `0` |

---

## 3. Recommended Implementation Strategy
- Implement the `kitwork` CLI subcommand router in `cmd/kitwork/main.go`.
- Keep CLI code strictly isolated from `engine/` core package.
