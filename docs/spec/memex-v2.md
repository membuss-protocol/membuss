# memex v2 — block exchange protocol

Protocol ID: `/membuss/memex/2.0.0` (`net/memex_v2/memex.go:ProtocolID`).
Sidecar: `/membuss/memex-bloom/2.0.0` (`bloom.go:BloomProtocolID`).

## Framing

Every frame on a memex stream:

```
+--------------+----------------------+
| len: 4 bytes | protobuf payload     |
| big-endian   | (len bytes)          |
+--------------+----------------------+
```

- Length prefix: `uint32` big-endian (`readFrame`, memex.go).
- `len == 0` or `len > 16 MiB` (`maxFrameSize = 16 << 20`) → frame invalid,
  sender must reset the stream. Receiver returns `nil` and tears down.

## Messages

All payloads are `membuss.v1.MemexMessage` (`proto/membuss.pb.go`):

| Field | Type | Meaning |
|-------|------|---------|
| `wants` | `repeated WantEntry` | blocks the sender requests |
| `blocks` | `repeated Block` | payload blocks answering wants |
| `object_infos` | `map<string, ObjectInfo>` | metadata sidecar keyed by MID |
| `sequence_number` | `uint64` | session ordering, monotonic per peer |

`WantEntry`: `mid`, `priority` (int32; currently unscheduled — see
finding.txt NETPKG-022), `send_dont_have`, `want_type`.

## Limits

- Frame cap: 16 MiB.
- Sessions idle out after 60s without activity.
- AIMD congestion window: init 8, cap 128, halve on write error
  (see finding.txt NETPKG-003/014 for known gaps).
