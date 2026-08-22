# `.mbuss` descriptor container

Defined in `core/descriptor/descriptor.go` (`Serialize`/`Parse`).

```
+--------+---------+-------------------+---------------+
| "MEMB" | version | protobuf payload  | sha256        |
| 4 B    | 1 B     | (variable)        | trailer 32 B  |
+--------+---------+-------------------+---------------+
```

- Magic: ASCII `MEMB`.
- Version byte: only the current value is accepted (`errBadVersion`);
  unknown versions must be rejected, never skipped.
- Payload: `membuss.v1.DescriptorPayload` — root MID, total size, block
  count, name, mime type, created-at, chunker id/size, bootstrap peers,
  MemNS name, signature, block list (MID+size+index each), optional
  erasure info (data/parity shard counts, shard MIDs).
- Trailer: SHA-256 over exactly the payload bytes. Parsers MUST verify
  it before trusting anything else (finding.txt CORE-I1 hardening list).
