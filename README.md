<div align="center">

# Membuss

### A decentralized content-addressed storage and delivery network.

Decentralized storage and delivery built on erasure coding, streaming block exchange, and automatic content persistence.

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
- [Feature Reference](#feature-reference)
- [Quick Start](#quick-start)
- [Docker](#docker)
- [Configuration](#configuration)
- [HTTP API](#http-api)
- [CLI](#cli)
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
| **Erasure coding** | Reed-Solomon `10+4` at the storage layer | None — availability depends on providers | Optional parity files, not protocol-level |
| **Content persistence** | Anchor Nodes auto-mirror announced content | Manual pinning required | Seeders must stay online |
| **Block streaming** | `FetchStream` returns an `io.Reader` immediately — first bytes arrive as the first block resolves | Waits for the DAG before delivering data | Piece-level streaming only |
| **Block verification** | SHA-256 verified before every block hits disk | Trusts the blockstore | Piece hashes only |
| **Congestion control** | AIMD sliding window per peer | None | uTP at transport level |
| **Provider selection** | Ranked by latency + bandwidth + freshness | Unranked list | Unranked list |
| **Reprovide cost** | Entry-node-only announcing, split into incremental groups | Full re-announce every cycle | N/A (tracker-based) |
| **Mutable naming** | MemNS with PubSub real-time updates + weighted routes | IPNS (single target, polling) | N/A |
| **Gateway** | Range requests, ETag caching, SPA fallback, custom domains | Basic HTTP gateway | N/A |
| **Peer exchange** | Signed records + reachability filtering | Unsigned, no filtering | N/A |

---

## How It Works

```
 add ──►  Chunk  ──►  Hash  ──►  MemFS / DAG  ──►  Erasure-code leaves  ──►  Store  ──►  Announce
            │           │              │                    │                  │            │
      256 KiB blocks  SHA-256      FILE / DIR         Reed-Solomon 10+4      Pebble      Kademlia
      (fixed default)   → MID      envelopes          (lose any 4 of 14)   blockstore      DHT

 get ──►  DHT: who has the root?  ──►  Memex session  ──►  walk DAG, pull blocks  ──►  verify  ──►  reassemble
```

1. **Chunk** — the input is split into content blocks (fixed 256 KiB by default; Rabin and FastCDC are also available).
2. **Hash** — each block is SHA-256 hashed, wrapped in a CIDv1 multihash, and tagged with a codec to form a **MID**.
3. **Structure** — files and directories become a MemFS tree (`FILE` / `DIR` / `SYMLINK` / `METADATA` nodes) over the content-addressed block graph, with automatic deduplication.
4. **Erase** — every raw leaf is Reed-Solomon encoded into `10 data + 4 parity` shards; any 4 of the 14 can be lost and the block still recovers.
5. **Store** — blocks are written to a Pebble-backed blockstore, accelerated by an in-memory bloom filter and verified on write.
6. **Announce** — provider records for the content's entry nodes are published to the Kademlia DHT and gossiped over PEX.
7. **Fetch** — a peer locates a provider of the root, opens a Memex session, and streams the whole tree from that provider — pulling child blocks over the same connection rather than re-querying the DHT per block.
8. **Reprovide** — Mem-Herald periodically re-announces entry nodes, split across incremental groups to keep DHT traffic low.
9. **Mirror** — Anchor Nodes optionally sync all announced content, so it persists after the original providers leave.

---

## Architecture

```
                          ┌─────────────────────────────────────────────┐
                          │                  Interfaces                  │
                          │   CLI   ·   gRPC   ·   Node API   ·  Mem-Gate │
                          │                    Desktop (Wails)           │
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
```

| Layer | Package | Responsibility |
|-------|---------|----------------|
| Content ID | `core/mid` | CIDv1-based `mem…` identifiers (raw / dag-pb / memfs codecs) |
| Chunking | `core/chunk` | Fixed, Rabin, and FastCDC chunkers |
| DAG | `core/dag` | Merkle DAG build + streaming resolve |
| Filesystem | `core/memfs` | UnixFS-equivalent file / directory tree |
| Erasure | `core/erasure`, `core/shard` | Reed-Solomon 10+4, consistent-hash shard ring |
| Storage | `core/store`, `core/db` | Pebble blockstore, bloom index, seal/GC |
| Naming | `core/memns`, `core/memlink` | Mutable names, DNS-link custom domains |
| Exchange | `net/memex_v2` | Streaming block exchange over libp2p |
| Discovery | `net/dht` | Enhanced Kademlia with ranked providers |
| Gossip | `net/pex` | Signed peer exchange |
| Reprovide | `net/herald` | Background re-announcer |
| Gateway | `gateway/memgate_v2`, `gateway/explorer` | HTTP CDN + web explorer |
| Persistence | `anchor` | Auto-mirroring Anchor Node engine |

---

## Feature Reference

### Erasure Coding

Every raw leaf is Reed-Solomon encoded into `10 data + 4 parity` shards (~40% overhead) before it leaves a node. Content survives the loss of any 4 of 14 shards. Redundancy is structural, not administrative — there is no pinning service to sign up for.

### Anchor Nodes

Optional full-mirror nodes that discover announced content via the DHT, pull it via Memex, seal it locally, and re-announce it. Content stays reachable even after the original providers disconnect.

### Memex v2 — Block Exchange

A bidirectional block-exchange protocol over libp2p streams:

- **Streaming assembly** — `FetchStream()` returns an `io.Reader` immediately while blocks resolve in the background; first bytes are available as soon as the first block arrives.
- **DAG-aware walking** — once a provider of the root is found, the whole tree (MemFS dirs, file envelopes, raw leaves) is pulled over the same session instead of re-querying the DHT per block.
- **Persistent stream pool** — connections are reused across sessions.
- **AIMD congestion control** — an adaptive per-peer sliding window that backs off on write errors.
- **Parallel verification** — a worker pool verifies SHA-256 hashes off the hot path.
- **Bloom-filter gossip** — nodes broadcast sealed-MID bloom filters so peers skip providers that don't have a wanted block.

### Mem-DHT — Enhanced Kademlia

Provider discovery ranked by a composite score of latency, bandwidth, and freshness, backed by a CID cache and custom record validators for the `membuss` and `memns` namespaces. Runs on a Pebble-backed datastore.

### Mem-PEX — Peer Exchange

A gossip protocol that periodically exchanges peer tables with random connected peers. Records are cryptographically signed (unsigned records are rejected), reachability-filtered, and persisted to disk across restarts.

### Mem-Gate — HTTP Gateway + CDN

A full HTTP gateway at `/mem/{MID}`:

- **Range requests** — `Range: bytes=N-M` returns `206 Partial Content`; video and audio play inline and seek correctly at any size.
- **ETag + immutable caching** — content-addressed responses are cached aggressively.
- **Directories & websites** — directory listings (HTML or `?format=json`) with automatic `index.html` serving and SPA fallback.
- **Custom domains** — resolve `example.com` → MID via DNS-link records.
- **Subdomain routing** — serve content at `mid.gateway.example` or via the path.

### MemFS — Files & Directories

A UnixFS-equivalent layer: files chunk into raw leaves under a `FILE` envelope, directories are ordered `DIR` nodes, and every node is content-addressed so dedup, walk, seal, and GC apply uniformly. Each file inside a directory remains independently addressable by its own MID.

### MemNS — Mutable Naming

Decentralized naming with DHT-stored, PubSub-updated records (no polling), weighted routes for traffic splitting, DNS-link fallback for IPFS migration, and delegated publishing. A bounded resolution depth prevents infinite chains.

### Mem-Herald — Reprovider

A background re-announcer with three strategies:

- **`roots`** *(default)* — announces content entry points (sealed roots plus reachable MemFS directory and file nodes), so everything stays discoverable without flooding the DHT with a record per leaf block.
- **`all`** — every block; used by Anchor Nodes that back up the whole network.
- **`shards`** — only the erasure shards this node owns, via a consistent-hash ring.

The reprovide cycle is split into incremental groups so each run announces only a fraction of the keyspace.

### Storage

- **Pebble blockstore** — an LSM key-value store with namespaced keys: blocks (`/b/`), DAG/MemFS nodes (`/d/`), seals (`/s/`), metadata (`/m/`).
- **Bloom filter** — `Has()` consults an in-memory bloom filter first; a "definitely absent" answer skips disk entirely.
- **Write verification** — `Put()` re-hashes bytes and rejects any block whose content does not match its MID.
- **Seal & GC** — sealed roots are pinned; unsealed content past a minimum age is collected on a schedule.

### Desktop App

A Wails-based GUI (`desktop/`) that starts and stops the daemon, embeds the web explorer with a globe and geolocation map, auto-updates from GitHub releases, and can keep the daemon alive after the window closes.

---

## Quick Start

### Build

Membuss builds with **CGO disabled**, producing static binaries.

**Requirements:** Go 1.25+, and Node.js 18+ (the explorer frontend is bundled into the daemon at build time).

```bash
git clone https://github.com/nnlgsakib/membuss
cd membuss
make build
# -> bin/membuss     (single binary: node + CLI)
```

`make build` compiles the SvelteKit explorer first, then the Go binary. `membuss` is one unified executable — it runs the node *and* acts as the operator CLI. To skip the frontend and build the binary alone:

```bash
CGO_ENABLED=0 go build -o bin/membuss ./cmd/membuss
```

### Run

```bash
# Initialize the data directory (identity + config), then run the node:
./bin/membuss init
./bin/membuss daemon start
# Mem-Gate gateway : http://127.0.0.1:8080
# Node API         : http://127.0.0.1:5001
# gRPC             : 127.0.0.1:50051
# libp2p           : tcp/4001, quic/4001, ws/4002

# The legacy standalone form is still supported:
./bin/membuss -config membuss.yaml
```

### Use

```bash
# Upload a file
./bin/membuss add ./video.mp4
# -> membafzbeidr5pk22uidyjnsay6lgrlkcdx7dcrvuimfnl4t5v4otdmbyfiugm

# Upload a directory (served as a browsable MemFS tree)
./bin/membuss add ./my-site

# Fetch content back
./bin/membuss get membafzbeidr5pk2… -o copy.mp4

# Browse in the explorer
# open http://127.0.0.1:8080/explorer/

# Stream directly from the gateway
# open http://127.0.0.1:8080/mem/membafzbeidr5pk2…
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

Configuration is a single YAML file; every field falls back to a sensible default. The most common fields:

```yaml
listen_addrs:
  - /ip4/0.0.0.0/tcp/4001
  - /ip4/0.0.0.0/udp/4001/quic-v1
  - /ip4/0.0.0.0/tcp/4002/ws

data_dir: ./data
gateway_addr: 127.0.0.1:8080
api_addr: 127.0.0.1:5001
grpc_addr: 127.0.0.1:50051

# Content persistence
anchor_mode: false            # true = mirror all announced content

# Reprovide (Mem-Herald)
reprovide_interval: 12h
reprovide_groups: 6           # split each cycle into N incremental runs
reprovide_strategy: roots     # roots | all | shards

# Security
api_key: ""                   # when set, Node API requires X-Membuss-Key

# Observability
metrics_enabled: true         # Prometheus at /metrics
log_level: info
```

The full schema — TLS, rate limiting, relay service, NAT, bloom sizing, DHT tuning, geolocation, and tunneling — lives in [`config/config.go`](config/config.go). A starter file is [`membuss.yaml`](membuss.yaml).

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
| `GET`  | `/memlink/{domain}` | Resolve a custom-domain link |
| `GET`  | `/explorer/` | Web explorer |
| `GET`  | `/healthz` | Liveness probe |

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
| `POST`   | `/api/v1/gc` | Run garbage collection |
| `DELETE` | `/api/v1/delete/{mid}` | Delete a block |
| `*`      | `/api/v1/memns/*`, `/keyring/*`, `/descriptor/*` | Naming, keys, portable descriptors |
| `GET`    | `/metrics` | Prometheus metrics |

Responses use a JSON envelope: `{"ok": true, "data": {…}}` or `{"ok": false, "error": "…"}`. When `api_key` is configured, requests must carry `X-Membuss-Key: <api_key>`.

---

## CLI

`membuss` is a single binary that is both the node and the operator client. The same executable runs the daemon (`membuss daemon start`) and drives a running node over gRPC / HTTP.

```
membuss <command> [flags]
```

| Command | Description |
|---------|-------------|
| `add <file-or-dir>` | Upload a file or directory, seal the root, print the MID |
| `get <MID> [-o file]` | Fetch content to a file or stdout (`--offset`, `--limit`) |
| `ls <MID>` | List a MemFS directory |
| `seal <MID>` | Pin (protect from GC) |
| `unseal <MID>` | Remove a pin |
| `delete <MID>` | Delete a block |
| `stat <MID>` | Size, block count, seal status |
| `peers` | Show the PEX peer table |
| `dht peek <MID>` | Query the DHT for providers |
| `gc` | Run garbage collection |
| `anchor status` | Anchor engine statistics |
| `ping [message]` | Connectivity probe |
| `daemon start` / `status` | Manage the local daemon |
| `keyring gen/list/export/import/rm` | Manage signing keys |
| `memns publish/resolve/log/delegate` | Mutable naming |
| `descriptor export/import/meta` | Portable `.mbuss` content descriptors |
| `version` | Version and build info |

---

## Content Identifiers

A Membuss ID (**MID**) is a CIDv1 rendered with the literal `mem` prefix:

```
mem + b + base32lower( <version> <codec> <multihash> )
```

Example: `membafzbeidr5pk22uidyjnsay6lgrlkcdx7dcrvuimfnl4t5v4otdmbyfiugm`

Codecs distinguish node types while sharing identical content-hash semantics:

| Codec | Value | Node type |
|-------|-------|-----------|
| raw | `0x55` | raw leaf block |
| dag-pb | `0x70` | Merkle-DAG internal node |
| memfs | `0x72` | MemFS `FILE` / `DIR` / `SYMLINK` / `METADATA` node |

Because the MID is the SHA-256 of the serialized node, identical content produces identical MIDs network-wide, and dedup, walk, seal, and GC all apply without special cases.

---

## Operating a Node

**Run an Anchor Node.** Set `anchor_mode: true` and `reprovide_strategy: all`. The node discovers announced content, mirrors it, and keeps it reachable after the origin leaves — the backbone of durability on a Membuss network.

**Expose a public gateway.** Bind `gateway_addr` to a public interface, front it with a reverse proxy for TLS, and set `gateway_rate_limit_per_min`. Content-addressed responses are immutable and cache-friendly at the edge.

**Secure the control plane.** The Node API and gRPC endpoints are administrative. Keep them bound to `127.0.0.1`, or set `api_key` and place them behind your own authentication if they must be remote.

**Observe.** Scrape `/metrics` with Prometheus (`metrics_enabled: true`) for provide/fetch counts, DHT activity, and store size. The daemon shuts down gracefully on `SIGINT`/`SIGTERM`.

---

## Development

```bash
make build          # single membuss binary: node + CLI (CGO disabled)
make test           # go test ./... -race -count=1
make lint           # golangci-lint
make proto          # regenerate protobuf bindings
make run-daemon     # run with ./membuss.yaml
make frontend-dev   # Vite dev server for the explorer
```

Requirements: Go 1.25+. The repository is a single Go module; subsystems live under `core/`, `net/`, `gateway/`, `anchor/`, and the binaries under `cmd/`.

---

## License

[Apache License 2.0](LICENSE)
