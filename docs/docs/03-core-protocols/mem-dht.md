---
id: mem-dht
title: Mem-DHT Kademlia Provider Routing Protocol
sidebar_label: Mem-DHT Routing
---

# Mem-DHT Kademlia Provider Routing Protocol

**Mem-DHT** (`core/dht`, Protocol ID: `/membuss/kad/1.0.0`) provides distributed provider routing across network nodes using Kademlia distance metrics.

---

## 1. Provider Record Routing

To keep DHT traffic minimal, Membuss nodes publish provider records **only for root entry-node MIDs** rather than announcing every chunk block.

```text
Record Key   : /membuss/provider/<RootMID>
Record Value : PeerID + Multiaddresses + TTL (24 Hours)
```

---

## 2. Kademlia Distance Metric

Uses XOR distance $d(x, y) = x \oplus y$ to query the $K=20$ closest peers for any key lookup.
