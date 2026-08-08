# Kitwork Engine — Error Experience & Diagnostic Catalogue

This document catalogues all developer-facing error categories in the Kitwork Engine, specifying error messages, source location tracking, remediation steps, and production privacy scrubbing rules.

---

## 1. Error Diagnostic Catalogue

| Error Category | Trigger Condition | Displayed Error Message Format | File & Line Context | Remediation Action | Dev vs. Prod Visibility |
|---|---|---|---|---|---|
| **Syntax Error (Banned Syntax)** | Script uses `while`, `try-catch`, `class`, or `function`. | `ParseError: banned keyword 'while' is not supported in Kitwork JS subset` | Yes (`file.js:14:5`) | Replace `while` loop with array iterators (`map`, `filter`, `forEach`) or bounded `for-of`. | Visible in both Dev & Prod logs. |
| **Syntax Error (Array Trailing Comma)** | Array literal has trailing comma `[1, 2,]`. | `ParseError: unexpected token ',' (array trailing commas are not permitted)` | Yes (`file.js:22:10`) | Remove trailing comma from array literal. | Visible in both Dev & Prod logs. |
| **Import Error** | Relative import file does not exist (`./lib/missing.js`). | `BundleError: cannot resolve native import './lib/missing.js'` | Yes (`file.js:2:1`) | Verify relative file path and filename case on disk. | Visible in both Dev & Prod logs. |
| **Bytecode Verifier Error** | Malformed bytecode or out-of-bounds jump offset. | `VerifyError [code 104]: jump address 0x01FA out of bounds` | Bytecode Offset | Recompile source file. Report engine bug if auto-generated. | Internal log only; Prod returns 500. |
| **Energy Limit Exceeded** | Instruction count exceeds `MaxEnergy` gas ceiling. | `DiagnosticEnergyLimit: Energy Limit Exceeded (10,000,000 opcodes)` | Yes (`file.js:45:12`) | Optimize loop execution or increase `MaxEnergy` in host configuration. | Visible in Dev; Prod returns 508. |
| **Context Cancelled** | Client disconnects or request timeout expires. | `DiagnosticCancelled: Execution Cancelled: context deadline exceeded` | Yes (`file.js:88:2`) | Increase request timeout or optimize database query duration. | Logged as Info level in Prod. |
| **Database Guard Violation** | `.update()` or `.delete()` called without `.where()`. | `DatabaseError: mutating operation 'update' requires a mandatory .where() clause` | Yes (`file.js:30:3`) | Add explicit `.where("id", id)` clause to query before calling `.update()`. | Visible in Dev & Prod logs. |
| **Missing Secret Variable** | `env.require("MISSING_KEY")` fails to find key. | `ConfigError: required environment key 'MISSING_KEY' is missing in .env` | Yes (`file.js:5:14`) | Add `MISSING_KEY=value` to tenant `.env` file. | Visible in Dev; Prod log redacts key value. |
| **Native Panic** | Go native capability method panics. | `NATIVE_PANIC: runtime exception in capability 'qrcode'` | Yes (`file.js:12:8`) | Inspect Go host logs (`slog`) for underlying Go panic trace. | Stack trace in Dev; Prod returns 500. |
| **Missing Route (404)** | Requested URL path matches no folder or route. | Renders nearest `notfound.kitwork.html` view | N/A | Create route folder or add fallback handler. | Renders 404 HTML/JSON. |

---

## 2. Production Security & Privacy Scrubbing Policy

```text
[ Development Mode (ALLOW_LOCAL=true) ]
  ├── Detailed source file, line, and column reported in HTTP response body
  ├── Complete diagnostic call stack printed to console
  └── Full SQL query strings exposed in debug logs

[ Production Mode (ALLOW_LOCAL=false) ]
  ├── HTTP response returns generic 500 Internal Server Error (or 404 / 429)
  ├── Host log (slog) records X-Request-ID, tenant identity, and diagnostic trace
  └── Secrets, database passwords, and PII automatically scrubbed from logs
```
