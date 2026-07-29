---
id: cli-reference
title: Complete CLI Command Reference
sidebar_label: CLI Command Reference
---

# Complete CLI Command Reference

The unified `membuss` binary provides CLI subcommands for node management, data ingestion, DAG inspection, and network status.

---

## 1. Content Management Subcommands

```bash
# Add file or directory to network
membuss add ./path/to/file [-r] [--chunker fixed|rabin|fastcdc]

# Fetch content by MID
membuss get <mid> -o ./destination

# Inspect DAG node descriptor
membuss dag <mid>

# Seal content (pin recursively)
membuss seal <mid>

# Unseal content
membuss unseal <mid>

# Remove content locally
membuss rm <mid>
```

---

## 2. Daemon & Network Subcommands

```bash
# Start local node daemon
membuss daemon start --config ./membuss.yaml

# Check daemon status
membuss daemon status

# List connected libp2p peers
membuss peers

# Trigger local garbage collection
membuss gc [--min-age 1h]
```
