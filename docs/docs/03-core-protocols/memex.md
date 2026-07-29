---
id: memex
title: Memex v2 Protocol Specification
sidebar_label: Memex Block Exchange
---

# Memex v2 Block Exchange Protocol

**Memex** is Membuss's custom block transfer protocol operating over libp2p stream multiplexing (Protocol ID: `/membuss/memex/2.0.0`).

---

## 1. Multiplexed Session Flow Diagram

```mermaid
sequenceDiagram
    autonumber
    participant PeerA as Peer A (Requester)
    participant PeerB as Peer B (Provider)

    PeerA->>PeerB: Dial Stream (/membuss/memex/2.0.0)
    PeerB-->>PeerA: Session Established
    PeerA->>PeerB: Send WantList Batch [MID1, MID2, MID3]
    
    par Stream Block 1
        PeerB-->>PeerA: Deliver Block Payload (MID1)
    and Stream Block 2
        PeerB-->>PeerA: Deliver Block Payload (MID2)
    and Stream Block 3
        PeerB-->>PeerA: Deliver Block Payload (MID3)
    end

    Note over PeerA,PeerB: Window size adjusts via AIMD Congestion Control
```

---

## 2. AIMD Congestion Control & Peer Ranking

- **AIMD Flow Control**: Uses Additive Increase / Multiplicative Decrease window scaling to dynamically adjust block request batch sizes based on measured round-trip time (RTT).
- **Peer Ranking**: Peers are continuously ranked using an Exponential Moving Average (EMA) of latency and throughput ($B/s$).
