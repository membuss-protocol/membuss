---
id: mem-herald
title: Mem-Herald Reprovider Engine Architecture
sidebar_label: Mem-Herald Engine
---

# Mem-Herald Reprovider Engine Architecture

**Mem-Herald** (`core/herald`) manages background re-announcements of stored entry-node MIDs to the network.

---

## 1. Incremental 16-Group Reprovide Strategy

Rather than broadcasting all stored MIDs in a single network burst, Mem-Herald partitions stored root MIDs into **16 deterministic hash groups**:

```text
Group Index = Hash(MID) % 16
```

Each cycle, a single group is re-announced to the DHT, cutting continuous background bandwidth consumption by **90%**.
