<div align="center">

# Membuss

### A decentralized content-addressed storage and delivery network.

Decentralized storage and delivery built on erasure coding, streaming block exchange, automatic content persistence, and dynamic plugin extensions.

IPFS loses content when providers leave. BitTorrent needs seeders online. Membuss encodes redundancy into the protocol itself.

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/nnlgsakib/membuss)](https://github.com/nnlgsakib/membuss/releases)

</div>

---

## Table of Contents

- [Why Membuss?](#why-membuss)
- [How It Works](#how-it-works)
- [Architecture](#architecture)
- [Quick Start](#quick-start)
  - [Desktop GUI (Recommended)](#desktop-gui-recommended)
  - [CLI & Daemon (Servers & Power Users)](#cli--daemon-servers--power-users)
- [Desktop Application](#desktop-application-)
- [Feature Reference](#feature-reference)
- [Modular Plugin System](#modular-plugin-system-)
- [HTTP API](#http-api)
- [CLI Reference](#cli-reference)
- [Content Identifiers](#content-identifiers)
- [Operating a Node](#operating-a-node)
- [Development](#development)
- [License](#license)

---

## Why Membuss?

Content-addressed networks share one structural weakness: **availability depends on humans remembering to keep data online.** IPFS content disappears when its providers shut down. BitTorrent torrents die when the last seeder leaves. Both push the burden of persistence onto the user.

Membuss takes a different position. Every leaf block is erasure-coded at the storage layer, and dedicated Anchor Nodes automatically mirror announced content. Data survives not because someone remembered to pin it, but because the protocol makes disappearance structurally unlikely.

### Membuss vs IPFS vs BitTorrent

| | Membuss | IPFS | BitTorrent |
|---|---|---|---|
| **User Experience** | Native Desktop App + CLI + Web Explorer | CLI + Third-Party Apps | Desktop Torrent Clients |
| **Erasure coding** | Reed-Solomon `10+4` at the storage layer | None — availability depends on providers | Optional parity files, not protocol-level |
| **Content persistence** | Anchor Nodes auto-mirror announced content | Manual pinning required | Seeders must stay online |
| **Block streaming** | `FetchStream` returns an `io.Reader` immediately — first bytes arrive as the first block resolves | Waits for the DAG before delivering data | Piece-level streaming only |
| **Block verification** | BLAKE3 (default) / SHA-256 verified before every block hits disk | Trusts the blockstore | Piece hashes only |
| **Congestion control** | AIMD sliding window per peer | None | uTP at transport level |
| **Provider selection** | Ranked by latency + bandwidth + freshness | Unranked list | Unranked list |
| **Reprovide cost** | Entry-node-only announcing, split into incremental groups | Full re-announce every cycle | N/A (tracker-based) |
| **Mutable naming** | MemNS with PubSub real-time updates + weighted routes | IPNS (single target, polling) | N/A |
| **Plugin Extensions** | Zero-core-modification hooks + custom REST/gRPC/CLI APIs | Limited IPFS plugins | N/A |

---

## How It Works

```
 add ──►  Chunk  ──►  Parallel Hash  ──►  MemFS / DAG  ──►  Erasure-code leaves  ──►  Store  ──►  Announce
            │               │                 │                    │                  │            │
      256 KiB blocks   BLAKE3 (default)   FILE / DIR         Reed-Solomon 10+4      Pebble      Kademlia
      (fixed default)    parallel pool    envelopes          (lose any 4 of 14)   hybrid DB     DHT

 get ──►  DHT: who has the root?  ──►  Memex session  ──►  walk DAG, pull blocks  ──►  verify  ──►  reassemble
```

1. **Chunk** — the input is split into content blocks (fixed 256 KiB by default; Rabin and FastCDC are also available).
2. **Hash** — each block is hashed concurrently in parallel (BLAKE3 by default, configurable to SHA-256 or SHA-512), wrapped in a CIDv1 multihash, and tagged with a codec to form a **MID**.
3. **Structure** — files and directories become a MemFS tree (`FILE` / `DIR` / `SYMLINK` / `METADATA` nodes) over the content-addressed block graph, with automatic deduplication.
4. **Erase** — every raw leaf is Reed-Solomon encoded into `10 data + 4 parity` shards; any 4 of the 14 can be lost and the block still recovers.
5. **Store** — blocks are stored in Pebble SSTables (with large blobs >= 1 MB on disk), accelerated by an in-memory Counting Bloom Filter for O(1) insertions, deletions, and lookup.
6. **Announce** — provider records for the content's entry nodes are published to the Kademlia DHT and gossiped over PEX.
7. **Fetch** — a peer locates a provider of the root, opens a Memex session, and streams the whole tree from that provider — pulling child blocks over the same connection rather than re-querying the DHT per block.
8. **Reprovide** — Mem-Herald periodically re-announces entry nodes, split across incremental groups to keep DHT traffic low.
9. **Mirror** — Anchor Nodes optionally sync all announced content, so it persists after the original providers leave.

---

## Architecture

```
                          ┌─────────────────────────────────────────────┐
                          │                  Interfaces                  │
                          │   Desktop GUI (Wails)  ·   CLI   ·   gRPC    │
                          │        Node API   ·   Mem-Gate Explorer      │
                          └───────────────┬─────────────────────────────┘
                                          │
        ┌─────────────────┬───────────────┼───────────────┬──────────────────┐
        ▼                 ▼               ▼               ▼                  ▼
   ┌──────────┐     ┌────────────┐  ┌───────────┐  ┌────────────┐    ┌──────────────┐
   │  MemFS   │     │    DAG     │  │  Erasure  │  │   MemNS    │    │  Mem-Herald  │
   │ file/dir │     │  builder / │  │  10+4 RS  │  │  mutable   │    │  reprovider  │
   │  layer   │     │  resolver  │  │  shards   │  │  naming    │    │              │
   └────┬─────┘     └─────┬──────┘  └─────┬─────┘  └─────┬──────┘    └──────┬───────┘
        │                 │               │              │                  │
        └─────────────────┴───────┬───────┴──────────────┴──────────────────┘
                                   ▼
                     ┌──────────────────────────┐        ┌──────────────────────┐
                     │      Pebble blockstore    │        │      Networking       │
                     │  bloom · verify · seal/GC │        │  Memex v2 · Mem-DHT   │
                     │  /b/ /d/ /s/ /m/          │◄──────►│  Mem-PEX · libp2p     │
                     └──────────────────────────┘        └──────────────────────┘
                                   ▲
                                   │
                     ┌──────────────────────────┐
                     │  Universal Plugin System │
                     │  Hooks · REST · CLI APIs │
                     └──────────────────────────┘
```

---

## Quick Start

Not everyone wants to use terminal commands to run a decentralized node. Membuss offers a rich **Desktop GUI** for casual users and content creators, alongside a single-binary **CLI/Daemon** for developers and server operators.

### Desktop GUI (Recommended)

The easiest way to run Membuss on Windows, macOS, or Linux.

#### Download Pre-Built Executables

1. Download the latest installer or executable for your OS from [GitHub Releases](https://github.com/nnlgsakib/membuss/releases).
2. Run the application to launch the graphical node manager.

#### Build Desktop App from Source

**Requirements:** Go 1.25+, Node.js 18+, and Wails CLI (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`).

```bash
git clone https://github.com/nnlgsakib/membuss
cd membuss/desktop
wails build
# -> build/bin/membuss-desktop (executable)
```

---

### CLI & Daemon (Servers & Power Users)

For head-less server deployments, Docker containers, or terminal power users.

#### Build CLI Binary

**Requirements:** Go 1.25+ and Node.js 18+ (the web explorer is bundled into the daemon at build time).

```bash
git clone https://github.com/nnlgsakib/membuss
cd membuss
make build
# -> bin/membuss (single binary: daemon + CLI)
```

#### Run Node Daemon

```bash
# Initialize data directory & default configuration
./bin/membuss init

# Run the node in the foreground
./bin/membuss daemon start

# Mem-Gate gateway : http://127.0.0.1:8080
# Node API         : http://127.0.0.1:5001
# gRPC             : 127.0.0.1:50051
```

#### Basic CLI Commands

```bash
# Upload a file and get its MID
./bin/membuss add ./video.mp4
# -> membafzbeidr5pk22uidyjnsay6lgrlkcdx7dcrvuimfnl4t5v4otdmbyfiugm

# Upload a folder as a browsable MemFS tree
./bin/membuss add ./my-site

# Retrieve content
./bin/membuss get membafzbeidr5pk2… -o output.mp4

# Inspect live node & plugin hook metrics
./bin/membuss inspector stats
```

---

## Desktop Application 🖥️

The Membuss Desktop Application (`desktop/`) provides a full graphical user interface powered by Wails and Svelte.

### Key Desktop Features

1. **One-Click Node Control**: Start, stop, and inspect the local Membuss node daemon effortlessly without typing terminal commands.
2. **Visual File & Folder Uploads**: Drag and drop files or directories into the window to slice, hash, erasure-code, seal, and generate MIDs (`mem1...`).
3. **Interactive 3D Globe & Geolocation Map**: Real-time visualization of connected swarm peers, displaying geographic locations, latency, and PEX routing metadata on an interactive 3D globe.
4. **Embedded MemFS & Web Explorer**: Browse pinned content, view Merkle DAG structures, inspect raw leaves, and stream audio/video files directly inside the app.
5. **System Tray Background Mode**: Minimizes to the desktop system tray, keeping your node seeding, auto-mirroring, and servicing network block requests in the background.
6. **Automatic Updates**: Native background version check with zero-downtime updates directly from GitHub releases.

---

## Feature Reference

### Erasure Coding

Every raw leaf is Reed-Solomon encoded into `10 data + 4 parity` shards (~40% overhead) before it leaves a node. Content survives the loss of any 4 of 14 shards. Redundancy is structural, not administrative — there is no pinning service to sign up for.

### Anchor Nodes

Optional full-mirror nodes that discover announced content via the DHT, pull it via Memex, seal it locally, and re-announce it. Content stays reachable even after the original providers disconnect.

### Memex v2 — Block Exchange

A high-performance bidirectional block-exchange protocol over persistent libp2p streams:

- **Wantlist Exchange & Opportunistic Delivery** — connected peers exchange active wantlists over persistent streams. When a node stores or receives a block, it immediately pushes the block to interested peers without waiting for round-trip pull requests.
- **Two-Phase Negotiation (`WANT_HAVE` / `HAVE`)** — requesting nodes query `WANT_HAVE` first; holding peers reply with lightweight `HAVE` frames so the requester targets only the fastest peer, preventing duplicate block downloads.
- **Immediate Cancel Broadcasting** — as soon as a block resolves locally, the engine broadcasts `Cancel` frames to active streams, clearing remote peer queues instantly.
- **Streaming Assembly** — `FetchStream()` returns an `io.Reader` immediately while blocks resolve in the background; first bytes are available as soon as the first block arrives.
- **DAG-Aware Walking** — once a provider of the root is found, the tree (MemFS dirs, file envelopes, raw leaves) is pulled over the same connection instead of re-querying the DHT per block.
- **Persistent Stream Pool & AIMD Congestion Control** — stream connections are multiplexed and reused with adaptive per-peer sliding windows.
- **Parallel Verification & DB Batching** — a dedicated worker pool verifies SHA-256 hashes off the hot path before batch-writing to storage.
- **Content Metadata Immutability** — content-addressed files and directories are strictly immutable, guaranteeing cryptographically verifiable data integrity.

### Mem-DHT — Enhanced Kademlia

Provider discovery ranked by a composite score of latency, bandwidth, and freshness, backed by a CID cache and custom record validators for the `membuss` and `memns` namespaces. Runs on a Pebble-backed datastore.

### Mem-Gate — HTTP Gateway + CDN

A full HTTP gateway at `/mem/{MID}`:

- **Range requests** — `Range: bytes=N-M` returns `206 Partial Content`; video and audio play inline and seek correctly at any size.
- **ETag + immutable caching** — content-addressed responses are cached aggressively.
- **Directories & websites** — directory listings (HTML or `?format=json`) with automatic `index.html` serving and SPA fallback.
- **Custom domains** — resolve `example.com` → MID via DNS-link records.

### MemFS — Files & Directories

A UnixFS-equivalent layer: files chunk into raw leaves under a `FILE` envelope, directories are ordered `DIR` nodes, and every node is content-addressed so dedup, walk, seal, and GC apply uniformly.

---

## Modular Plugin System 🧩

Membuss features a dynamic plugin architecture (`pkg/plugin`) that enables custom logic, store interceptors, REST routes, gRPC services, and CLI subcommands to be added **without modifying core code**.

### Core Architecture

- **Single Entrypoint (`*plugin.Core`)**: Grants full read/write access to `Store`, `Host`, `DHT`, `Memex`, `PEX`, `Herald`, `Anchor`, `MemNS`, `Keyring`, and `Metrics`.
- **Universal Subsystem Hooks (`HookBus`)**:
  - `BeforeBlockPut` / `AfterBlockPut`: Intercept or transform blocks before persistence.
  - `BeforeBlockGet` / `AfterBlockGet`: Intercept or transform retrieved block bytes.
  - `AfterBlockDel`: Intercept block deletion.
  - `OnAnchorHold` / `OnAnchorSeal`: Intercept Anchor Node pinning events.
  - `OnPeerConnected` / `OnPeerDisconnected`: Track swarm peer connections.
- **Extension Registries**: Mount custom REST endpoints onto Gateway and Node API, define custom libp2p stream protocols, and register hierarchical Cobra CLI subcommands.

### Showcase Reference Plugin (`echo-inspector`)

Test live plugin hook execution and telemetry using the built-in inspector:

```bash
# Check plugin health
./bin/membuss inspector status

# View live daemon hook metrics (Block Puts, Gets, Bytes Processed)
./bin/membuss inspector stats

# Display real-time log of intercepted core events
./bin/membuss inspector events

# Execute a live storage hook mutation & retrieval verification test
./bin/membuss inspector test-hook
```

---

## Docker

```bash
# Single node
docker compose up -d

# 3-node local cluster (Node 1 = anchor + relay)
docker compose -f docker-compose.multi.yml up -d

# One-off container
make docker-run
```

The image is distroless and runs as a non-root user. Exposed ports: `4001` (libp2p TCP/QUIC), `4002` (WebSocket), `5001` (Node API), `8080` (Mem-Gate), `50051` (gRPC).

---

## Configuration

Configuration is a single YAML file (`membuss.yaml`); every field falls back to a sensible default:

```yaml
listen_addrs:
  - /ip4/0.0.0.0/tcp/4001
  - /ip4/0.0.0.0/udp/4001/quic-v1
  - /ip4/0.0.0.0/tcp/4002/ws

data_dir: ./data
gateway_addr: 127.0.0.1:8080
api_addr: 127.0.0.1:5001
grpc_addr: 127.0.0.1:50051

# Server Enable/Disable Toggles
# Disable individual servers (e.g. gateway-only CDN node, headless daemon, or pure relay node)
servers:
  gateway:
    enabled: true              # Mem-Gate HTTP gateway & Web Explorer
  node_api:
    enabled: true              # Local Node Control API (/api/v1)
  grpc:
    enabled: true              # CLI <-> Daemon gRPC service

# Persistence & Anchor Engine
anchor_mode: false            # true = mirror all announced content
anchor:
  max_storage: 100GB           # Human-readable storage limit (e.g. 500MB, 100GB, 1TB, or 0 = unlimited)
  fetch_concurrency: 8         # Parallel worker pool count for backlog discovery
  reacquire_batch_size: 32     # Round-robin sampling batch size per discovery round

# Modular Plugins
plugins:
  enabled: true
  active:
    - echo-inspector
  config:
    echo-inspector:
      log_level: info

# Observability
metrics_enabled: true         # Prometheus at /metrics
log_level: info
```

---

## HTTP API

### Mem-Gate — Public Gateway

| Method | Path | Description |
|--------|------|-------------|
| `GET`  | `/mem/{mid}` | Fetch resolved content (Range-aware) |
| `HEAD` | `/mem/{mid}` | Existence + metadata headers |
| `GET`  | `/mem/{mid}?format=dag-json` | DAG node as JSON |
| `GET`  | `/mem/{mid}/` | Directory listing (`?format=json` for JSON) |
| `GET`  | `/mem/{mid}/{path}` | File within a MemFS directory |
| `GET`  | `/memns/{name}` | Resolve a MemNS name |
| `GET`  | `/explorer/` | Web explorer |

### Node API — Local Control

| Method | Path | Description |
|--------|------|-------------|
| `POST`   | `/api/v1/add` | Upload a file (raw body or multipart) |
| `POST`   | `/api/v1/add/dir` | Upload a directory (multipart) |
| `GET`    | `/api/v1/get/{mid}` | Fetch content |
| `GET`    | `/api/v1/ls/{mid}` | List a directory |
| `POST`   | `/api/v1/seal/{mid}` | Pin recursively |
| `DELETE` | `/api/v1/seal/{mid}` | Unpin |
| `GET`    | `/api/v1/stat/{mid}` | Size, block count, seal status |
| `GET`    | `/api/v1/peers` | Connected peers |
| `GET`    | `/api/v1/node/info` | Peer ID, addresses, version |
| `*`      | `/api/v1/inspector/*` | Plugin inspector endpoints |
| `GET`    | `/metrics` | Prometheus metrics |

---

## CLI Reference

`membuss` is a single binary that is both the node daemon and the operator client.

```bash
membuss <command> [flags]
```

| Command | Description |
|---------|-------------|
| `add <file-or-dir>` | Upload a file or directory, seal the root, print the MID |
| `get <MID> [-o file]` | Fetch content to a file or stdout (`--offset`, `--limit`) |
| `ls <MID>` | List a MemFS directory |
| `seal <MID>` | Pin (protect from GC) |
| `unseal <MID>` | Remove a pin |
| `stat <MID>` | Size, block count, seal status |
| `peers` | Show the PEX peer table |
| `dht peek <MID>` | Query the DHT for providers |
| `gc` | Run garbage collection |
| `anchor status` | Anchor engine statistics |
| `inspector stats/status/events/test-hook` | Plugin system telemetry & live test |
| `daemon start` / `status` | Manage the local daemon |
| `memns publish/resolve/log/delegate` | Mutable naming |
| `version` | Version and build info |

---

## Content Identifiers

A Membuss ID (**MID**) is a CIDv1 rendered with the literal `mem` prefix:

```
mem + b + base32lower( <version> <codec> <multihash> )
```

Example: `membafzbeidr5pk22uidyjnsay6lgrlkcdx7dcrvuimfnl4t5v4otdmbyfiugm`

---

## Operating a Node

**Run an Anchor Node.** Set `anchor_mode: true`. The node discovers announced content, mirrors it, and keeps it reachable after the origin leaves — the backbone of durability on a Membuss network.

Anchor Nodes feature:
- **Bloom Filter Delta Sync**: Exchanges inventory summaries to cut discovery bandwidth by >95%.
- **Storage Quotas & LRU Eviction**: Bounded disk storage (`max_storage_bytes`) with automated LRU eviction for auto-discovered content while protecting operator-pinned data.
- **Adaptive Dialing & Peer Reputation**: Measures EMA response latency and success rates to dial the fastest and healthiest peers first.
- **Prometheus Observability**: Exposes active worker saturation, fetch health, sync throughput, and storage utilization via `/anchor/status` and `/metrics`.

**Expose a public gateway.** Bind `gateway_addr` to a public interface, front it with a reverse proxy for TLS, and set `gateway_rate_limit_per_min`. Content-addressed responses are immutable and cache-friendly at the edge.

---

## Development

```bash
make build          # single membuss binary: node + CLI (CGO disabled)
make test           # go test ./... -count=1
make lint           # golangci-lint
make run-daemon     # run daemon with ./membuss.yaml
```

---

## License

[Apache License 2.0](LICENSE)
