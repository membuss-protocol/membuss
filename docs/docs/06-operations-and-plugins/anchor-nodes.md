---
id: anchor-nodes
title: Operating an Anchor Node & Auto-Mirroring
sidebar_label: Anchor Nodes
---

# Operating an Anchor Node & Auto-Mirroring

**Anchor Nodes** provide guaranteed content durability across Membuss networks.

---

## 1. Automated Mirroring Protocol

When `anchor_mode: true` is configured:
1. The node monitors announced entry-node MIDs on Mem-DHT and PEX.
2. It fetches and mirrors announced content trees using worker concurrency pools.
3. Content remains available network-wide even if original uploader nodes disconnect.

---

## 2. Bloom Filter Delta Sync & Bounded Storage

- **Bloom Delta Sync**: Exchanges inventory Bloom filter snapshots to prevent redundant data fetches (cutting sync traffic by $> 95\%$).
- **Bounded Storage & LRU Eviction**: Maintains a `max_storage` quota (e.g. 100GB). Unpinned auto-mirrored content is evicted using LRU ordering when storage limits are reached, while operator-sealed content remains immune from eviction.
