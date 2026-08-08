# Kitwork Engine — Public Release Checklist (v1.0.0-RC1)

This document tracks the release readiness checklist for Kitwork Engine v1.0.0-RC1 across 26 technical and operational dimensions.

---

## 1. Release Readiness Master Matrix

| Dimension | Current Status | Evidence / Source | Remaining Work | Blocker for RC1? | User Impact |
|---|---|---|---|---|---|
| **OS Builds** | **Pass (Windows)** | Windows `.exe` builds cleanly (`go build`). | Test & verify GitHub Actions CI for Linux `amd64`/`arm64` & macOS `arm64`. | **Yes** | Cross-platform developers cannot build binaries without verified CI. |
| **Test Suite** | **Pass** | All unit/integration tests pass (`go test ./...`). | Add Sprint 1 integration tests (`KIT-B02`, `KIT-B03`, `KIT-B04`). | **Yes** | Ensures core runtime correctness. |
| **Race Detector** | **Pass** | `go test -race ./...` passes. | Run race detector under high-concurrency host shutdown. | **Yes** | Prevents host deadlocks and data races in production. |
| **Fuzz Tests** | **Pass** | `compiler_fuzz_test.go`, `render_fuzz_test.go`. | Increase fuzz time to 1 hour in pre-release CI. | **No** | Guarantees parser and VM stability under unexpected inputs. |
| **Benchmarks** | **Pass** | `work/bench_handler_test.go` allocation budgets pass. | Document baseline RPS and memory allocation numbers. | **No** | Assures zero-allocation request hot path. |
| **Static Analysis** | **Pass** | `go vet ./...` passes without warnings. | Add `golangci-lint` configuration to repository root. | **No** | Ensures Go code cleanliness. |
| **Error Handling** | **Partial (Gap)** | `runtime.Diagnostic` exposes structured location. | Resolve `KIT-B01` (.safe() execution gap in VM). | **Yes** | JS authors cannot catch errors with `.safe()`. |
| **Security** | **Pass** | Outbound HTTP SSRF guard, `.env` scrubbing. | Add `SECURITY.md` contact policy file. | **Yes** | Protects self-hosted servers from SSRF and secret leaks. |
| **Tenant Isolation** | **Pass** | `retention_soak_test.go`, per-tenant DB & memory bounds. | Add concurrent multi-tenant soak test. | **Yes** | Prevents cross-tenant data leakage. |
| **Scheduler Recovery** | **Pass** | `cron_persist.go`, SQLite job store locks. | Test SIGKILL recovery behavior during active job execution. | **No** | Ensures cron background task durability. |
| **Database Migration** | **Pass** | Auto-table migration for system store. | Document user database schema migration guidelines. | **No** | Smooth database schema updates for apps. |
| **Backward Compatibility** | **Pass** | `contract_test.go` freezes VM v2 opcode contract. | Formalize public vs internal API deprecation strategy. | **Yes** | Prevents breaking changes for early adopters. |
| **CLI Tools** | **Pass** | `go run . check` preflight validator works. | Add `kitwork version` and `kitwork init` CLI helpers. | **No** | Developer convenience during bootstrapping. |
| **Configuration** | **Pass** | Executable `app.web()` manifest + `.env` loader. | Add schema validation warnings for malformed `config.kitwork.yaml`. | **No** | Clear configuration error diagnostics. |
| **Logging** | **Pass** | Structured `log/slog` output with request IDs. | Add log level flag (`LOG_LEVEL=debug\|info\|warn`). | **No** | Production log observability. |
| **Observability** | **Pass** | `RuntimeHealthSnapshot` tracks energy and VM stats. | Expose `/health` JSON HTTP telemetry endpoint. | **No** | Operational monitoring integration. |
| **Documentation** | **Partial** | `STABILITY.md`, `RUNTIME_ARCHITECTURE.md`. | Reconcile historical `ARCHITECTURE.md` with production tree routing. | **Yes** | New developers get confused by stale docs. |
| **Examples** | **Pass** | Sample sites in `apps/` and `getstarted/`. | Package clean 1-click starter repository (`kitwork-starter`). | **Yes** | Enables first-time users to run "Hello World" in 60s. |
| **License** | **Pass** | AGPL-3.0 with CLA exception ([LICENSE](file:///d:/project/kitwork/LICENSE)). | Ensure header headers comply with AGPL-3.0. | **No** | Legal clarity for open source users. |
| **Contribution Guide** | **Pass** | [CONTRIBUTING.md](file:///d:/project/kitwork/engine/CONTRIBUTING.md) present. | Update contribution steps for `engine/` sub-repo. | **No** | Guides external contributors. |
| **Issue Template** | **Missing** | `.github/` folder exists, template missing. | Add `.github/ISSUE_TEMPLATE/bug_report.md`. | **No** | Standardized bug reporting. |
| **Security Policy** | **Missing** | `SECURITY.md` missing. | Create `SECURITY.md` specifying vulnerability disclosure email. | **Yes** | Responsible security disclosure. |
| **Changelog** | **Missing** | `CHANGELOG.md` missing. | Create `CHANGELOG.md` documenting v1.0.0-RC1 release notes. | **Yes** | Informs users of version history. |
| **Versioning Contract** | **Pass** | `contract_test.go` fixes BytecodeVersion=2. | Document SemVer rules for language and bytecode. | **Yes** | Version predictability. |
| **Release Artifacts** | **Missing** | Binary build scripts missing. | Create `scripts/build-release.sh` generating tarballs. | **Yes** | Pre-compiled binary distribution. |
| **Docker Support** | **Missing** | `Dockerfile` missing. | Add production `Dockerfile` (multi-stage Go build). | **No** | One-command container deployment. |

---

## 2. Release Blockers Summary (Must Complete Before RC1)

1. **Cross-Platform CI Build**: Verify GitHub Actions workflow compiling Linux `amd64` and macOS `arm64` binaries.
2. **Sprint 1 Correctness Fixes**: Complete `KIT-B02` (deadlock fix), `KIT-B03` (method shadowing fix), `KIT-B04` (cache key fix).
3. **Documentation Alignment**: Reconcile `ARCHITECTURE.md` with production tree routing.
4. **Release Docs & Policy**: Add `SECURITY.md`, `CHANGELOG.md`, and `CHANGES.md`.
5. **Release Build Pipeline**: Create automated build script for release binaries and tarball artifacts.
