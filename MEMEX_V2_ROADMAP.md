# Memex v2 Implementation Roadmap

This document serves as the official implementation roadmap for **Memex v2**. The core goal of v2 is to achieve BitTorrent and IPFS-style reliability, high throughput, and fault tolerance during block exchange.

Follow the checklist below to implement and verify the new protocol.

---

## Phase 1: Protocol Schema & Code Generation
Update the protobuf schemas and compile them to support bidirectional, sequence-tracked block exchanges and negative acknowledgments.

- [ ] **Define Protocol Schema**
  * Update [rpc/proto/membuss.proto](file:///D:/projects/go/rsrc/membuss/rpc/proto/membuss.proto) to include sequence numbers, batch metadata, and explicit fields for Bloom filter exchanges on stream init.
  * *Draft specification:*
    ```proto
    message MemexMessageV2 {
      uint64 sequence_number = 1;
      repeated WantEntry wants = 2;
      repeated string cancels = 3;
      repeated Block blocks = 4;
      repeated string dont_haves = 5;
      bytes bloom_filter = 6;
    }
    ```
- [ ] **Generate Protobuf Code**
  * Run the build script/command to re-generate the Go protobuf models under [proto/membuss.pb.go](file:///D:/projects/go/rsrc/membuss/proto/membuss.pb.go).
- [ ] **Protocol Suffix Configuration**
  * Add `ProtocolIDV2 = protocol.ID("/membuss/memex/2.0.0")` in [net/memex/memex.go](file:///D:/projects/go/rsrc/membuss/net/memex/memex.go).

---

## Phase 2: Persistent Stream Pool & Connection Reuse
Transition away from dynamic, session-scoped streams to pooled, persistent streams per peer.

- [ ] **Implement Stream Pool Manager**
  * Define a thread-safe connection/stream pool (`PeerStreamPool`) in [net/memex/memex.go](file:///D:/projects/go/rsrc/membuss/net/memex/memex.go).
  * Hold persistent bidirectional stream references mapped by `peer.ID`.
- [ ] **Multiplex Wants across Sessions**
  * Modify `writeLoop` in [net/memex/session_io.go](file:///D:/projects/go/rsrc/membuss/net/memex/session_io.go#L110) to accept a session ID or global identifier, allowing multiple concurrent session retrieves to queue wants on the same stream safely.
- [ ] **Stream Lifecycle & Reconnection Hook**
  * Add automatic connection monitoring. If a stream is dropped or encounters a network error, mark the peer as dead, cancel in-flight wants on that peer, and schedule them on other active peers.

---

## Phase 3: Decoupled Verification & Batch DB Writes
Separate CPU-intensive cryptographic hashing and slow disk writes from the network IO read loops.

- [ ] **Implement Cryptographic Verifier Worker Pool**
  * Build a worker pool (`VerifierPool`) with a fixed configuration (e.g., `runtime.NumCPU()`) to parse block payloads and compute SHA-256 hashes asynchronously.
- [ ] **Integrate Async Database Batcher**
  * Create a buffered channel for validated blocks.
  * Run a background thread that flushes blocks to the BadgerDB store [core/store/badger.go](file:///D:/projects/go/rsrc/membuss/core/store/badger.go) using Badger's write batches (`WriteBatch`).

---

## Phase 4: Dynamic Scheduler & Endgame Mode
Build the smart request router to handle high latency, slow peers, and network dropouts.

- [ ] **Active Latency Scoreboard**
  * Add real-time RTT tracking to [net/memex/memex.go](file:///D:/projects/go/rsrc/membuss/net/memex/memex.go#L187-L197). Update peer performance stats on every frame response.
- [ ] **Sliding Window Congestion Control**
  * Implement an adaptive pipeline depth ($W$) per peer.
  * Adjust window size dynamically based on response speed and time-outs (AIMD congestion control).
- [ ] **Unblock `FetchStream`**
  * Fix the structural bug in `FetchStream` in [net/memex/session.go](file:///D:/projects/go/rsrc/membuss/net/memex/session.go#L322-L538) where it blocks the caller synchronously on `<-s.walkerDone`. Make it return the `pipeReader` immediately while the downloader resolved/fetched blocks run concurrently in the background.
- [ ] **Endgame Mode Implementation**
  * Implement "Endgame Mode" in [net/memex/session.go](file:///D:/projects/go/rsrc/membuss/net/memex/session.go#L226). When remaining blocks drop below a threshold (e.g. last 5% of blocks), broadcast them to all available providers. On first success, broadcast `Cancel` frames to the other peers.

---

## Phase 5: Verification & Testing
Ensure the new v2 protocol is bulletproof and backward compatible.

- [ ] **Unit Tests for Stream Reuse**
  * Write tests in [net/memex/memex_test.go](file:///D:/projects/go/rsrc/membuss/net/memex/memex_test.go) verifying that multiple file requests to the same peer do not open duplicate libp2p stream connections.
- [ ] **Verifier Latency Tests**
  * Verify that slow disk writes do not degrade network throughput during high-bandwidth block transfers.
- [ ] **Backward Compatibility Fallback**
  * Test that nodes running Memex v2 successfully negotiate and fall back to `/membuss/memex/1.0.0` when communicating with v1 nodes.
