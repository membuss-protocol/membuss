---
id: memns
title: MemNS Mutable Naming Protocol
sidebar_label: MemNS Mutable Naming
---

# MemNS Mutable Naming Protocol

**MemNS** (`core/memns`, PubSub Topic: `/membuss/memns/v1`) implements mutable cryptographic pointers that map public keys or static names to dynamically updating MIDs.

---

## 1. Cryptographic Record Structure

Each MemNS record contains:
- Target MID (e.g. `membafzbeidr...`)
- Monotonic Sequence Number (`uint64`)
- Expiration Unix Timestamp (`int64`)
- Ed25519 Signature over `(TargetMID + Sequence + Expiry)`

---

## 2. Real-Time PubSub Propagation

When a node publishes a MemNS update, it is broadcast over libp2p PubSub `/membuss/memns/v1`. Subscribed gateways update pointer mappings in **sub-second real-time**.
