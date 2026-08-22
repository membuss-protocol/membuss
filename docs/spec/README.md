# Membuss Wire Protocol Specifications

Normative reference for every peer-to-peer protocol membuss nodes speak.
Each doc states: protocol ID, framing, message schema, limits, and the
versioning/negotiation plan for a future v3.

| Doc | Protocol ID |
|-----|-------------|
| [memex-v2.md](memex-v2.md) | `/membuss/memex/2.0.0` (+ `/membuss/memex-bloom/2.0.0`) |
| [pex.md](pex.md) | `/membuss/pex/1.0.0` |
| [dht-namespaces.md](dht-namespaces.md) | `/membuss/dht/1.0.0` (kad record validator namespaces) |
| [edge-rpc.md](edge-rpc.md) | `/membuss/edge/exec/v1` |
| [descriptor.md](descriptor.md) | n/a — `.mbuss` container format |

## Versioning plan

Current protocols carry their version inside the protocol ID string
(`/2.0.0`, `/1.0.0`). A breaking v3 must:

1. Register a new protocol ID (`/membuss/memex/3.0.0`); never mutate an
   existing one.
2. Keep the old handler mounted during the deprecation window.
3. Prefer capability negotiation via a `Hello` frame once any protocol
   gains per-connection options; until then the protocol-ID match IS the
   contract.
