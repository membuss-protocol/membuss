<div align="center">

# Membuss

### The content network that survives partial failure.

Decentralized storage and delivery built on erasure coding, streaming block exchange, and automatic content persistence.

IPFS loses content when providers leave. BitTorrent needs seeders online. Membuss encodes redundancy into the protocol itself.

[![Go 1.22+](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/nnlgsakib/membuss)](https://github.com/nnlgsakib/membuss/releases)

</div>

---

## Why Membuss?

Content-addressed networks have a fundamental problem: **availability depends on humans remembering to pin things.** IPFS content disappears when providers shut down. BitTorrent torrents die when seeders leave. Both protocols push persistence responsibility to the user.

Membuss solves this differently. Every block is erasure-coded at the storage layer. Dedicated Anchor Nodes automatically mirror announced content. Content survives not because someone pinned it, but because the protocol makes disappearance structurally unlikely.

### Membuss vs IPFS vs BitTorrent

| | Membuss | IPFS | BitTorrent |
|---|---|---|---|
| **Erasure coding** | Built-in Reed-Solomon `10+4` on every block | None — availability depends entirely on providers | Optional parity files, not protocol-level |
| **Content persistence** | Anchor Nodes auto-mirror all announced content | Manual pinning required | Seeders must stay online |
| **Block streaming** | `FetchStream` returns `io.Reader` immediately — first bytes arrive as soon as first block resolves | Waits for entire DAG before delivering any data | Piece-level streaming only |
| **Block verification** | SHA-256 verified before every block hits disk | Trusts the blockstore, no per-block verify | Piece hashes only |
| **Congestion control** | AIMD sliding window per peer (8–128 blocks) | None | uTP at transport level only |
| **Provider selection** | Ranked by latency + bandwidth + freshness | Unranked list | Unranked list |
| **Reprovide efficiency** | 6 incremental groups — 83% less DHT load per cycle | Full re-announce every cycle | N/A (tracker-based) |
| **Mutable naming** | MemNS with PubSub real-time updates + weighted routes | IPNS (single target, polling) | N/A |
| **Gateway** | Range requests, ETag caching, SPA fallback, custom domains | Basic HTTP gateway | N/A |
| **Peer exchange** | Signed records + reachability filtering | Unsigned, no filtering | N/A |
| **Bloom filter gossip** | Pre-filter providers before asking — skip peers that don't have the block | None | N/A |

---

## How It Works

```
File → Chunk → Hash → Merkle DAG → Erasure Code (10+4) → Distribute → Announce
  │                                              │              │           │
  │                                              ▼              ▼           ▼
  │                                         BadgerDB      Consistent    Kademlia
  │                                         Blockstore     Hashing      DHT
  │
  └── Reassemble ← Fetch via Memex ← Provider Discovery ← DHT Query
```

1. **Chunk** — Rabin fingerprinting splits files into content-defined blocks
2. **Hash** — Each block gets a SHA-256 multihash, wrapped with `mem` codec prefix → **MID**
3. **DAG** — Blocks structured into a Merkle DAG (fanout 174, deduplication automatic)
4. **Erase** — Reed-Solomon `10+4` encodes every leaf — lose any 4 of 14 shards, still recover
5. **Store** — Blocks saved to BadgerDB with bloom filter acceleration (10M capacity, 0.1% FPR)
6. **Exchange** — Memex v2: bidirectional block exchange with streaming assembly, AIMD congestion, parallel fetching
7. **Announce** — Provider records published to Kademlia DHT, gossiped via PEX
8. **Reprovide** — Mem-Herald re-announces in incremental groups (6 groups = 83% less DHT traffic)
9. **Mirror** — Anchor Nodes optionally sync all announced content for durability

---

## Features

### Erasure Coding (Built-in)

Every block is Reed-Solomon encoded into `10 data + 4 parity` shards before it leaves a node. 40% storage overhead. Content survives the loss of any 4 shards. No pinning service needed — redundancy is structural, not administrative.

### Anchor Nodes

Optional full-mirror nodes that automatically discover announced content via DHT, pull it via Memex, seal it locally, and re-announce it. Content persists even when original providers go offline. No manual pinning. No third-party service.

### Memex v2 — Block Exchange

Custom bidirectional protocol over libp2p streams:

- **Streaming assembly** — `FetchStream()` returns an `io.Reader` pipe immediately while blocks resolve in background goroutines. First bytes arrive as soon as first block arrives.
- **Persistent stream pool** — reuse connections across sessions instead of opening new streams per request.
- **AIMD congestion control** — adaptive sliding window (8–128 blocks) per peer, halves on write errors.
- **Cryptographic verification** — `runtime.NumCPU()` worker pool verifies SHA-256 hashes off the hot path.
- **Batch DB writer** — buffered channel with 128-block flush batches, 5ms tick flush.
- **Bloom filter gossip** — nodes broadcast sealed-MID bloom filters every 5 minutes. Peers skip providers that don't have the wanted block.

### Mem-DHT — Enhanced Kademlia

Provider discovery ranked by composite score:

- **Latency** — `1000/(ms+1)` score, faster peers ranked higher
- **Bandwidth** — 1 point per KB/s rate + 1 per MB total transferred
- **Freshness** — `500/(hours+1)`, recently-seen peers preferred
- **10,000-entry CID cache** — eliminates repeated allocation on hot paths
- **Custom validators** — `membuss` and `memns` namespaces with strict validation

### Mem-PEX — Peer Exchange

Gossip protocol at `/membuss/pex/1.0.0`:

- Every 30 seconds, picks 5 random connected peers, exchanges peer tables
- **Signed records** — every `PeerInfo` is cryptographically signed; unsigned records rejected
- **Reachability filtering** — PUBLIC peers shared with full addrs, RELAY_ONLY with relay addrs only
- **2-hour freshness window** — stale peers evicted automatically
- **Persistent table** — saved to disk every 5 minutes, restored on restart

### Mem-Gate — HTTP Gateway + CDN

Full HTTP gateway at `/mem/{MID}`:

- **Range requests** — `Range: bytes=N-M` returns `206 Partial Content` for audio/video streaming
- **ETag + immutable caching** — `Cache-Control: max-age=31536000` with ETag matching
- **SPA fallback** — auto-serves `index.html` for HTML navigation requests
- **Subdomain routing** — serve content via `mid.example.com` or path-based `/mem/{MID}`
- **MemNS custom domains** — resolve `example.com` → MID via DNS TXT `_memlink` records
- **Directory listings** — HTML or JSON with `?format=json`, auto-detects `index.html`

### MemNS — Mutable Naming

Decentralized naming system with:

- DHT-stored records with PubSub real-time updates (no polling)
- Weighted routes for traffic splitting (geo-based, load-based)
- DNS TXT `_dnslink` fallback for IPFS migration
- Max resolution depth of 10 prevents infinite chains

### Mem-Herald — Reprovider

Background reprovider with three strategies:

- `roots` — sealed root MIDs only (cheap default)
- `all` — every block (used by anchor nodes)
- `shards` — erasure shards via consistent hashing ring

**Incremental groups** split reprovide into 6 cycles. Each cycle announces 1/6 of keys — 83% less DHT load than IPFS's full re-announce.

### Desktop App

Wails-based GUI:

- Start/stop daemon from dashboard
- Embedded explorer with SvelteKit UI, Three.js globe, Leaflet maps
- Auto-update via GitHub releases
- Keep-alive mode (daemon survives GUI close)

### BadgerDB Storage

- **Bloom filter** — `Has()` checks bloom first; "definitely absent" skips disk entirely
- **Content verification** — `Put()` verifies SHA-256 matches MID before writing
- **Namespace separation** — blocks (`/b/`), DAGs (`/d/`), seals (`/s/`), metadata (`/m/`)
- **Value log GC** — periodic background garbage collection recovers disk space

---

## Quick Start

### Build

```bash
git clone https://github.com/nnlgsakib/membuss
cd membuss
make build
# -> bin/membuss (daemon) and bin/membuss-cli (CLI)
```

### Run

```bash
./bin/membuss -config membuss.yaml
# Gateway:  http://127.0.0.1:8080
# Node API: http://127.0.0.1:5001
# gRPC:     127.0.0.1:50051
```

### Use

```bash
# Upload
./bin/membuss-cli add README.md
# -> mem1z4a2bd9fzsg7n6n8y9z4m9z9y8z7x6w5v4u3t2s1r0q9p8o7n6m5

# Fetch
./bin/membuss-cli get mem1z4a2bd9f... -o copy.md

# Browse
open http://127.0.0.1:8080/explorer/
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

**Image**: distroless `nonroot`, ~25 MB. Ports: `4001` (libp2p), `5001` (Node API), `8080` (Mem-Gate), `50051` (gRPC).

---

## Configuration

Single YAML file. Key fields:

```yaml
listen_addrs:
  - /ip4/0.0.0.0/tcp/4001
  - /ip4/0.0.0.0/udp/4001/quic-v1

data_dir: ./data
gateway_addr: 127.0.0.1:8080
api_addr: 127.0.0.1:5001
grpc_addr: 127.0.0.1:50051

anchor_mode: false
reprovide_interval: 10m
reprovide_groups: 6
```

Full schema: [`config/config.go`](config/config.go). All fields overridable via environment variables in Docker.

---

## API

### Mem-Gate — Public Gateway

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/mem/{mid}` | Fetch resolved content |
| `HEAD` | `/mem/{mid}` | Existence + Content-Length |
| `GET` | `/mem/{mid}?format=dag-json` | DAG node as JSON |
| `GET` | `/mem/{mid}/{path}` | DAG path traversal |
| `GET` | `/explorer/` | Web explorer |
| `GET` | `/healthz` | Liveness probe |

### Node API — Local Control

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/add` | Upload (raw or multipart) |
| `GET` | `/api/v1/get/{mid}` | Fetch content |
| `POST` | `/api/v1/seal/{mid}` | Pin recursively |
| `DELETE` | `/api/v1/seal/{mid}` | Unpin |
| `GET` | `/api/v1/stat/{mid}` | Size, blocks, seal status |
| `GET` | `/api/v1/peers` | Connected peers |
| `GET` | `/api/v1/node/info` | Peer ID, addrs, version |
| `POST` | `/api/v1/gc` | Garbage collection |
| `GET` | `/metrics` | Prometheus metrics |

Auth: `X-Membuss-Key: <api_key>` when `api_key` is set.

---

## CLI

| Command | Description |
|---------|-------------|
| `membuss-cli add <file>` | Upload file, return MID |
| `membuss-cli add-dir <dir>` | Upload directory as MemFS |
| `membuss-cli get <MID> [-o file]` | Fetch content |
| `membuss-cli seal <MID>` | Pin (prevent GC) |
| `membuss-cli unseal <MID>` | Unpin |
| `membuss-cli stat <MID>` | Size, blocks, seal status |
| `membuss-cli peers` | PEX peer table |
| `membuss-cli dht peek <MID>` | Query DHT for providers |
| `membuss-cli gc` | Garbage collection |
| `membuss-cli anchor status` | Anchor engine stats |
| `membuss-cli ping` | Connectivity probe |

All commands support `--json`.

---

## Development

```bash
make build          # Daemon + CLI
make test           # go test ./... -race -count=1
make lint           # golangci-lint
make proto          # Regenerate protobuf bindings
make run-daemon     # Run with ./membuss.yaml
make frontend-dev   # Vite dev server for explorer
```

---

## License

[Apache License 2.0](LICENSE)
