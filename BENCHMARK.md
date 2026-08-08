# Kitwork VM v2 Performance Baseline

Measured on 2026-08-01. This document replaces the pre-VM-v2 k6 report.

These numbers are a local regression baseline, not a production capacity
promise. They describe the exact workloads below on one development machine.
Do not compare them directly with the previous report: the engine, routes,
payloads, Go version and load harness are different.

## Environment

- CPU: Intel Core i7-11850H, 8 cores / 16 logical processors
- RAM: 32 GB
- OS: Windows 11 Pro, build 26200
- Go: go1.26.0 windows/amd64
- Engine revision: `2b2a9ce7fa04073eb357227cafa81d71fcfe4c10`
- Engine worktree: VM v2 development state

The Go benchmarks ran five times, sequentially. Tables report the median
`ns/op`; allocations are from the corresponding median run. The HTTP load
tests ran three times and report the median run-level result.

## VM Microbenchmarks

Command:

```powershell
go test ./runtime -run '^$' -bench '^BenchmarkVM' -benchmem -count=5
```

| Workload | Median | Instructions | Memory | Allocations |
|---|---:|---:|---:|---:|
| Arithmetic dispatch | 35.017 us | 1,816 | 0 B | 0 |
| 100 function calls | 56.863 us | 2,221 | 768 B | 4 |
| Array map/filter/reduce | 13.445 us | 320 | 4,568 B | 23 |
| VM fast reset | 7.516 ns | - | 0 B | 0 |
| Pool acquire/release | 337.3 ns | - | 0 B | 0 |
| Exceptional state release | 611.311 us | - | 2,028,259 B | 3,116 |

Arithmetic dispatch executes at approximately 19.3 ns per verified
instruction in this synthetic loop. Fast reset and normal pool reuse remain
allocation-free.

## Production Handler Corpus

`BenchmarkServeHandlerEngine` runs the complete `work.Tenant.Serve` lifecycle
while removing `httptest.ResponseRecorder` and request-clone allocations.

```powershell
go test ./work -run '^$' -bench '^BenchmarkServeHandlerEngine$' -benchmem -count=5
```

| Workload | Median | Memory/request | Allocations/request |
|---|---:|---:|---:|
| Plain text | 5.642 us | 1,768 B | 40 |
| JSON compute | 19.768 us | 6,532 B | 76 |
| Native import | 12.859 us | 4,288 B | 65 |
| Guarded view | 17.065 us | 6,389 B | 90 |
| Markdown collection | 85.192 us | 10,603 B | 182 |
| SQLite callback query | 1.366 ms | 12,113 B | 253 |

The parallel tenant benchmark shows no cost growth with the number of loaded
sites:

| Workload | Median |
|---|---:|
| One tenant | 8.641 us/request |
| Tenant selection from 1 site | 7.315 us/request |
| Tenant selection from 8 sites | 6.053 us/request |
| Tenant selection from 64 sites | 4.130 us/request |

The apparent improvement at higher site counts is benchmark scheduling noise,
not a claim that more sites make requests faster. The important result is that
per-request cost does not grow with site count.

## Canary Through The Core Engine

The host workspace contains a real test site at
`apps/0123456789abcdefghijklmnopqrstuvwxyz/kitwork.localhost`. It passes through
`core.Engine`, app/site/generation ownership, routing, the VM, response
lifecycle and rendering.

```powershell
go test . -run '^$' -bench '^BenchmarkCanaryProductionPath$' -benchmem -count=5
```

| Workload | Median | Memory/request | Allocations/request |
|---|---:|---:|---:|
| Health JSON | 316.868 us | 15,630 B | 239 |
| Home SSR | 2.086 ms | 519,669 B | 2,516 |
| Health JSON, parallel | 256.540 us | 53,188 B | 255 |

The full Core path is currently much more expensive than the underlying
Tenant handler. That difference is actionable and should not be attributed to
the VM.

## Loopback HTTP

The host-level `tools/canarybench` runner starts `core.Engine` without the host
database, cron or queue workers, then sends real HTTP/1.1 requests over
loopback TCP. Latency includes reading the complete response body.

### JSON

- 50,000 requests per run
- 16 concurrent clients
- 1,000 warm-up requests
- 92-byte response body

| Metric | Median of 3 runs |
|---|---:|
| Throughput | 4,057 requests/s |
| p50 | 3.814 ms |
| p95 | 5.621 ms |
| p99 | 6.816 ms |
| Errors | 0.000% |

### SSR home

- 20,000 requests per run
- 16 concurrent clients
- 500 warm-up requests
- 17,164-byte response body

| Metric | Median of 3 runs |
|---|---:|
| Throughput | 1,381 requests/s |
| p50 | 11.356 ms |
| p95 | 14.721 ms |
| p99 | 16.767 ms |
| Errors | 0.000% |

Five isolated first requests with no warm-up put cold site generation plus the
first SSR response at a median of **50.138 ms**.

Reproduce:

```powershell
go run ./tools/canarybench -path '/api/health?source=load'
go run ./tools/canarybench -path '/' -requests 20000
go run ./tools/canarybench -path '/' -requests 1 -concurrency 1 -warmup 0
```

## Profile Finding

A five-second CPU profile of the warm Canary JSON route attributed:

- 85.3% cumulative CPU to `utilities/safepath.Contains` canonicalization,
  reached from `Tenant.serveTreeStatic`;
- 84.6% cumulative CPU to `filepath.EvalSymlinks`;
- 2.6% cumulative CPU to VM execution.

On Windows, repeated path canonicalization enters `FindFirstFile` and related
filesystem syscalls. This is the current dominant production-path bottleneck.
The next optimization should preserve the safe-path boundary while moving
canonical site-root/static metadata into the immutable generation instead of
re-evaluating symlinks on every request.

The second clear target is SSR allocation: approximately 520 KB and 2,516
allocations per Canary home render. Template/render profiling should follow
after the static path check is removed from the hot request path.

## Filesystem Boundary Optimization

Measured on 2026-08-06 using the same machine and five sequential runs.

Two changes preserve the existing static-file precedence and symlink escape
protection:

1. The immutable tenant generation prepares canonical app/site roots once.
   Candidate targets are still canonicalized before a real file is served.
2. Static resolution calls `os.Stat` before canonicalization. Dynamic route
   directories and missing files return immediately; only an existing regular
   file pays for the symlink-aware containment check.

The prepared-boundary microbenchmark changed from a median of **255.426 us,
6,816 B, 113 allocations** to **144.191 us, 4,351 B, 60 allocations**.

The production Canary JSON route changed as follows:

| State | Median | Memory/request | Allocations/request |
|---|---:|---:|---:|
| Original baseline | 316.868 us | 15,630 B | 239 |
| Prepared root boundary | 180.741 us | 12,739 B | 195 |
| Existing-file-only canonicalization | 33.611 us | 7,924 B | 135 |

That is approximately **9.4x lower latency**, **49% less memory**, and **44%
fewer allocations** for this production JSON path. The parallel JSON median
is now **29.720 us, 9,133 B, 135 allocations**.

The follow-up CPU profile no longer attributes the request to
`filepath.EvalSymlinks`. The largest cumulative application costs are VM
execution (24.8%), the remaining existence check in `os.Stat` (18.7%), native
method dispatch/response work, and JSON encoding. Removing `os.Stat` would
require a static manifest or a precedence change, so it is deliberately left
in place until a real workload justifies that complexity.

## Generation-Prepared SSR Presentation

Measured on 2026-08-06 with the same Canary home page and five sequential
runs. The original profile attributed 75.8% cumulative CPU and 93.2% of
allocation space below `Render.tmpl`. JIT CSS, material, icons, fonts, theme,
client-runtime scans, and inline CSS/JS minification were repeated for every
request even though the active template generation was immutable.

Static render trees now prepare source-driven presentation once. Templates
whose `class`, `style`, `data-kit-*`, `data-kitwork-*`, or style-block content
depends on request data retain the complete request pipeline. The default
minifier uses parser-level `{{ }}` delimiters to prepare template HTML and
inline assets. Final requests treat bound data as opaque and do not parse the
document a second time. The `stdminify` build conservatively keeps request-time
minification.

| State | Median | Memory/request | Allocations/request |
|---|---:|---:|---:|
| Original baseline | 2.086 ms | 519,669 B | 2,516 |
| Generation-prepared JIT presentation | 564.596 us | 283,416 B | 1,026 |
| Prepared inline assets + request markup | 230.477 us | 156,781 B | 191 |
| Prepared document + opaque request data | 121.401 us | 97,469 B | 170 |

The final path is approximately **17.2x faster**, uses **81% less memory**, and
performs **93% fewer allocations** than the original production SSR baseline.
Regression tests verify that data-driven classes still generate
request-specific CSS and that prepared minification leaves bound data
untouched.

Authored Hydrate entities are now decoded to the same value exposed by the
browser DOM before verification, pre-render, and dynamic-class extraction.
Template minification preserves attribute quotes until those server passes
finish, so Hydrate pages retain generation-prepared minification without an
entity-sensitive fallback.

## Generation-Prepared Template Evaluation

Measured on 2026-08-06 with a fixed 16-item template corpus covering nested
paths, `if` comparisons, `for`, arithmetic, nullish coalescing, ternary
selection, escaped output, and `raw(...)`.

The previous binder repeatedly scanned every expression for every operator,
parsed failed numeric literals, split paths, copied the complete scope map for
each loop item, and assembled nested output strings. The prepared render plan
now owns immutable expression trees and path segments. Requests use lexical
scope frames, one output builder, and direct escaped `Value` writes.

| State | Median | Memory/op | Allocations/op |
|---|---:|---:|---:|
| Request-time expression parsing | 331.746 us | 35,289 B | 583 |
| Prepared expression tree + scope frames | 11.907 us | 4,560 B | 8 |

The isolated evaluator is approximately **27.9x faster**, uses **87% less
memory**, and performs **98.6% fewer allocations**. CI caps this corpus at 12
allocations and separately verifies deterministic output, loop-scope isolation,
and byte-for-byte equivalence with `html.EscapeString`.

On the production Canary home route, the same change reduced the warm request
from 170 to 92 allocations and from 97,469 B to approximately 68,188 B. The
five-run latency median was 124.156 us versus the previous 121.401 us; that
desktop variance is intentionally recorded rather than treated as a speed
claim or CI threshold.

## Production Stability Gates

Measured on 2026-08-06 after bounded request/runtime observability was enabled:

| Path | Median | Memory/op | Allocations/op | CI budget |
|---|---:|---:|---:|---:|
| Prepared route resolution | 1.265 us | 536 B | 8 | 8 allocations |
| Observed `core.Engine` text request | 10.850 us | 1,811 B | 42 | 48 allocations |
| Prepared template evaluator | 11.907 us | 4,560 B | 8 | 12 allocations |
| Generation-prepared SSR fixture | 204.587 us | 27,898 B | 102 | 110 full / 100 engine |

The SSR fixture includes Tailwind JIT, material, an icon, dark theme classes,
Hydrate expressions, server bindings, and final minification. Latency remains
a benchmark record rather than a test assertion; only deterministic allocation
budgets fail CI.

## Regression Rule

Always compare the same benchmark, payload, concurrency and machine state.
Use at least five Go benchmark runs and three HTTP runs. Keep bad runs in the
record, investigate large variance, and use allocation gates in CI instead of
hard latency thresholds.
