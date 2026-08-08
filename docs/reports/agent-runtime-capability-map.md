# Kitwork Agent Runtime — Engine Capability Mapping Matrix

This document audits the existing Kitwork Engine capabilities against the architectural requirements of a durable, multi-tenant AI Agent Runtime.

---

## 1. Capability Assessment Table

| Capability | Current Support Level | Recommended System Layer | Existing Engine Package | Implementation Notes / Strategy |
|---|---|---|---|---|
| **Agent Identity** | **Supported** | Host Runtime & App Layer | [engine/site/identity.go](file:///d:/project/kitwork/engine/site/identity.go) | Multi-tenant `TenantID` and `site.Identity` isolate agent workspaces. |
| **Agent State** | **Supported** | Host Runtime & DB Layer | [engine/work/db.sqlite.go](file:///d:/project/kitwork/engine/work/db.sqlite.go) | SQLite entity tables store key-value memory and agent state. |
| **Run State** | **Can Build on Primitives** | Host Runtime Layer | [engine/work/queue.go](file:///d:/project/kitwork/engine/work/queue.go) | Requires a dedicated `RunState` manager in Host Runtime. |
| **Step Execution** | **Supported** | VM & Interpreter | [engine/runtime/interpreter.go](file:///d:/project/kitwork/engine/runtime/interpreter.go) | Sequential VM bytecode execution with stack isolation. |
| **Scheduler** | **Supported** | Host Runtime Layer | [engine/work/cron.go](file:///d:/project/kitwork/engine/work/cron.go) | `work.CronScheduler` manages recurring agent triggers. |
| **Event Inbox** | **Supported** | Host Runtime Layer | [engine/work/queue.go](file:///d:/project/kitwork/engine/work/queue.go) | SQLite persistent queue buffers incoming webhook/agent events. |
| **Retry Policy** | **Partially Supported** | Host Runtime Layer | [engine/work/queue.go](file:///d:/project/kitwork/engine/work/queue.go) | Exponential backoff retry in worker queue; needs Step-level policy. |
| **Timeout** | **Supported** | VM & Host Context | `context.WithTimeout` | Go `context.Timeout` cancels VM opcode loop cleanly. |
| **Cancellation** | **Supported** | VM Interpreter | [engine/runtime/interpreter.go](file:///d:/project/kitwork/engine/runtime/interpreter.go#L162) | Checked every 64 opcodes via `DiagnosticCancelled`. |
| **Checkpoint** | **Can Build on Primitives** | Host Runtime Layer | [engine/compiler/artifact.go](file:///d:/project/kitwork/engine/compiler/artifact.go) | Store serialised JSON state snapshot in SQLite checkpoints table. |
| **Resume** | **Can Build on Primitives** | Host Runtime Layer | [engine/work/tenant.go](file:///d:/project/kitwork/engine/work/tenant.go) | Re-hydrate run state from last checkpoint on host startup. |
| **Tool Execution** | **Supported** | Engine Capabilities | [engine/utilities](file:///d:/project/kitwork/engine/utilities) | Go native capabilities (HTTP, Crypto, QR) exposed to VM. |
| **Skill Registry** | **Missing** | Application Layer | N/A (New) | Needs declarative skill manifest registry in application layer. |
| **Secret Binding** | **Supported** | Host Config & Env | [engine/work/env.go](file:///d:/project/kitwork/engine/work/env.go) | `env.require()` injects scrubbed secrets per tenant. |
| **Human Approval** | **Missing** | Host Runtime Layer | N/A (New) | Pause run at `WaitingForApproval` state and issue approval token. |
| **Audit Log** | **Supported** | Host & App Layer | `log/slog` & SQLite | Structured `slog` and SQLite audit tables record all tool actions. |
| **Resource Budget** | **Supported** | VM Interpreter | [engine/runtime/energy.go](file:///d:/project/kitwork/engine/runtime/energy.go) | `MaxEnergy` gas ceiling limits execution opcodes per step. |
| **Model Adapter** | **Application Layer** | Application Layer | N/A (New) | Model-agnostic LLM caller (OpenAI / Claude / Gemini API). |
| **Native Execution** | **Supported** | Go Stdlib Host | Repo Root | Standard Go library execution with zero CGO dependencies. |
| **Container Execution**| **Not in VM** | External Capability | N/A | VM remains stdlib Go; container calls routed via external tools. |

---

## 2. Architecture Boundary Principles
1. **VM Stays Thin & Pure**: The Go VM (`engine/runtime`) executes bytecode and enforces gas limits (`MaxEnergy`). It contains zero LLM or Agent business logic.
2. **Host Runtime Manages Durability**: The Host Runtime (`engine/work`) handles Run state machines, persistent checkpoints, process restart recovery, and human approval tokens.
3. **Application Defines Model & Prompts**: Prompts, LLM provider API credentials, and domain workflows live in the application layer (e.g. `WithAI Newsroom`).
