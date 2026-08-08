# Kitwork Console — UI/UX Specification & Wireframe Blueprint

This document defines the functional specification, data sources, and wireframe layouts for the future Kitwork Console administration dashboard.

---

## 1. Console Module Architecture

```text
Kitwork Console (Admin UI)
  ├── 1. Tenant Overview (Active sites, generation versions, health status)
  ├── 2. Runtime & Deployments (Generation publication history, hot reload triggers)
  ├── 3. Structured Logs (slog viewer, X-Request-ID filter, error stack traces)
  ├── 4. Scheduler & Queue (Cron job status, queue worker heartbeats, manual triggers)
  ├── 5. Database Manager (Per-tenant SQLite table explorer, query runner)
  ├── 6. Domains & SSL (Custom domain mappings, AutoSSL certificate status)
  ├── 7. Environment Variables (.env manager per tenant generation)
  ├── 8. Resource Usage (Active VM pool count, MaxEnergy gas metrics, RAM)
  ├── 9. Error Diagnostics (Diagnostic code distribution, syntax error list)
  └── 10. Audit Log (Identity activity audit trail)
```

---

## 2. Wireframe Specifications

### Module 1: Tenant Overview Dashboard
```text
+--------------------------------------------------------------------------------+
| KITWORK CONSOLE | Tenants (12) | System Status: HEALTHY | Energy: 1.4M / 10M    |
+--------------------------------------------------------------------------------+
| Identity           Domain            Gen Ver    Status      RPS    Avg Energy  |
| 0123456789...      lofiwithme.com    v4         Active      142    1,240       |
| 02ni3cg1xb...      kitwork.io        v12        Active      850      890       |
+--------------------------------------------------------------------------------+
```

### Module 3: Structured Logs Viewer
```text
+--------------------------------------------------------------------------------+
| LOGS | Filter by Request-ID: [ req_9f8a2b... ] | Level: [ ERROR ]             |
+--------------------------------------------------------------------------------+
| Timestamp    Level  Tenant          Message                           IP       |
| 09:14:02Z    ERR    lofiwithme.com  DatabaseError: missing .where()   127.0.0.1|
| 09:12:45Z    WARN   kitwork.io      Prewarm compile fallback          127.0.0.1|
+--------------------------------------------------------------------------------+
```

### Module 4: Scheduler & Queue Manager
```text
+--------------------------------------------------------------------------------+
| CRON SCHEDULER | Identity: tenant_a | Active Jobs: 3                           |
+--------------------------------------------------------------------------------+
| Job Name           Schedule       Last Run       Next Run       Status         |
| _cron/clean.js     */5 * * * *    09:10:00Z      09:15:00Z      SUCCESS        |
| _cron/sync.js      0 0 * * *      00:00:00Z      24:00:00Z      SUCCESS        |
+--------------------------------------------------------------------------------+
```

---

## 3. Host API Seams for Console Backend
- `GET /_console/api/health` -> `core.Engine.Health()` returning `RuntimeHealthSnapshot`.
- `GET /_console/api/tenants` -> Lists active site runtimes and generation versions.
- `POST /_console/api/reload` -> Triggers `Generation` preparation and atomic swap.
