---
id: chunking-and-hashing
title: Chunking Algorithms & Parallel Hashing Pipeline
sidebar_label: Chunking & Parallel Hashing
---

# Chunking Algorithms & Parallel Hashing Pipeline

Membuss provides three chunking algorithms (`core/chunk`) and a parallel worker goroutine pool (`core/dag/parallel.go`).

---

## 1. Chunking Algorithms

- **Fixed Chunker (`fixedChunker`)**: 256 KiB fixed block size.
- **Rabin Chunker (`rabinChunker`)**: Content-defined chunking using Rabin fingerprints.
- **FastCDC Chunker (`fastCDCChunker`)**: Gear-based fast content-defined chunking.

---

## 2. Parallel Merkle Tree Engine (`BuildParallel`)

For large file ingestion ($> 1$ GB), `BuildParallel` distributes work across goroutines:

```
Stream Reader ──► Producer ──► Worker Channels (BLAKE3 Hashing) ──► Re-Ordering Queue ──► Root Merkle DAG
```

- **Producer**: Reads bytes from chunker stream.
- **Worker Channels**: $N$ concurrent workers compute BLAKE3 multihashes and generate leaf `DAGNode` protobufs.
- **Re-Ordering Queue**: Re-orders leaf MIDs sequentially by chunk index before assembling the root Merkle tree.
