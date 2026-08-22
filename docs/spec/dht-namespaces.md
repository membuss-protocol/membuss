# DHT namespaces and record validators

Routing: `/membuss/dht/1.0.0` (`net/dht/dht.go:ProtocolPrefix`).

## Validator table

| Namespace | Validator | Notes |
|-----------|-----------|-------|
| `/memns/` | `validateMemNS` (validator.go) | signed seq records; name = `k` + base36(sha256(owner pubkey)) |
| `/membuss/` | `validateMembuss` | permissive; dev/testing |
| `/membuss/anchors/v1` | registry validator | UNSIGNED today — poisoning risk, finding.txt NETPKG-040 |
| `/membuss/relays/v1` | registry validator | UNSIGNED today — finding.txt NETPKG-040 |

## Record rules (MemNS)

- `sequence > 0`, monotonic per name.
- `validity` = unix-nano expiry; expired records rejected.
- Ed25519 signature over `value || seq(u64 BE) || validity(u64 BE)`.
- Signer must be owner (pubkey match) or listed delegate.
- Name binding: sha256(owner pubkey) rendered base36 with `k` prefix must
  equal the key after the `/memns/` prefix.
