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

---

## 4. Practical Implementation Reality Checks

1.  **The Browser Connectivity Wall**:
    *   Web browsers cannot open raw UDP or QUIC sockets. They only support **WebTransport** (client-to-server) and **WebRTC** (peer-to-peer).
    *   Thus, "Client-to-Client QUIC replacement of WebRTC" is only possible for **Kitwork Native Clients** (such as native apps, CLI tools, or daemon nodes). For web-browser clients, peer-to-peer communication defaults to WebRTC, or falls back to server-side **QUIC Relay** routing.
2.  **NAT Traversal & Hole Punching Complexity**:
    *   UDP hole punching works reliably in home network environments (Full Cone NAT) but fails in strict corporate firewalls and cellular networks (Symmetric NAT).
    *   P2P connectivity is a long-term optimization. The initial data plane must focus on stable **Server-to-Server (S2S)** and **Server-to-Client (S2C)** connections before attempting C2C P2P.

---

## 5. Development Roadmap

```
[Phase 0: Abstraction & ID] ──> [Phase 1: Local Bus Integration] ──> [Phase 2: S2S QUIC Mesh] ──> [Phase 3: S2C WebTransport] ──> [Phase 4: C2C P2P & Relay]
```

### Phase 0: Foundations (Abstraction & Identity)
*   Define the Go `Bus` interface in the `work` package.
*   Implement asymmetric Key Pair generation (Ed25519) and signature validation.
*   Design the endpoint addressing schema (`endpoint: <identity_pubkey>`).

### Phase 1: Local Engine Integration
*   Refactor the current local SSE Broker to implement the `Bus` interface as a driver.
*   Resolve cross-request context restrictions by moving active client mappings to a shared tenant registry.

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
*   Write an End-to-End Encrypted (E2EE) relay router module (DERP equivalent) in the Go engine to serve as a fallback when direct P2P connections fail.
