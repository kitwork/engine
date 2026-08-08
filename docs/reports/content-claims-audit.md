# Kitwork Engine — Content Claims Audit

This audit evaluates all marketing claims and technical assertions to ensure 100% honesty, accuracy, and empirical verification.

---

## 1. Public Technical Claims Audit Matrix

| Claim Made | Claim Status | Supporting Code Evidence | Supporting Test Evidence | Action / Wording Adjustment |
|---|---|---|---|---|
| **"Zero CGO Dependencies"** | **Verified Fact** | [engine/go.mod](file:///d:/project/kitwork/engine/go.mod) contains standard library Go modules only. | `go build ./...` compiles with `CGO_ENABLED=0`. | Approved for public copy. |
| **"Zero Allocation Hot Paths"** | **Verified Fact** | `app.Pool` VM recycling and `FastReset`. | `work/bench_handler_test.go` (`TestHandlerCorpusAllocationBudgets`). | Approved for public copy. |
| **"Pre-Publication Bytecode Verification"** | **Verified Fact** | [engine/runtime/verify.go](file:///d:/project/kitwork/engine/runtime/verify.go) 4-pass verifier. | `verify_test.go`, `contract_test.go`. | Approved for public copy. |
| **"100% Bug-Free or Production Ready"** | **Unconfirmed / Prohibited** | Backlog identifies `KIT-B01` (`.safe()` VM abort) and `KIT-B02` (shutdown lock order). | Sprint 1 plan active. | **Strictly Prohibited**. Use "v1.0.0-RC1 in Active Stabilization". |
| **"World's Fastest Web Engine"** | **Unconfirmed / Prohibited** | No comparative global benchmarks against V8 or Cloudflare Workers. | Local benchmarks only. | **Strictly Prohibited**. Replaced with empirical RPS numbers. |
| **"Durable AI Agent Runtime"** | **Verified Capability (With Bounds)** | Identity SQLite memory, energy budgeting, capability registry. | `agent-runtime-readiness.md`. | Approved with clarification on current LLM bridge requirements. |
