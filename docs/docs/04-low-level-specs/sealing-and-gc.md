---
id: sealing-and-gc
title: Content Sealing, DAG Reachability Walk & GC
sidebar_label: Content Sealing & GC
---

# Content Sealing & Garbage Collection Architecture

Membuss implements explicit content sealing and two-phase reachability garbage collection (`core/store/seal.go`, `core/store/gc_tracker.go`).

---

## 1. Content Sealing

- **Unsealed Content**: Ingested blocks that have not been explicitly pinned.
- **Sealed Content**: Content marked for permanent retention. `Seal(root)` recursively walks the Merkle DAG from `root` and writes seal records under `seal/` prefix in Pebble DB.

---

## 2. Two-Phase Garbage Collection (GC)

1. **Phase 1 (Reachability Walk)**: Traverses all sealed roots (`AllSealed()`) and populates an in-memory reachability set.
2. **Phase 2 (Orphan Sweeping)**: Iterates over `blk/` and `dag/` key namespaces. Any key NOT in the reachability set AND older than `minAge` is deleted.
3. **Ingestion Grace Tracker (`gcTracker`)**: Protects recently uploaded blocks from concurrent GC sweeps using a configurable grace period window.
