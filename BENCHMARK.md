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

## Regression Rule

Always compare the same benchmark, payload, concurrency and machine state.
Use at least five Go benchmark runs and three HTTP runs. Keep bad runs in the
record, investigate large variance, and use allocation gates in CI instead of
hard latency thresholds.
