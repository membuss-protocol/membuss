---
id: storage-engine
title: Pebble DB Storage Engine Architecture & Namespace Encoding
sidebar_label: Storage Engine Architecture
---

# Pebble DB Storage Engine Architecture

Membuss leverages **CockroachDB's Pebble DB** (`core/db/pebble_ds.go`, `core/store/pebble.go`) as its embedded Log-Structured Merge-tree (LSM) storage engine.

---

## 1. LSM Namespace Key Layout

Keys in Pebble DB are prefixed to partition namespaces cleanly:

| Prefix Variable | Raw Key Bytes | Target Content | Example Key String |
|---|---|---|---|
| `db.PrefixBlock` | `blk/` | Content block payload / header | `blk/<m.Bytes()>` |
| `db.PrefixDAG` | `dag/` | Serialized `DAGNode` protobuf | `dag/<m.Bytes()>` |
| `db.PrefixMeta` | `meta/` | System metadata & timestamps | `meta/ts/<m.String()>` |
| `db.PrefixSeal` | `seal/` | Root sealing records | `seal/<m.Bytes()>` |

---

## 2. Zero-Copy Slice Allocation

- **`m.Bytes()`**: Returns a direct sub-slice of the underlying multihash byte array without heap allocation.
- **`Block.RawData()`**: Provides zero-copy byte slice getters for internal block exchange pipelines.
