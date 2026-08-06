---
id: erasure-coding
title: Storage-Layer Reed-Solomon Erasure Coding (10+4)
sidebar_label: Reed-Solomon Erasure Coding
---

# Storage-Layer Reed-Solomon Erasure Coding (10+4)

Membuss integrates protocol-level **Reed-Solomon erasure coding** (`klauspost/reedsolomon`) directly into ingestion, block storage, peer placement, transparent retrieval, and background repair pipelines.

---

## 1. Adaptive Galois Field Matrix Sharding ($GF(2^8)$)

Membuss dynamically optimizes the data ($K$) and parity ($M$) shard configuration based on content size to maximize fault tolerance while minimizing storage overhead:

| Payload Size | Data Shards ($K$) | Parity Shards ($M$) | Overhead | Fault Tolerance |
|---|---|---|---|---|
| **$< 64 \text{ KB}$** | $2$ | $1$ | $+50\%$ | $1$ missing shard |
| **$< 1 \text{ MB}$** | $4$ | $2$ | $+50\%$ | $2$ missing shards |
| **$< 10 \text{ MB}$** | $8$ | $3$ | $+37.5\%$ | $3$ missing shards |
| **$\ge 10 \text{ MB}$** *(Default)* | $10$ | $4$ | $+40\%$ | **$4$ missing shards** |

```text
Original Block Payload (256 KiB)
  ├── Data Shards (D1 ... D10)  :  10 × 25.6 KiB
  └── Parity Shards (P1 ... P4) :   4 × 25.6 KiB
```

---

## 2. Ingestion & Manifest Persistence

During ingestion (`AddWithProgress` / `AddDirectory`):
1. **Shard Encoding**: Each leaf block is encoded using `erasure.NewEncoder(erasure.AdaptiveConfig(size))`.
2. **Manifest Creation**: An `ErasureManifest` protobuf structure is constructed linking `OriginalMid`, `DataShards`, `ParityShards`, and `ShardMids`.
3. **Storage & Announcement**: All 14 shard blocks are stored in the `Blockstore` under their respective `ShardMID` hashes and announced across the P2P DHT network.

---

## 3. Transparent Resilient Retrieval & Reconstruction

When a client requests a MID via `fetchingBlockstore`:
1. **Direct Fetch**: Attempts to retrieve the primary block directly.
2. **Erasure Fallback**: If the primary block is missing or storing peers go offline:
   - Reads the `ErasureManifest` from store metadata.
   - Fetches available shards from connected swarm peers.
   - As soon as at least $K = \text{DataShards}$ valid shards arrive, executes `encoder.Decode(shards, manifest)`.
   - Verifies reconstructed bytes match the expected BLAKE3 hash.
   - Restores the block locally and streams it seamlessly to the caller.

---

## 4. Background Shard Repair Worker

The background repair worker (`core/erasure/repair.go`) continuously audits sealed MIDs:
1. **Health Audit**: Checks presence of all 14 shards across the network.
2. **Degraded Detection**: Identifies MIDs with $K \le \text{present} < N$ shards available.
3. **Shard Reconstruction**: Reconstructs missing data/parity shards via matrix inversion.
4. **Re-distribution**: Writes missing shards back to disk/peers and re-announces them to restore full 10+4 redundancy.
