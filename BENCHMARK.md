# Kitwork Engine Performance Benchmark

This report documents the performance characteristics of the Kitwork Engine under high-concurrency load testing using Grafana k6 on local server execution.

## Environment
- **CPU**: 11th Gen Intel(R) Core(TM) i7-11850H @ 2.50GHz (8 Cores, 16 Threads)
- **RAM**: 32 GB (33,271,628 KB Total)
- **OS**: Windows 11 / Windows 10
- **Go Version**: go1.25.0 windows/amd64

---

## Benchmark Scenario 1: Plain-Text Endpoint (`/hello`)
Serves a static string `hello world` directly from a routing handler.

### Load Profile
- **Virtual Users (VUs)**: Up to 1000 concurrent VUs (dynamic scaling)
- **Duration**: 30 seconds
- **Target RPS**: 50,000 requests/sec

### Results
- **Total Requests**: 1,177,172
- **Sustained Throughput**: 39,217 RPS
- **Error Rate**: 0.00%

### Latency
- **Average (Avg)**: 10.77ms
- **Median (50th percentile)**: 7.33ms
- **p90 (90th percentile)**: 26.18ms
- **p95 (95th percentile)**: 35.46ms
- **Maximum (Max)**: 171.82ms

### Validation
- **Status 200**: 100.00% success rate
- **Response Body**: 100.00% verified ('hello world')
- **Total Checks Succeeded**: 2,354,344

---

## Benchmark Scenario 2: JSON API Endpoint (`/api/json`)
Returns a structured JSON payload dynamically built in JS space:
```json
{
  "id": 1,
  "name": "kitwork",
  "runtime": true
}
```

### Load Profile
- **Virtual Users (VUs)**: Up to 1000 concurrent VUs (dynamic scaling)
- **Duration**: 30 seconds
- **Target RPS**: 50,000 requests/sec

### Results
- **Total Requests**: 941,988
- **Sustained Throughput**: 31,392 RPS
- **Error Rate**: 0.00% (69 connection refusal failures out of 941,988 requests during startup ramp-up)

### Latency
- **Average (Avg)**: 13.95ms
- **Median (50th percentile)**: 9.82ms
- **p90 (90th percentile)**: 32.46ms
- **p95 (95th percentile)**: 42.60ms
- **Maximum (Max)**: 139.52ms

### Validation
- **Status 200**: 99.99% success rate
- **Response JSON Integrity**: 99.99% verified (`id == 1`, `name == "kitwork"`, `runtime == true`)
- **Total Checks Succeeded**: 3,767,676

---

## Benchmark Scenario 3: Static Disk-Based Cache Endpoint (`/teststatic`)
Serves the response body directly from a single-file offset binary snapshot on disk, completely bypassing the Javascript VM.

### Load Profile
- **Virtual Users (VUs)**: Up to 200 concurrent VUs (dynamic scaling)
- **Duration**: 10 seconds
- **Target RPS**: 15,000 requests/sec

### Results
- **Total Requests**: 116,596
- **Sustained Throughput**: 11,650 RPS
- **Error Rate**: 0.00%

### Latency
- **Average (Avg)**: 11.36ms
- **Median (50th percentile)**: 17.20ms
- **p90 (90th percentile)**: 20.97ms
- **p95 (95th percentile)**: 23.03ms
- **Maximum (Max)**: 35.27ms

### Validation
- **Status 200**: 100.00% success rate
- **Response Body**: 100.00% verified ('static cache output')
- **Total Checks Succeeded**: 233,192

---

## Conclusion
Kitwork Engine demonstrates outstanding performance characteristics across all benchmark configurations:
- **Plain-text routing** achieves ~39.2k RPS with 7.33ms median latency.
- **Dynamic JSON VM-execution** achieves ~31.4k RPS with 9.82ms median latency.
- **Single-File Offset Static Disk Caching** achieves stable, low-latency execution (max 35ms, p95 23ms under high loads) with zero connection failures, bypassing VM execution overhead entirely to serve pre-rendered snapshots.
