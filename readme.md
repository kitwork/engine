# 🚀 Kitwork Engine
> **High-Performance, Multi-Tenant Sovereign Logic Engine for Go.**

[![Go Version](https://img.shields.io/badge/go-1.25+-black?style=flat-square&logo=go)](https://golang.org)
[![Build Status](https://img.shields.io/badge/status-production--ready-blue?style=flat-square)](#)
[![Performance](https://img.shields.io/badge/latency-70ns-green?style=flat-square)](#)

Kitwork Engine is an industrial-grade, stack-based bytecode virtual machine and routing infrastructure written natively in Go. It enables SaaS providers and developers to run untrusted, dynamic JavaScript-based routing and workflow logic at native-level speeds. 

By separating the hosting platform (Go) from the tenant business rules (JavaScript Bytecode), Kitwork is ideal for multi-tenant architectures, edge functions, and programmable API gateways.

---

## ⚡ Performance Highlights
* **Core VM Instruction Speed**: ~14.1 Million operations/sec.
* **Logic Execution Latency**: ~70ns per VM instruction clock.
* **Zero-Allocation Query Builder**: 230ns compilation, **20x faster** than GORM, with 0 B/op memory overhead.
* **Zero-Downtime Hot Reloading**: Compiles and atomic-swaps script contexts in `<10ms`.
* **Zero-Allocation Disk Caching**: Streams cached binary payload directly using OS-level file offsets and `io.Copy`.

---

## 📦 Go Quickstart

### 1. Install Dependency
```bash
go get github.com/kitwork/engine
```

### 2. Standard Go Integration
Implement Kitwork in your main entrypoint in just a few lines:

```go
package main

import (
	"log"

	"github.com/kitwork/engine"
)

func main() {
	// Boot the engine server with configuration
	log.Println("Starting Kitwork Logic Engine...")
	if err := engine.Run("config.kitwork.yml"); err != nil {
		log.Fatalf("Server startup failed: %v", err)
	}
}
```

---

## ⚙️ Configuration (`config.kitwork.yml`)
The engine is configured using a YAML/JSON configuration file. Environment variables inside the file are automatically expanded at boot time.

```yaml
# Server Port
port: 8080

# Multi-tenant root directory
root: "tenants"

# List of domains for Auto-HTTPS (ACME)
domains:
  - kitwork.vn

# VM Energy Budget (limits loop iterations and network calls per request)
max_energy: 1000000

# Enables hot reloading of script files on modification
hot_reload: true

# Database connection pool (PostgreSQL / MySQL supported)
database:
  type: "postgres"
  host: "localhost"
  port: 5432
  user: "postgres"
  password: "${DB_PASSWORD}" # Expanded automatically from environment variables
  name: "postgres"
  ssl: "require"
  timeout: 5
  max_open: 50
  max_idle: 10
  lifetime: 12 # Connection max lifetime (minutes)
```

---

## 📂 Multi-Tenant Layout
Kitwork automatically maps incoming host requests to dedicated tenant environments based on the folder structure inside the configured `root` directory:

```
[root]/ (e.g. tenants/)
  └─ [tenant_identity]/ (e.g. test/)
       └─ [domain]/ (e.g. localhost/)
            ├─ app.kitwork.js   <-- Script compiled into Bytecode
            ├─ views/           <-- Sovereign HTML page fragments
            │    └─ page.kitwork.html
            ├─ static/          <-- Hashed disk-based `.static` cache snapshots
            └─ assets/          <-- Direct resource assets (CSS, JS, media)
```
When a request hits `http://localhost:8080`, the engine matches it against `tenants/test/localhost/app.kitwork.js`, compiling the VM bytecode on the fly if not cached.

---

## 🛠️ Key Architectural Features

### 1. VM Energy Limit (`max_energy`)
Protects your Go application from resource exhaustion attacks (e.g., infinite loops or heavy calculations in user scripts). The virtual machine increments energy counters for every opcode instruction execution. If `max_energy` is exceeded, the VM aborts execution cleanly, returning an error to the client instead of blocking Go scheduler threads.

### 2. Zero-Allocation Disk Caching (`.static()`)
The `.static("1h")` routing hook statically bakes dynamic handler responses to the disk using a single-file offset design:

```
[ 10-byte String Header ("0000000118") ] [ 118 bytes of JSON Metadata ] [ Raw Response Body ]
```
* **Read optimization**: The Go server reads the first 10 bytes to get the metadata length `L`, reads the next `L` bytes to configure response headers/status, and then calls `io.Copy(w, file)` to stream the remainder of the file directly to the network socket.
* **No `Seek` System Calls**: Reading the file sequentially moves the descriptor pointer automatically, saving unnecessary OS system calls and maintaining a near-zero memory footprint.

### 3. Resilient Hot Reloading
If `hot_reload` is enabled, the engine watches the `app.kitwork.js` file of each tenant. 
* **Safe swaps**: Re-compilation happens in isolation. Upon successful compilation, the pointer swap is handled atomically.
* **Syntax protection**: If an uploaded script has compile errors, the engine retains the old, stable bytecode in memory and logs the error, ensuring the server never goes down.

### 4. ACID Database Transactions
Expose relational transaction blocks safely inside the JavaScript context:
```javascript
const db = database.connection();

db.atomic((tx) => {
    tx.table("users").where("id", 1).update({ balance: 100 });
    tx.table("logs").create({ action: "balance_sync" });
    
    // Automatically commits if block completes, 
    // rolls back if any JS/Go error occurs or if an invalid parse is returned.
});
```

---

## ✒️ License & Support
* Developed by **Huỳnh Nhân Quốc** under the **Kitwork Foundation**.
* Licensed under the **MIT License**.