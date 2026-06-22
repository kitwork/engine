# Kitwork Network Backbone Architecture: QUIC-Centric Transport Core

This document specifies the technical design, architectural boundaries, and implementation roadmap for the **QUIC-centric transport backbone** of the Kitwork distributed runtime system.

---

## 1. Executive Summary & Vision

> **Kitwork is a distributed, multi-tenant serverless runtime system (infrastructure layer), not a single real-time application.**

The core networking backbone is designed to handle all communication requirements of a multi-node cluster under a single unified protocol: **QUIC**. By leveraging QUIC's multiplexed streams, native encryption, and UDP foundation, we eliminate protocol fragmentation (e.g., mixing gRPC, WebSockets, and WebRTC) in favor of a clean, sovereign, and lightweight transport layer.

---

## 2. Structural Decomposition

To maintain reliability and decouple infrastructure complexity from developer-facing web application logic, the network stack is divided into three distinct planes:

```
                      KITWORK
   
       ┌─────────────────────────────────────┐
       │     CONTROL PLANE (The Brain)       │
       ├─────────────────────────────────────┤
       │ - Node Identity (Ed25519/Keys)      │
       │ - Node & Tenant Discovery           │
       │ - Routing Decisions (Direct/Relay)  │
       │ - Cluster Leases (Postgres-backed)  │
       └──────────────────┬──────────────────┘
                          │
                          ▼
       ┌─────────────────────────────────────┐
       │     DATA PLANE (QUIC Backbone)      │
       ├─────────────────────────────────────┤
       │ - Node-to-Node Cluster Mesh         │
       │ - Server-to-Server Event Relay      │
       │ - Client-to-Server WebTransport     │
       │ - Stream-based Multiplexed RPC      │
       └──────────────────┬──────────────────┘
                          │
                          ▼
       ┌─────────────────────────────────────┐
       │     APPLICATION / UI LAYER          │
       ├─────────────────────────────────────┤
       │ - Local SSE Broker (Local Bus)      │
       │ - Logs UI & Progress Streaming      │
       │ - Deployment & Metrics Dashboard    │
       └─────────────────────────────────────┘
```

### 2.1. The Control Plane
Responsible for orchestration, identity validation, routing tables, and consensus. It acts as the "Source of Truth" (Invariant 2).
*   **Consensus**: Uses database-backed leases (Postgres/SQLite) for coordination instead of heavy Paxos/Raft.
*   **Discovery**: Tracks node heartbeat records and determines whether peers can communicate directly or require relaying.

### 2.2. The Data Plane (QUIC Backbone)
The high-performance transport pipeline of the cluster.
*   **Mesh Connection**: Every node maintains a full-duplex QUIC connection to peer nodes.
*   **Streams**: Each node opens lightweight, multiplexed streams on the fly for RPC calls, metrics gossip, and SSE channel message forwarding.

### 2.3. The Application Layer
Developer-facing interfaces exposed to JScript VM containers.
*   **SSE (Server-Sent Events)**: Serves purely as a local browser-streaming interface (e.g., streaming deployment logs, runtime status, and metrics). It does not participate in core system-level networking.

---

## 3. Core Architectural Invariants

### Invariant A: Interface-First Transport Abstraction
To prevent runtime components (routers, VM executors) from being tightly coupled to a specific protocol (SSE, WebSockets, or QUIC), all message publishing and subscribing must go through a protocol-agnostic `Bus` interface in the Go engine supporting both multicast (Pub/Sub) and unicast (1-to-1 routing):

```go
type Bus interface {
    Publish(channel string, payload []byte) error
    SendTo(clientID string, payload []byte) error // Route direct 1-to-1 message in cluster
    Subscribe(channel string) (<-chan []byte, error)
    Unsubscribe(channel string) error
}
```

*   **Single-Node Mode**: Uses the local SSE broker implementation.
*   **Multi-Node Cluster Mode**: Swaps in the QUIC Mesh transport driver.
*   *Benefit*: Decouples the JScript VM from networking details. Upgrading the underlying network doesn't require modifying JScript VM routes.

### Invariant B: Identity-First Cryptography & TLS 1.3 Peer Verification
Node identity, peer authentication, and code-execution permissions (Capsule verification) are unified under the same asymmetric cryptographic keys (Ed25519).
*   **CA-less Peer Verification**: Since nodes connect directly via IP:Port without conventional SSL certs signed by public Certificate Authorities (CAs), Kitwork implements self-signed certificates paired with custom **`VerifyPeerCertificate`** callbacks in TLS 1.3. This pins node identities by verifying signature authenticity against the public Ed25519 key registered in the Control Plane.

### Invariant C: Unicast Routing & Address Resolution
To route direct 1-to-1 client messages (`sse.send` or `SendTo`), the cluster maintains a client location registry in the Control Plane:
*   When a client connects to Node A, its presence is written to the registry (`Client X -> Node A`).
*   When Node B calls `Bus.SendTo("ClientX", payload)`, the QUIC Bus driver resolves Client X's location, identifies Node A as the host, and forwards the packet over the peer QUIC stream. Node A then delivers it locally.

### Invariant D: Multiplexed Tiny RPC over QUIC Streams
We reject gRPC due to its heavy runtime footprint and complex protobuf requirements. Instead, Kitwork nodes communicate via custom multiplexed QUIC streams using a minimal RPC layer encoding payloads in JSON or MsgPack.

### Invariant E: Tenant-Scoped Channels & Authorization (Cluster Isolation)
The local Bus is per-tenant, but across the cluster every channel **must be namespaced by tenant identity** so one node can never leak a tenant's events to another. Channel keys are `<tenant_id>/<channel>`, and the Bus enforces an ACL: a VM may only `Publish`/`Subscribe` under its own tenant identity. Without this invariant, the cluster silently breaks the tenant isolation the single-node engine guarantees.

### Unified Addressing Schema
Every endpoint resolves through one URI, so the Bus can choose the route (in-process / S2S / S2C / P2P) from the address alone:

```
kw://<node>/<tenant>/<kind>/<id>      kind ∈ { channel, client, capsule, node }
```

The Control Plane's location registry (Invariant C) maps `client`/`capsule` ids to their current host node.

---

## 3.5. Failure Modes, Delivery Semantics & Observability

### Control-Plane Degradation (no split-brain)
If the Control Plane store (Postgres lease/registry) becomes unavailable, the cluster enters a **degraded mode**, not a hard failure: established QUIC connections and their streams keep flowing (the data plane is independent), and only *new* discovery, routing decisions, and address resolution stall. Nodes never invent conflicting routing state on their own.

### Delivery Guarantees (per traffic class)
*   **Pub/Sub events (SSE-class)**: *at-most-once* — non-blocking drop when a slow consumer's buffer fills (a realtime stream favors freshness over completeness); `Last-Event-ID` replay covers gaps.
*   **Tiny RPC (S2S)**: *at-least-once* with an explicit ack + idempotency key; the caller retries on a dropped stream. An RPC is never silently dropped.
*   **Backpressure**: a saturated downstream applies QUIC flow control upstream rather than buffering without bound — memory stays bounded under fan-out.

### Cluster-Wide Rate Limiting
The engine's existing rate-limit `scope` axis (`tenant` | `server`) gains a third value `cluster`: limiter state for cluster-scoped rules is shared over the Bus, so a single IP/user is capped across **all** nodes, not only the node it happened to hit.

### Backbone Observability
The transport is self-instrumented: active connection count, open stream count, direct-vs-relay ratio, per-peer RTT, and dropped-event counters — surfaced to the Application-layer monitoring UI.

---

## 4. Practical Implementation Reality Checks

1.  **The Browser Connectivity Wall**:
    *   Web browsers cannot open raw UDP or QUIC sockets. They only support **WebTransport** (client-to-server) and **WebRTC** (peer-to-peer).
    *   Thus, "Client-to-Client QUIC replacement of WebRTC" is only possible for **Kitwork Native Clients** (such as native apps, CLI tools, or daemon nodes). For web-browser clients, peer-to-peer communication defaults to WebRTC, or falls back to server-side **QUIC Relay** routing.
2.  **NAT Traversal & Hole Punching Complexity**:
    *   UDP hole punching works reliably in home network environments (Full Cone NAT) but fails in strict corporate firewalls and cellular networks (Symmetric NAT).
    *   P2P connectivity is a long-term optimization. The initial data plane must focus on stable **Server-to-Server (S2S)** and **Server-to-Client (S2C)** connections before attempting C2C P2P.

---

## 4.5. Current Code Foundation (what already exists)

The single-node engine already contains working prototypes of the backbone's local layer — these are not throwaway; they become the first `Bus` driver.

| Already in code | Role in the QUIC backbone |
|---|---|
| **SSE broker** (`work/sse.go`): per-tenant pub/sub channels, `SendTo` (1-to-1), `Last-Event-ID` replay, dynamic subscribe/unsubscribe | Prototype of the **Local Bus driver** — refactored to implement `Bus` in Phase 1 |
| **Shared-registry fix** for the cross-request broker | The local form of Invariant C (Address Resolution): active client mappings live in a shared per-tenant registry |
| **Rate-limit `scope: tenant\|server`** (`LimiterStore` / `EnforceChecks`) | Ready to accept the third `scope: cluster` value once the Bus shares limiter state |
| **Per-tenant identity** | The seed of the Control Plane: tenant isolation today → node/peer identity (Ed25519) tomorrow |

> SSE today is the **stepping stone**: shipping it proves the channel / pub-sub model that the QUIC data plane will later carry — the broker abstraction is reused verbatim.

---

## 5. Development Roadmap

```
[Phase 0: Abstraction & ID] ──> [Phase 1: Local Bus] ──> [Phase 1.5: PG NOTIFY] ──> [Phase 2: S2S QUIC Mesh] ──> [Phase 3: S2C WebTransport] ──> [Phase 4: C2C P2P & Relay]
```

### Phase 0: Foundations (Abstraction & Identity)
*   Define the Go `Bus` interface in the `work` package.
*   Implement asymmetric Key Pair generation (Ed25519) and signature validation.
*   Design the endpoint addressing schema (`endpoint: <identity_pubkey>`).

### Phase 1: Local Engine Integration
*   Refactor the current local SSE Broker to implement the `Bus` interface as a driver.
*   Resolve cross-request context restrictions by moving active client mappings to a shared tenant registry.

### Phase 1.5: Multi-Node via Postgres LISTEN/NOTIFY (Intermediate)
A cheap stepping stone between the single-node in-memory broker and the full QUIC mesh: multiple processes (or a small cluster sharing one database) fan messages out through Postgres `LISTEN/NOTIFY`, behind the same `Bus` interface. This makes multi-node pub/sub work **before** QUIC lands and validates the Bus abstraction under real cross-process delivery.

### Phase 2: Server-to-Server QUIC Mesh
*   Integrate `quic-go`.
*   Implement the QUIC driver for the `Bus` interface.
*   Establish full-duplex S2S QUIC connection meshes and route real-time events across nodes using multiplexed streams.

### Phase 3: Server-to-Client WebTransport
*   Deploy HTTP/3 capabilities alongside the current HTTP core server.
*   Expose a WebTransport endpoint for client connections.
*   Upgrade client-side JavaScript to auto-detect and upgrade connection transport from SSE to WebTransport.

### Phase 4: Peer-to-Peer & Relay (Tailscale Model)
*   Implement UDP Hole Punching protocols for native agents.
*   Write an End-to-End Encrypted (E2EE) relay router module (DERP equivalent) in the Go engine to serve as a fallback when direct P2P connections fail. The relay is **blind**: it forwards encrypted bytes between peers and never holds the keys to decrypt them — sovereignty is preserved even when traffic transits a shared relay.
