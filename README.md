<div align="center">

# 🌌 Membuss Network

### A Decentralized, Content-Addressed Storage & Delivery Infrastructure

Decentralized storage and high-speed streaming built on protocol-level Reed-Solomon erasure coding, parallel BLAKE3 Merkle DAGs, and automatic content persistence.

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/nnlgsakib/membuss)](https://github.com/nnlgsakib/membuss/releases)
[![Docs Website](https://img.shields.io/badge/Documentation-Live_Docs_Hub-c8956c?logo=docusaurus&logoColor=white)](https://membuss-docs.vercel.app/)

[🌐 Interactive Documentation Hub](https://membuss-docs.vercel.app/) • [📦 Precompiled Releases](https://github.com/nnlgsakib/membuss/releases) • [🖥️ Desktop GUI App](#desktop-gui-recommended)

</div>

---

## ⚡ What is Membuss?

**Membuss** is a next-generation decentralized content storage and streaming protocol designed to solve the **data availability crisis** inherent in peer-to-peer file networks.

Traditional decentralized networks externalize content persistence entirely to end-users:
- **IPFS**: Files vanish permanently as soon as origin providers stop pinning CIDs.
- **BitTorrent**: Torrents die the moment the last seeder goes offline.

**Membuss solves this at the protocol layer**: Every payload is automatically sharded into **Reed-Solomon 10+4 erasure pieces** at ingestion, and dedicated **Anchor Nodes** automatically mirror announced content. Data survives not because someone remembered to pin it, but because disappearance is mathematically impossible.

```
Ingest (256KB) ──► BLAKE3 Parallel Pool ──► MemFS DAG ──► Reed-Solomon 10+4 ──► Pebble LSM Store ──► Memex Streaming
```

---

## 🚀 Key Problems Solved

| Problem in Legacy P2P | The Membuss Protocol Solution | Operational Benefit |
|---|---|---|
| ❌ **Data Loss on Disconnect** | Protocol-level **Reed-Solomon 10+4** erasure coding + **Anchor Auto-Mirroring** | Payloads survive even if 40% of storing peers go offline simultaneously |
| ❌ **Slow Write Ingestion** | **Pebble DB Hybrid SSTable Storage** ($<1$ MiB blocks stored directly in LSM SSTables) | **50x–100x write throughput boost** by eliminating OS filesystem inode exhaustion |
| ❌ **High DHT Overhead** | **Mem-Herald 16-Group Incremental Announce Protocol** | **90% reduction in DHT network gossip traffic** compared to full re-announce bursts |
| ❌ **Slow Deletion & Scans** | In-memory 8-bit saturating **Counting Bloom Filter** | **$O(1)$ constant-time additions and deletions** without DB compaction scans |
| ❌ **CPU Hashing Bottlenecks** | Multi-threaded `BuildParallel` worker pool using **BLAKE3 (`0x1e`)** | Full hardware utilization of AVX-512 / NEON vector CPU instructions |

---

## 📖 Deep Dive Documentation Hub

Explore the full interactive documentation website featuring deep-dive technical architecture blueprints, mathematical proofs, sequence flowcharts, protocol specifications, and API references:

> 🔗 **[Explore Full Interactive Documentation Website](https://membuss-docs.vercel.app/)**

### Highlights in the Documentation
- **[Executive System Thesis](https://membuss-docs.vercel.app/docs/getting-started/introduction)** — Core protocol philosophy, problem analysis, and market comparison.
- **[System Architecture Blueprint](https://membuss-docs.vercel.app/docs/architecture/overview)** — Interactive Mermaid sequence diagrams & end-to-end data pipelines.
- **[Reed-Solomon Erasure Coding](https://membuss-docs.vercel.app/docs/architecture/erasure-coding)** — SIMD Galois Field $GF(2^8)$ matrix arithmetic specifications.
- **[Pebble Hybrid Store & Bloom Filter](https://membuss-docs.vercel.app/docs/low-level-specs/pebble-hybrid-store)** — LSM SSTable engine & $O(1)$ saturating counter math.
- **[Memex v2 Protocol Spec](https://membuss-docs.vercel.app/docs/core-protocols/memex)** — Multiplexed libp2p block transfer & AIMD sliding window flow control.
- **[APIs & Protobuf Specs](https://membuss-docs.vercel.app/docs/apis-and-interfaces/grpc-api)** — gRPC contracts, Node REST API (`/api/v1`), and Mem-Gate HTTP CDN (`:8080`).

---

## ⚡ Quick Start & Installation

### Desktop GUI (Recommended)

Membuss includes a cross-platform graphical desktop application powered by **Wails v2** (Go + React / TypeScript).

- **Windows**: Download `Membuss-v2.3.0-windows-amd64-installer.exe` or `.zip`.
- **Linux**: Download `Membuss-v2.3.0-linux-amd64.AppImage` or `.tar.gz`.

👉 **[Download Precompiled Executables](https://membuss-docs.vercel.app/downloads)**

---

### Single-Binary CLI & Node Daemon

For server deployments, Docker containers, or command-line power users:

```bash
# 1. Clone repository
git clone https://github.com/nnlgsakib/membuss
cd membuss

# 2. Build single executable (daemon + CLI + bundled Web Explorer)
make build

# 3. Initialize data directory & start node daemon
./bin/membuss init
./bin/membuss daemon start

# 4. Ingest content & receive MID (Content Identifier)
./bin/membuss add ./video.mp4
# -> membafzbeidr5pk22uidyjnsay6lgrlkcdx7dcrvuimfnl4t5v4otdmbyfiugm
```

---

## 🏛️ Ecosystem & Future Capabilities

- **Mem-Gate HTTP CDN Gateway**: Built-in HTTP gateway (`:8080`) supporting RFC 7233 byte-range streaming for seamless 4K video playbacks.
- **MemNS Cryptographic Mutable Pointers**: Ed25519 signed pointers allowing dynamic updates without altering root content MIDs.
- **Mem-Git Version Control**: Content-addressed Merkle DAGs enabling native Git-style snapshotting and diff tracking.
- **Universal Plugin System**: Custom storage lifecycle hooks (`StorageHooks`) and dynamic API extensions without modifying protocol core code.

---

## 📜 License

Membuss is open-source software licensed under the [Apache 2.0 License](LICENSE).
