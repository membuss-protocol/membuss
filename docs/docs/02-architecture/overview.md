---
id: overview
title: System Architecture & Data Flow Blueprint
sidebar_label: System Architecture Blueprint
---

# System Architecture & Data Flow Blueprint

The **Membuss** architecture is structured into modular, decoupled layers for networking, storage, content processing, and API interfaces.

---

## 1. System Architecture Diagram

```mermaid
graph TB
    subgraph Client_Layer ["Client & Interface Layer"]
        GUI[Desktop GUI - Wails]
        CLI[CLI Binary]
        GRPC[gRPC API - :50051]
        REST[Node API - :5001]
        Gate[Mem-Gate HTTP CDN - :8080]
    end

    subgraph Core_Pipeline ["Core Processing Pipeline"]
        MemFS[MemFS Engine - File/Dir/Symlink]
        DAG[Parallel DAG Builder - BLAKE3]
        RS[Reed-Solomon 10+4 Erasure Encoder]
    end

    subgraph Storage_Engine ["Storage & Indexing Engine"]
        Pebble[Pebble DB LSM Storage]
        CBF[O/1 Counting Bloom Filter]
        Blob[Flat File Blob Offloader]
    end

    subgraph Swarm_Layer ["P2P Networking Swarm"]
        Memex[Memex v2 Block Exchange]
        DHT[Mem-DHT Kademlia Routing]
        PEX[Mem-PEX Gossip Swarm]
        Herald[Mem-Herald Reprovider]
    end

    GUI --> GRPC
    CLI --> GRPC
    REST --> MemFS
    Gate --> MemFS

    MemFS --> DAG
    DAG --> RS
    RS --> Pebble
    Pebble <--> CBF
    Pebble <--> Blob

    Pebble <--> Memex
    Memex <--> DHT
    Memex <--> PEX
    Herald --> DHT
```

---

## 2. Ingestion & Storage Sequence

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant CLI as CLI / Daemon
    participant Chunker as Chunker (256KB)
    participant Pool as BLAKE3 Worker Pool
    participant RS as RS 10+4 Encoder
    participant Store as Pebble Store / Blob
    participant CBF as Counting Bloom Filter
    participant DHT as Mem-DHT / PEX

    User->>CLI: membuss add file.mp4
    CLI->>Chunker: Slices stream into 256KB chunks
    Chunker->>Pool: Spawns concurrent hashing channels
    Pool-->>CLI: Computes BLAKE3 MIDs & Protobuf DAG
    CLI->>RS: Encodes raw leaves into 10+4 shards
    RS->>Store: Writes <1MB to Pebble SSTable / >=1MB to Disk
    Store->>CBF: Increments bucket counters in O(1) time
    CLI->>DHT: Announces root entry MID via Mem-Herald
    CLI-->>User: Returns root MID (membafzbe...)
```

---

## 3. Retrieval & Remote Fetch Flow

```mermaid
sequenceDiagram
    autonumber
    actor Client
    participant Gate as Mem-Gate Gateway
    participant CBF as Counting Bloom Filter
    participant Store as Pebble Store
    participant Memex as Memex v2 Stream
    participant Network as libp2p Swarm

    Client->>Gate: GET /mem/membafzbe...
    Gate->>CBF: Test(m) in O(1) RAM time
    alt Block Exists Locally
        CBF-->>Gate: Found (true)
        Gate->>Store: Read SSTable or Flat Blob
        Store-->>Client: Returns streaming bytes
    else Block Missing Locally
        CBF-->>Gate: Missing (false)
        Gate->>Network: DHT Provider Query for MID
        Network-->>Memex: Returns Provider Multiaddrs
        Gate->>Memex: Open Memex Stream Session
        Memex-->>Gate: Streams raw blocks over multiplexed channel
        Gate->>Store: Caches fetched blocks locally
        Gate-->>Client: Returns streaming bytes
    end
```
