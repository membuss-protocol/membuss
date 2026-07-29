---
id: configuration
title: Configuration Schema (membuss.yaml)
sidebar_label: Configuration Schema
---

# Complete `membuss.yaml` Configuration Schema

Membuss node configuration is managed via a single YAML manifest (`membuss.yaml`). Every setting has an explicit fallback default inside `core/config/config.go`.

---

## Complete `membuss.yaml` Manifest

```yaml
# libp2p Transport Listen Addresses
listen_addrs:
  - /ip4/0.0.0.0/tcp/4001
  - /ip4/0.0.0.0/udp/4001/quic-v1
  - /ip4/0.0.0.0/tcp/4002/ws

# Directory Paths
data_dir: ./data
blocks_path: ./data/blocks   # Optional flat file blob storage path for large objects (>= 1MB)

# Service Bindings
gateway_addr: 127.0.0.1:8080
api_addr: 127.0.0.1:5001
grpc_addr: 127.0.0.1:50051

# Subsystem Enable Toggles
servers:
  gateway:
    enabled: true              # Mem-Gate HTTP gateway & Web Explorer UI
  node_api:
    enabled: true              # Local REST Control API (/api/v1)
  grpc:
    enabled: true              # Daemon gRPC service

# Multihash & Storage Engine Settings
storage:
  default_hash: "blake3"       # Multihash codec: "blake3" (0x1e), "sha256" (0x12), "sha512" (0x13)
  bloom_capacity: 10000000     # Counting Bloom Filter expected items
  bloom_fp_rate: 0.001        # Target false positive rate (0.1%)

# Persistence & Anchor Engine
anchor_mode: false            # true = automatically discover and mirror announced network content
anchor:
  max_storage: 100GB           # Bounded storage quota (e.g. 500MB, 100GB, 1TB, 0 = unlimited)
  fetch_concurrency: 8         # Parallel worker pool size for backlog mirror fetching
  reacquire_batch_size: 32     # Sampling batch size per discovery round

# Modular Plugin Extensions
plugins:
  enabled: true
  active:
    - echo-inspector
  config:
    echo-inspector:
      log_level: info

# Observability
metrics_enabled: true         # Exposes Prometheus metrics at /metrics
log_level: info
```

---

## Detailed Parameter Reference

### `storage.default_hash`
- **Type**: `string`
- **Default**: `"blake3"`
- **Options**: `"blake3"`, `"sha256"`, `"sha512"`
- **Description**: Sets the default multihash algorithm used by `BuildParallel` during chunk ingestion.

### `anchor_mode` & `anchor.max_storage`
- **Type**: `boolean` & `string`
- **Default**: `false` & `"100GB"`
- **Description**: Configures whether the node operates as an **Anchor Node**, listening to network announcements and maintaining bounded storage with automated LRU eviction for auto-mirrored content.
