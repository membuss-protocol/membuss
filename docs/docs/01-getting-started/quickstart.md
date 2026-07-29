---
id: quickstart
title: Developer & Node Quick Start Guide
sidebar_label: Quick Start Guide
---

# Developer & Node Quick Start Guide

This guide walks through compiling the `membuss` single-binary executable, initializing the daemon, storing content via BLAKE3 parallel chunking, and resolving objects via CLI, gRPC, and Mem-Gate HTTP gateway.

---

## 1. Environment Requirements

- **Go**: 1.25+ (`go version`)
- **CGO**: Not required (`CGO_ENABLED=0`)
- **OS**: Linux, macOS, Windows

---

## 2. Compilation from Source

Clone the repository and compile the unified `membuss` executable:

```bash
git clone https://github.com/nnlgsakib/membuss.git
cd membuss
make build
# Produces output executable ./bin/membuss
```

Verify binary build:

```bash
./bin/membuss version
```

---

## 3. Initializing & Starting the Node Daemon

Start the local Membuss daemon using the default configuration file (`membuss.yaml`):

```bash
./bin/membuss daemon start --config ./membuss.yaml
```

### Active Daemon Services:
- **Pebble LSM Blockstore**: Initialized at `./data/pebble`
- **libp2p Network Host**: Swarm listening on `/ip4/0.0.0.0/tcp/4001` and `/ip4/0.0.0.0/udp/4001/quic-v1`
- **Daemon gRPC Control API**: Listening on `127.0.0.1:50051`
- **Node REST Control API**: Listening on `http://127.0.0.1:5001/api/v1`
- **Mem-Gate HTTP CDN & Web Explorer**: Listening on `http://127.0.0.1:8080`

---

## 4. Ingesting & Retrieving Data via CLI

### Ingesting Content (BLAKE3 Multihash)

Add a file or directory tree to local storage:

```bash
./bin/membuss add ./dataset.tar.gz
```

**Output**:
```text
Added ./dataset.tar.gz
MID:  membafzbeidr5pk22uidyjnsay6lgrlkcdx7dcrvuimfnl4t5v4otdmbyfiugm
Size: 104857600 bytes (100.00 MB)
Chunks: 400 (Fixed 256 KiB)
Hash Algorithm: BLAKE3 (0x1e)
```

### Inspecting the Merkle DAG Descriptor

Inspect the block layout and child links of an MID:

```bash
./bin/membuss dag membafzbeidr5pk22uidyjnsay6lgrlkcdx7dcrvuimfnl4t5v4otdmbyfiugm
```

### Retrieving Content

Re-assemble and write content back to disk:

```bash
./bin/membuss get membafzbeidr5pk22uidyjnsay6lgrlkcdx7dcrvuimfnl4t5v4otdmbyfiugm -o ./dataset-restored.tar.gz
```

---

## 5. Public Gateway & Web Explorer

- **Public HTTP Gateway**: `http://127.0.0.1:8080/mem/membafzbeidr5pk22uidyjnsay6lgrlkcdx7dcrvuimfnl4t5v4otdmbyfiugm`
- **Embedded Web Explorer Dashboard**: `http://127.0.0.1:8080/explorer/`
