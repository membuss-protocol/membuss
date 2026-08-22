# edge RPC — remote function execution

Protocol ID: `/membuss/edge/exec/v1` (`net/edge_rpc/protocol.go:ProtocolID`).

## Framing

JSON messages over libp2p msgio length-delimited streams. This violates
the protobuf-everywhere convention and is tracked for migration
(finding.txt NETPKG-030).

## Messages

`RPCRequest`: `mid`, `path`, `code` (WASM/JS bytes), `runtime`
(go/wasm or js), `req` (`memedge.Request`), `limits`.

`RPCResponse`: `response` (`memedge.Response`), `peer_id`, `tier`,
`error`.

## Limits and security posture (current)

- Token bucket: 20 req/s per peer.
- Hardcoded 10s exec timeout (finding.txt NETPKG-031).
- **Unauthenticated execution** — any connected peer may run code on a
  node. Default-off allowlist + signed authorization is the planned fix
  (finding.txt NETPKG-030). Do not expose nodes running this protocol
  to untrusted swarms until that lands.

## Tiering

Requests fall through publisher tiers: gateway (`TierPublisher`) then
peer swarm (`TierPeer`); status >= 500 should retry next tier
(finding.txt NETPKG-031 tracks the current gap).
