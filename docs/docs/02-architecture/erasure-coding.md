---
id: erasure-coding
title: Storage-Layer Reed-Solomon Erasure Coding (10+4)
sidebar_label: Reed-Solomon Erasure Coding
---

# Storage-Layer Reed-Solomon Erasure Coding (10+4)

Membuss integrates protocol-level **Reed-Solomon erasure coding** (`klauspost/reedsolomon`) directly into block storage and peer distribution pipelines.

---

## 1. Galois Field Matrix Sharding (GF(2^8))

When a payload is committed:
- **Data Shards (`K = 10`)**: The payload is partitioned into 10 equal-sized data shards.
- **Parity Shards (`M = 4`)**: 4 parity shards are generated using Galois Field matrix multiplication:
  `P = G * D`
  where `G` is a Vandermonde or Cauchy distribution matrix over `GF(2^8)`.

```
Data Payload (256 KiB)
  ├── Data Shards (D1 ... D10)  :  10 × 25.6 KiB
  └── Parity Shards (P1 ... P4) :   4 × 25.6 KiB
```

---

## 2. Reconstruction Algorithm & Integrity Verification

- **Fault Tolerance Limit**: Any **4 out of 14 shards** can be missing or corrupted without data loss.
- **Reconstruction Process**:
  1. `rs.Reconstruct()` checks shard availability.
  2. If >= 10 shards are present, matrix inverse computation reconstructs missing shards.
  3. The reconstructed data is verified against the expected BLAKE3 multihash digest before being returned.
