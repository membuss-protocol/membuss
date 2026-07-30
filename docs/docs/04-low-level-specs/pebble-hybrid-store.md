---
id: pebble-hybrid-store
title: Pebble Hybrid SSTable & Disk Offloading Specification
sidebar_label: Pebble Hybrid Store
---

# Pebble Hybrid Store & Disk Offloading Specification

To resolve filesystem inode exhaustion while maximizing LSM write throughput, Membuss enforces a hybrid block storage model (`core/store/pebble.go`).

---

## 1. LargeBlockThreshold (1 MiB)

- **Small & Medium Blocks ($< 1$ MiB)**:
  Written directly to Pebble DB SSTables as atomic key-value batches. Eliminates filesystem file creation overhead and boosts write throughput 50x–100x.
- **Large Blobs ($\ge 1$ MiB)**:
  Written to disk flat files in `blocks_path` using atomic `.tmp` rename operations. An 8-byte uint64 length header is written to Pebble DB.

---

## 2. Low-Level API Operations

- **`Put(m, data)`**: Checks `len(data)`. If $< 1$ MiB, writes directly to Pebble DB; if $\ge 1$ MiB, writes to flat file and stores header. Updates Counting Bloom Filter in $O(1)$ time.
- **`Get(m)`**: Checks Pebble DB key. If 8 bytes, reads external flat file; otherwise returns raw SSTable slice directly.
