---
id: merkle-dag-memfs
title: Merkle DAG & MemFS Abstraction Layer
sidebar_label: Merkle DAG & MemFS
---

# Merkle DAG & MemFS Abstraction Layer

**MemFS** (`core/dag`) maps standard file systems, directories, symbolic links, and metadata onto content-addressed Merkle Directed Acyclic Graphs (DAGs).

---

## 1. Protobuf Specifications (`proto/membuss.proto`)

All DAG nodes are serialized via Protocol Buffers:

```protobuf
enum NodeType {
  RAW = 0;
  FILE = 1;
  DIR = 2;
  SYMLINK = 3;
  METADATA = 4;
}

message Link {
  bytes mid = 1;         // Raw MID binary payload
  string name = 2;       // File or directory name
  uint64 size = 3;       // Size of child target in bytes
  NodeType type = 4;     // Child node type
}

message DAGNode {
  NodeType type = 1;      // Primary node type
  bytes data = 2;        // Inline raw payload (if applicable)
  repeated Link links = 3; // Child links
  uint64 total_size = 4; // Total size of DAG subtree
}
```

---

## 2. Directory Tree Representation

When ingesting a directory structure:
- Individual files become chunked `FILE` DAG subtrees.
- Directory nodes encapsulate ordered arrays of `Link` descriptors.
- The root directory MID represents an immutable, tamper-evident cryptographic hash of the entire file tree.

```
                    Root Directory [DIR]
                 MID: membafzbeidr...root
                            │
          ┌─────────────────┴─────────────────┐
          ▼                                   ▼
    docs/ [DIR]                       index.html [FILE]
MID: membafzbeidr...docs            MID: membafzbeidr...html
          │
    intro.md [FILE]
MID: membafzbeidr...md
```
