---
id: relay-nodes
title: Operating Relay Nodes & NAT Traversal
sidebar_label: Relay Nodes & NAT
---

# Operating Relay Nodes & NAT Traversal

Membuss utilizes **libp2p Circuit Relay v2** and **DCUtR (Direct Connection Upgrade through Relay)** to ensure seamless connectivity between nodes behind NATs, firewalls, and carrier-grade NATs (CGNAT).

---

## 1. Overview

- **AutoNAT v2**: Automatically probes public vs. private reachability.
- **Circuit Relay v2**: Publicly accessible nodes act as relay hops for NATed peers.
- **DCUtR Hole Punching**: Automatically upgrades relayed connections to direct peer-to-peer streams.
- **Mem-Herald Relay Discovery**: Automatically advertises active relay providers on the DHT namespace `/membuss/relays/v1`.

---

## 2. Configuration for Public Bootnodes & Relay Servers

Public nodes (VPS, dedicated servers, Anchor nodes) should enable `relay_service` and `force_public` to guarantee 24/7 uninterrupted relay availability:

```yaml
# membuss.yaml

# Enable Circuit Relay v2 hop service
relay_service: true
relay_max_conns: 256
relay_max_reservations: 256
relay_bandwidth_mb: 32

# Lock host reachability to Public (prevents AutoNAT probe drops from stopping the relay)
force_public: true
```

### CLI / Environment Overrides

You can also pass these settings via CLI flags or environment variables:

```bash
# Via CLI flag
membuss daemon start --force-public

# Via Environment Variable
export MEMBUSS_FORCE_PUBLIC=true
membuss daemon start
```

---

## 3. Configuration for Private / NATed Nodes

Nodes running on home networks, mobile devices, or behind strict firewalls can configure static relay nodes or discover them dynamically via the DHT:

```yaml
# membuss.yaml

# Static relay bootstrap nodes
relay_peers:
  - /ip4/45.10.162.79/udp/4001/quic-v1/p2p/12D3KooWMNbuDSWaMw7evxzsp9CtaphofzxcEbHisWQQUmg7zfUx

# (Optional) Force private mode to immediately acquire relay reservations without waiting for AutoNAT
force_relay: false
```
