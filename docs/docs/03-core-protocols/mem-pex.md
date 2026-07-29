---
id: mem-pex
title: Mem-PEX Gossip Swarm Discovery Protocol
sidebar_label: Mem-PEX Swarm Discovery
---

# Mem-PEX Gossip Swarm Discovery Protocol

**Mem-PEX** (Peer Exchange, `core/pex`, PubSub Topic: `/membuss/pex/1.0.0`) allows active nodes to exchange peer multiaddresses in real-time over PubSub gossip without constant DHT lookups.

---

## 1. Message Payload Schema

```json
{
  "peer_id": "12D3KooW...",
  "addrs": [
    "/ip4/192.168.1.10/tcp/4001",
    "/ip4/192.168.1.10/udp/4001/quic-v1"
  ],
  "timestamp": 1753801200
}
```

---

## 2. Sybil Defense & Subnet Limits

PEX enforces IP bucket limits (max 3 peers per `/24` IPv4 subnet) to defend against Sybil routing table poisoning.
