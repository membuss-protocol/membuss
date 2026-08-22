# PEX — peer exchange protocol

Protocol ID: `/membuss/pex/1.0.0` (`net/pex/pex.go:ProtocolID`).

## Framing

```
+--------------+----------------------+
| len: 4 bytes | protobuf payload     |
| big-endian   | (len bytes)          |
+--------------+----------------------+
```

- Cap: 1 MiB (`readMsg`, pex.go).
- **Legacy fallback**: if the first read yields fewer than 4 bytes the
  receiver abandons framing and buffers until stream EOF or 1 MiB.
  Known-broken on short reads (finding.txt NETPKG-011); treat as
  deprecated and do not rely on it for new clients.

## Messages

Payload is `membuss.v1.PeerInfo`, optionally wrapped in a signed
gossip record:

| Field | Meaning |
|-------|---------|
| `peer_id` | libp2p peer ID string |
| `addrs` / `relay_addrs` | direct + relay multiaddrs (Phase 12) |
| `last_seen` | unix seconds |
| `reachability` | enum, sender's observed NAT posture |
| `last_dial_success` | bool |
| `signature` / `pub_key` / `seq` | Ed25519-signed record; monotonic `seq` gates replays (Phase 20) |

Anti-entropy: peers exchange signed records both directions per gossip
round; `(seq, lastSeen)` ordering decides winners.
