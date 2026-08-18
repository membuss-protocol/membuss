<div align="center">

# Membuss

### A peer-to-peer network for decentralized storage, networking, and edge compute.

Membuss lets applications store data, stream media, and execute serverless workloads directly across a distributed, content-addressed network.

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/membuss-protocol/membuss)](https://github.com/membuss-protocol/membuss/releases)
[![Docs Website](https://img.shields.io/badge/Documentation-Live_Docs_Hub-c8956c?logo=docusaurus&logoColor=white)](https://membuss-docs.vercel.app/)

[Website](https://membuss-docs.vercel.app/) · [Documentation](https://membuss-docs.vercel.app/docs/getting-started/introduction) · [Download Releases](https://github.com/membuss-protocol/membuss/releases) · [Desktop App](https://github.com/membuss-protocol/membuss/releases/tag/v2.9.0)

</div>

---

## 🌐 Network Overview

<div align="center">

<table align="center">
<tr>
<td align="center">

```
                         ┌──────────────────────────────┐
                         │   P2P Swarm (libp2p + DHT)   │
                         │  Device A ◄───► Device B     │
                         └──────────────┬───────────────┘
                                        │
                                        ▼
                         ┌──────────────────────────────┐
                         │   Membuss Network Engine     │
                         └──────┬────────────────┬──────┘
                                │                │
           ┌────────────────────┴───┐        ┌───┴────────────────────┐
           ▼                        ▼        ▼                        ▼
┌──────────────────────┐ ┌──────────────────────┐ ┌──────────────────────┐
│ Content-Addressed    │ │ Reed-Solomon 10+4    │ │ MemEdge Serverless   │
│ Merkle DAGs (MIDs)   │ │ Mathematical Parity  │ │ Go (WASI) & JS Engine│
└──────────────────────┘ └──────────────────────┘ └──────────────────────┘
```

</td>
</tr>
</table>

</div>

| Subsystem | Core Technologies | Primary Capability |
|---|---|---|
| **🌐 Swarm Networking** | `libp2p` · `Mem-DHT` · `PEX` | Multi-transport mesh (TCP, QUIC, WebSocket) with autonomous peer discovery |
| **📦 Resilient Storage** | `Pebble LSM` · `Reed-Solomon 10+4` · `BLAKE3` | Content-addressed Merkle DAGs surviving 40% simultaneous peer loss |
| **⚡ Serverless Compute** | `MemEdge` · `Wazero (WASI)` · `Goja (JS)` | Microsecond cold starts (`<0.5ms`) with 3-Tier Fair Compute Scheduling |

---

## 💡 Why Membuss?

Modern web and mobile applications depend almost exclusively on centralized cloud servers. This architecture introduces:

- **Single Points of Failure**: Outages in centralized cloud regions take down thousands of services at once.
- **Data Availability Crises**: In legacy P2P networks (like IPFS or BitTorrent), files vanish the moment seeders or origin pinners disconnect.
- **Bandwidth & Infrastructure Costs**: Centralized bandwidth and compute pricing scale exponentially with data throughput.
- **Rigid Edge Options**: Running serverless functions close to users usually requires vendor lock-in to proprietary cloud edges.

**Membuss takes a unified approach.** It turns ordinary connected devices into a shared substrate of resilient storage and distributed computation:

- **Protocol-Level Data Survival**: Every piece of data is split into **Reed-Solomon 10+4 erasure shards** at ingestion. Even if 40% of the nodes storing a file disconnect simultaneously, the data is mathematically reconstructed without loss.
- **Stateless Edge Execution**: Serverless functions run directly on the P2P edge with microsecond cold starts using pure-Go WebAssembly (Wazero) and JavaScript (Goja) engines.

---

## 📦 What is Membuss?

Membuss is a modular, decentralized infrastructure layer built in Go. It provides the core building blocks for:

- **Content-Addressed Storage**: Files and directories are chunked, hashed with BLAKE3, and structured into cryptographic Merkle DAGs identified by a **MID** (e.g. `mem1z4a2...`).
- **Resilient Erasure Coding**: Single-pass Reed-Solomon 10+4 encoding protects data against peer churn and drive failures without requiring full duplicates.
- **High-Throughput Block Exchange (Memex v2)**: Multiplexed block transfer protocol over libp2p streams featuring AIMD sliding window flow control and peer wantlist negotiation.
- **Serverless Edge Compute (MemEdge)**: Stateless execution of Go/WASI and JavaScript functions with 3-Tier Fair Compute Scheduling (Publisher $\rightarrow$ Peer $\rightarrow$ Gateway).
- **Mutable Pointers (MemNS)**: Cryptographically signed Ed25519 pointers allowing dynamic naming (`memns://my-app`) without changing content addresses.
- **Public Gateway & CDN (MemGate)**: Built-in HTTP gateway supporting RFC 7233 byte-range streaming, live Web Explorer, and edge function execution over standard web ports.

---

## ⚙️ How It Works

```
1. Ingestion & Chunking ──► Adaptive procedural sizing (256 KiB to 4 MiB)
2. Hashing & Merkle DAG ──► BLAKE3 parallel multihashing (0x1e) into UnixFS hierarchy
3. Erasure Coding       ──► 10 Data + 4 Parity shards generated in-memory (RAM)
4. Local Blockstore     ──► Pebble LSM SSTables with Counting Bloom Filter index
5. P2P Swarm Exchange   ──► Announced to Mem-DHT & transferred via Memex v2 streams
6. Serverless Compute   ──► Stateless on-demand execution via MemEdge (WASI / JS)
```

---

## ✨ Features

- **Decentralized P2P Networking**: Built on libp2p with TCP, QUIC, and WebSocket transports.
- **Content Addressing (MID)**: Cryptographically verifiable, multihash-based identifiers.
- **Adaptive Block Sizing**: BitTorrent-style procedural chunk sizing (256 KiB up to 4 MiB) to eliminate database write freezes on multi-gigabyte files.
- **Reed-Solomon 10+4 Erasure Coding**: Transparent shard fetch and background self-healing repair workers.
- **Pebble LSM Storage Engine**: High-throughput storage with in-memory Counting Bloom Filters for $O(1)$ additions and deletions.
- **MemEdge Serverless Compute**: Microsecond cold starts ($<0.5\text{ms}$) running WebAssembly (WASI) and ECMAScript 5.1/6.
- **3-Tier Fair Compute Scheduling**: Routes execution to the content publisher first, then connected edge peers, and finally local gateway sandboxes.
- **Public HTTP CDN Gateway**: Stream 4K video, audio, and web assets via standard HTTP with full byte-range seek support.
- **Built-in Web Explorer**: SvelteKit-powered dashboard for inspecting DAG blocks, peer topology, network telemetry, and node status.
- **Cross-Platform Desktop Application**: Native desktop GUI with automatic multi-version upgrades/downgrades and rollback safety.

---

## 🚀 Quick Start

### Option 1: Desktop GUI (Recommended for Users)

Membuss includes an all-in-one graphical desktop application for Windows, Linux, and macOS powered by Wails v2:

1. Download the latest installer from [GitHub Releases](https://github.com/membuss-protocol/membuss/releases/latest).
2. Launch **Membuss Desktop** — the local daemon, gateway, and visual explorer start automatically.

---

### Option 2: CLI & Node Daemon (For Developers & Servers)

#### 1. Build from Source

```bash
# Clone repository
git clone https://github.com/membuss-protocol/membuss.git
cd membuss

# Build unified executable
go build -o membuss ./cmd/membuss
```

#### 2. Initialize and Start Node

```bash
# Initialize data directory (~/.membuss)
./membuss init

# Start local daemon & HTTP gateway
./membuss daemon
```

#### 3. Store and Retrieve Content

```bash
# Ingest a file into the network
./membuss add ./video.mp4
# Output: membafzbeidr5pk22uidyjnsay6lgrlkcdx7dcrvuimfnl4t5v4otdmbyfiugm (MID)

# Retrieve content by MID
./membuss get membafzbeidr5pk22uidyjnsay6lgrlkcdx7dcrvuimfnl4t5v4otdmbyfiugm ./downloaded.mp4

# Stream directly to stdout
./membuss cat <MID> | ffplay -
```

---

## 📖 Practical Examples

### 1. Media Streaming via Mem-Gate HTTP CDN

Every node includes an HTTP gateway (default port `:8080`). You can stream video or host websites directly from a content address:

```bash
# Stream 4K video with seek/range support in any browser:
http://localhost:8080/mem/<MID>/video.mp4

# Inspect the Merkle DAG structure in the Web Explorer:
http://localhost:8080/explorer/mid/<MID>
```

---

### 2. Writing a MemEdge Serverless Function

Deploy an edge function that executes on-demand across the network without managing servers:

#### JavaScript (`router.js`)

```javascript
export default function handler(req) {
    const { method, path, query } = req;

    if (path === "/healthz") {
        return {
            status: 200,
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ status: "healthy", runtime: "MemEdge-Goja" })
        };
    }

    if (path === "/convert" && method === "GET") {
        const usd = parseFloat(query.usd || "100");
        const eur = usd * 0.92;
        return {
            status: 200,
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ usd, eur, rate: 0.92 })
        };
    }

    return { status: 404, body: "Route Not Found" };
}
```

#### Deploy & Execute

```bash
# 1. Add function to Membuss
./membuss add router.js
# Output: mem1z4a2... (MID)

# 2. Execute via HTTP Gateway
curl "http://localhost:8080/mem/mem1z4a2.../convert?usd=250&exec=true"
# Output: {"usd":250,"eur":230,"rate":0.92}
```

---

## 🏛️ System Architecture

<div align="center">

<table align="center">
<tr>
<td align="center">

```
┌────────────────────────────────────────────────────────────────────────┐
│                        Applications & Interfaces                       │
│           Web Explorer (SvelteKit)  ·  Desktop GUI (Wails v2)          │
├────────────────────────────────────────────────────────────────────────┤
│                       Serverless Compute Layer                         │
│       MemEdge Engine: Wazero (WASI Preview 1) & Goja (JavaScript)      │
├────────────────────────────────────────────────────────────────────────┤
│                           Application APIs                             │
│      Mem-Gate HTTP CDN (:8080)  ·  Node API (:5001)  ·  gRPC (:50051)  │
├────────────────────────────────────────────────────────────────────────┤
│                       Routing & Block Exchange                         │
│           Memex v2 Protocol  ·  Mem-DHT Kademlia  ·  PEX Gossip        │
├────────────────────────────────────────────────────────────────────────┤
│                           Networking Layer                             │
│                libp2p (TCP, QUIC, WebSocket Transports)                │
├────────────────────────────────────────────────────────────────────────┤
│                      Storage & Cryptographic Core                      │
│        Pebble LSM Blockstore  ·  Reed-Solomon 10+4  ·  BLAKE3 DAG      │
└────────────────────────────────────────────────────────────────────────┘
```

</td>
</tr>
</table>

</div>

---

## ⚖️ Membuss vs Existing Systems

| Feature | BitTorrent | IPFS (Kubo) | Cloud (AWS / Cloudflare) | Membuss |
|---|---|---|---|---|
| **P2P Swarm Distribution** | ✅ Yes | ✅ Yes | ❌ No (Centralized) | ✅ **Yes (Memex v2)** |
| **Cryptographic Content Addressing** | ⚠️ Torrent InfoHash | ✅ Yes (CID) | ❌ No (URL / DNS) | ✅ **Yes (MID)** |
| **Data Survives Offline Seeders** | ❌ No (Dead Torrents) | ❌ No (Vanishes without Pin) | ✅ Yes (SLA) | ✅ **Yes (Reed-Solomon 10+4 + Anchors)** |
| **Single-Pass Erasure Coding** | ❌ No | ❌ No | ⚠️ Internal Only | ✅ **Yes (SIMD Galois Field)** |
| **Native HTTP Video Streaming (RFC 7233)** | ⚠️ Requires Torrent Client | ⚠️ High Gateway Latency | ✅ Yes | ✅ **Yes (Instant Range Seek)** |
| **Serverless Edge Compute** | ❌ No | ❌ No | ✅ Yes (Cloud Functions) | ✅ **Yes (MemEdge WASI & JS)** |
| **Sub-millisecond Cold Starts** | N/A | N/A | ❌ 50–300ms | ✅ **< 0.5ms (Goja / Wazero)** |
| **Zero Infrastructure Invoices** | ✅ Yes | ✅ Yes | ❌ High Bandwidth Costs | ✅ **Yes (100% Permissionless)** |

---

## 💼 Real-World Use Cases

Membuss unifies the swarm distribution of **BitTorrent**, the content-addressing of **IPFS**, the low-latency caching of **Cloudflare**, and the serverless execution of **AWS Lambda** into a single, cohesive P2P protocol:

### 1. ⚡ Swarm-Assisted Media Streaming & Content Delivery (Decentralized CDN)
Deliver 4K/8K video, audio, gaming patches, and multi-gigabyte files directly across a peer swarm without cloud bandwidth invoices. With built-in **RFC 7233 byte-range seek** on the HTTP gateway (`Mem-Gate`), any standard web browser or video player can stream content instantly from a cryptographic MID.

### 2. 🛡️ Permanent Disaster-Resilient Archiving (The "Immortal Seeder")
Unlike BitTorrent (where torrents die when seeders leave) or IPFS (where unpinned data vanishes), Membuss encodes all data with **Reed-Solomon 10+4 erasure coding** and Anchor Node mirroring. Scientific datasets, legal records, open-source repositories, and digital archives survive even if 40% of the network goes offline simultaneously.

### 3. 🚀 Full-Stack Decentralized Web Applications (dApps)
Host entire modern web applications (React, Svelte, Vue, WASM) and their dynamic backend APIs from a single content address. With **MemNS mutable naming** (`memns://my-app`), you can update your code and frontend without breaking consumer URLs or relying on centralized DNS/hosting providers.

### 4. 🧠 Decentralized AI & Foundation Model Distribution
Shard and distribute multi-gigabyte AI weights (LLMs, Whisper, Stable Diffusion) across edge nodes. Edge devices can fetch verified shards over high-speed **Memex v2 streams** and execute lightweight preprocessing, tokenization, or vector similarity math using **MemEdge WebAssembly (WASI)**.

### 5. ⚙️ Serverless Edge Microservices & Dynamic Webhooks
Replace centralized cloud functions with decentralized edge compute. Run API gateways, currency converters, cryptographic signature verifiers, image transforms, and webhook handlers with sub-millisecond cold starts (`<0.5ms`) and zero recurring infrastructure bills.

### 6. 🔄 Censorship-Resistant P2P File Sharing & Collaboration
Share massive file collections, source code archives, and multimedia libraries peer-to-peer. Downloads automatically pull simultaneously from DHT providers, local LAN peers, and network anchors at wire speed with cryptographic BLAKE3 verification on every chunk.

---

## 🗺️ Project Roadmap

- [x] Content-addressed Merkle DAG storage with BLAKE3 multihashes
- [x] Single-pass Reed-Solomon 10+4 erasure coding and background repair worker
- [x] Adaptive procedural block sizing (256 KiB up to 4 MiB)
- [x] Memex v2 multiplexed block exchange protocol with AIMD flow control
- [x] MemEdge Serverless Engine (Go/WASI via Wazero & JS via Goja)
- [x] Mem-Gate HTTP gateway with RFC 7233 byte-range streaming
- [x] Cross-platform desktop application with atomic multi-version installer
- [x] SvelteKit-powered Web Explorer
- [ ] Distributed storage provider incentive economics
- [ ] Dynamic WebAssembly WASI socket extension support
- [ ] Browser-native WebRTC transport gateway bridge

---

## 🔬 Project Status

> [!NOTE]
> Membuss is under active development. While the core storage engine, erasure coding, Memex v2, and MemEdge runtimes are feature-complete and tested across multiple operating systems, network APIs and protocol specifications may continue to evolve before `v3.0.0`.

---

## 🤝 Contributing

Contributions, bug reports, and architectural proposals are welcome!

Please check out our [Contribution Guidelines (CONTRIBUTING.md)](CONTRIBUTING.md) for local setup, development workflows, testing rules, and Pull Request guidelines.

For technical specifications and protocol architecture blueprints, visit our [Documentation Hub](https://membuss-docs.vercel.app/).

---

## 📜 License

Membuss is open-source software released under the [Apache 2.0 License](LICENSE).
