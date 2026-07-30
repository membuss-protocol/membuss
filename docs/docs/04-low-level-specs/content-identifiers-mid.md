---
id: content-identifiers-mid
title: MID (Mem ID) Binary Specification & Multihashes
sidebar_label: Content Identifiers (MID)
---

# MID Binary Specification & Multihash Engine

A **Mem ID (MID)** (`core/mid`) is Membuss's core content identifier based on CIDv1 formatted with a mandatory `mem` prefix.

---

## 1. String Representation

```text
mem + b + base32lower( <version> <codec> <multihash> )
```

- `mem`: Static Membuss identifier prefix string.
- `b`: Multibase prefix (RFC 4648 Base32 lower without padding).
- `<version>`: `0x01` (CIDv1).
- `<codec>`: Content codec (`0x55` for `raw`, `0x72` for `CodecMemFS`).
- `<multihash>`: Self-describing multihash digest envelope.

---

## 2. Multihash Algorithm Support Table

| Algorithm Name | Multihash Code | Hex Code | Output Size | Default in Membuss |
|---|---|---|---|---|
| **BLAKE3** | `multihash.BLAKE3` | `0x1e` | 32 Bytes | **YES (Default)** |
| **SHA2-256** | `multihash.SHA2_256` | `0x12` | 32 Bytes | Supported |
| **SHA2-512** | `multihash.SHA2_512` | `0x13` | 64 Bytes | Supported |
| **KECCAK-256** | `multihash.KECCAK_256` | `0x1b` | 32 Bytes | Supported |
| **SHAKE-256** | `multihash.SHAKE_256` | `0x1d` | 32 Bytes | Supported |
