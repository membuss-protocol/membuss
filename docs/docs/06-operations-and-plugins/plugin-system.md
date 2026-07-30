---
id: plugin-system
title: Universal Plugin Extension Architecture
sidebar_label: Universal Plugin Extensions
---

# Universal Plugin Extension Architecture

Membuss features a **Universal Plugin System** (`core/plugin`) allowing developers to extend node capabilities without modifying core source code.

---

## 1. Lifecycle Event Hooks (`StorageHooks`)

Plugins intercept block storage and retrieval via Go interfaces:

```go
type StorageHooks interface {
    TriggerBeforeBlockPut(ctx context.Context, block *Block) (*Block, error)
    TriggerAfterBlockPut(ctx context.Context, m mid.MID, size int64)
    TriggerBeforeBlockGet(ctx context.Context, m mid.MID) (mid.MID, error)
    TriggerAfterBlockGet(ctx context.Context, m mid.MID, data []byte) ([]byte, error)
    TriggerAfterBlockDel(ctx context.Context, m mid.MID)
}
```

---

## 2. API & CLI Subcommand Extensions

Plugins can register:
- **REST Endpoints**: Custom HTTP routes attached to local Node API.
- **gRPC Services**: Custom gRPC methods.
- **CLI Subcommands**: Custom CLI tools callable via `membuss inspector <plugin-command>`.
